'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

function popup(initialStatus, disconnectResponse, confirmAnswer=true) {
  const elements = Object.fromEntries(['status', 'connection-toggle', 'open-chat', 'base', 'token'].map(id => [id, {
    textContent: '', value: '', disabled: false,
  }]));
  elements.base.value = 'http://127.0.0.1:4175';
  elements.token.value = 'test-token';
  let currentStatus = initialStatus;
  const messages = [];
  const context = vm.createContext({
    document: { getElementById: id => elements[id] },
    setInterval: () => {},
    confirm:()=>confirmAnswer,
    window:{close(){}},
    chrome: {
      sidePanel:{open:async()=>{}},
      tabs: { query: async () => [{ id: 42, url: 'https://www.linkedin.com/in/test', title: 'Profile' }] },
      runtime: { sendMessage: async message => {
        messages.push(message);
        if (message.type === 'ferro-status') return currentStatus;
        if (message.type === 'ferro-disconnect') {
          if (disconnectResponse?.error) return disconnectResponse;
          currentStatus = { configured: false, connected: false };
          return disconnectResponse || { ok: true };
        }
        if (message.type === 'ferro-connect') {
          if (currentStatus.configured && currentStatus.tabId!==42 && !message.confirmDisconnectTab) return {requiresConfirmation:true,pairedTab:String(currentStatus.tabId)};
          currentStatus = { configured: true, connected: true, base: message.connection.base, tabId: 42 };
          return { ok: true };
        }
      } },
    },
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, 'popup.js'), 'utf8'), context);
  return { elements, messages, context };
}

async function settle() {
  await new Promise(resolve => setImmediate(resolve));
}

test('one button reflects a live connection and disconnects it', async () => {
  const { elements, messages } = popup({ configured: true, connected: true, base: 'http://127.0.0.1:4175', tabId: 42 });
  await settle();
  assert.equal(elements['connection-toggle'].textContent, 'Disconnect');
  assert.match(elements.status.textContent, /Connected to this tab/);
  await elements['connection-toggle'].onclick();
  assert.equal(messages.at(-2).type, 'ferro-disconnect');
  assert.equal(elements['connection-toggle'].textContent, 'Connect this tab');
  assert.match(elements.status.textContent, /Disconnected/);
});

test('stale pairing is visible and can still be disconnected', async () => {
  const { elements } = popup({ configured: true, connected: false, base: 'http://127.0.0.1:4175', tabId: 42, transportError: 'bridge unavailable' });
  await settle();
  assert.equal(elements['connection-toggle'].textContent, 'Disconnect');
  assert.match(elements.status.textContent, /Trying to reconnect/);
  assert.match(elements.status.textContent, /bridge unavailable/);
  await elements['connection-toggle'].onclick();
  assert.equal(elements['connection-toggle'].textContent, 'Connect this tab');
});

test('disconnected saved credentials leave bridge fields editable', async () => {
  const { elements } = popup({ configured: false, connected: false, base: 'http://127.0.0.1:4175', credentialsAvailable: true });
  await settle();
  assert.equal(elements.base.value, 'http://127.0.0.1:4175');
  assert.equal(elements.base.disabled, false);
  assert.equal(elements.token.disabled, false);
});

test('changing bridge URL uses the newly entered token', async () => {
  const { elements, messages } = popup({ configured: false, connected: false, base: 'http://127.0.0.1:4175', credentialsAvailable: true });
  await settle();
  elements.base.value = 'http://127.0.0.1:4176';
  elements.token.value = 'new-bridge-token';
  await elements['connection-toggle'].onclick();
  const connect = messages.find(message => message.type === 'ferro-connect');
  assert.equal(connect.connection.base, 'http://127.0.0.1:4176');
  assert.equal(connect.connection.token, 'new-bridge-token');
});

test('disconnected button pairs the active tab with the entered bridge', async () => {
  const { elements, messages } = popup({ configured: false, connected: false });
  await settle();
  await elements['connection-toggle'].onclick();
  const connect = messages.find(message => message.type === 'ferro-connect');
  assert.equal(connect.connection.base, 'http://127.0.0.1:4175');
  assert.equal(connect.connection.tabId, 42);
  assert.equal(elements['connection-toggle'].textContent, 'Disconnect');
});

test('failed disconnect remains visible while connection is configured', async () => {
  const { elements } = popup({ configured: true, connected: true, base: 'http://127.0.0.1:4175', tabId: 42 }, {error: 'A browser action is still running.'});
  await settle();
  await elements['connection-toggle'].onclick();
  assert.match(elements.status.textContent, /A browser action is still running/);
  assert.equal(elements['connection-toggle'].textContent, 'Disconnect');
});
test('local disconnect preserves bridge warning', async () => {
  const { elements } = popup({ configured: true, connected: true, base: 'http://127.0.0.1:4175', tabId: 42 }, {ok: true, warning: 'Bridge did not confirm disconnect.'});
  await settle();
  await elements['connection-toggle'].onclick();
  assert.match(elements.status.textContent, /Bridge did not confirm disconnect/);
});

test('switch to another tab reuses saved credentials after confirmation',async()=>{
 const {elements,messages}=popup({configured:true,connected:true,base:'http://127.0.0.1:4175',tabId:41});
 await settle();elements.token.value='';
 assert.equal(elements['connection-toggle'].textContent,'Connect this tab');
 assert.equal(elements['open-chat'].hidden,true);
 await elements['connection-toggle'].onclick();
 const requests=messages.filter(m=>m.type==='ferro-connect');
 assert.equal(requests.length,2);assert.equal(requests[1].confirmDisconnectTab,'41');
 assert.equal(requests[1].connection.token,'');assert.equal(elements['open-chat'].hidden,false);
});
test('cancelled switch preserves the existing pairing',async()=>{
 const {elements,messages}=popup({configured:true,connected:true,base:'http://127.0.0.1:4175',tabId:41},undefined,false);
 await settle();await elements['connection-toggle'].onclick();
 assert.equal(messages.filter(m=>m.type==='ferro-connect').length,1);
 assert.equal(elements['open-chat'].hidden,true);assert.match(elements.status.textContent,/Connection unchanged/);
});

test('popup accepts hosted endpoint and rejects arbitrary remote endpoints',async()=>{
 const hosted=popup({configured:false,connected:false});
 await settle(); hosted.elements.base.value='https://ferro.sire.run/bridge';
 await hosted.elements['connection-toggle'].onclick();
 assert.equal(hosted.messages.find(m=>m.type==='ferro-connect').connection.base,'https://ferro.sire.run/bridge');

 const invalid=popup({configured:false,connected:false});
 await settle(); invalid.elements.base.value='https://remote.example/bridge';
 await invalid.elements['connection-toggle'].onclick();
 assert.equal(invalid.messages.some(m=>m.type==='ferro-connect'),false);
 assert.match(invalid.elements.status.textContent,/https:\/\/ferro\.sire\.run\/bridge/);
});
