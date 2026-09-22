'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

function worker(overrides = {}) {
  const listeners = [];
  const context = vm.createContext({
    console, URL, AbortController, AbortSignal, setTimeout, clearTimeout,
    fetch: async () => ({ok:true,status:204}),
    chrome: {
      storage: {session:{get:async()=>({}),set:async()=>{},remove:async()=>{}}},
      tabs:{onRemoved:{addListener(){}},onUpdated:{addListener(){},removeListener(){}},get:async()=>({url:'https://allowed.test/'}),sendMessage:async()=>({result:'content'})},
      runtime:{id:'test-extension',getURL:p=>`chrome-extension://test-extension/${p}`,onMessage:{addListener:f=>listeners.push(f)}},
    },
    ...overrides,
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname,'background.js'),'utf8'), context);
  return {context,listeners};
}

test('poll includes the pairing tab id and bearer credential', async () => {
  let sent;
  const {context}=worker({fetch:async(url,opts)=>{sent={url,opts};return {ok:true,status:204}}});
  await vm.runInContext('fetchNext("http://127.0.0.1:4173","test-token",42)',context);
  assert.equal(sent.opts.headers['X-Ferro-Tab-Id'],'42');
  assert.equal(sent.opts.headers.Authorization,'Bearer test-token');
});
test('origin change and expired commands never reach content', async()=>{
  const {context}=worker();
  context.deadline=Date.now()+10000;
  const result=await vm.runInContext('handleAction(42,{op:"click",origin:"https://different.test",deadlineMs:deadline})',context);
  assert.equal(result.code,'origin_changed');
  await assert.rejects(vm.runInContext('handleAction(42,{op:"click",deadlineMs:1})',context),/expired/);
});
test('page content cannot pair a new tab',async()=>{
  const {listeners}=worker();
  const response=await new Promise(resolve=>listeners[0]({type:'ferro-connect',connection:{}},{id:'test-extension',url:'https://allowed.test/',tab:{id:42}},resolve));
  assert.match(response.error,/popup/);
});
