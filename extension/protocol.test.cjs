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

test('missing receiver is attached before a command, without retrying delivered input', async()=>{
  const {context}=worker();
  let ready=false, injections=0, inputs=0;
  context.chrome.scripting={executeScript:async request=>{
    assert.deepEqual(Array.from(request.files),['adapter.js','content.js']);
    assert.equal(request.target.tabId,42);
    assert.deepEqual(Array.from(request.target.frameIds),[0]);
    injections++;ready=true;
  }};
  context.chrome.tabs.sendMessage=async(_id,message)=>{
    if(message.type==='ferro-ping'){
      if(!ready)throw new Error('Receiving end does not exist.');
      return {ready:true};
    }
    inputs++;
    throw new Error('Response lost after input');
  };
  await assert.rejects(vm.runInContext('sendToContent(42,{type:"ferro-perform",action:{op:"click"}})',context),/Response lost/);
  assert.equal(injections,1);
  assert.equal(inputs,1,'uncertain input must not be repeated');
});

test('pairing refuses an inaccessible page before claiming connection',async()=>{
  let paired=0;
  const {context,listeners}=worker({fetch:async()=>{paired++;return {ok:true,status:204}}});
  context.chrome.tabs.sendMessage=async()=>{throw new Error('Receiving end does not exist.')};
  context.chrome.scripting={executeScript:async()=>{throw new Error('Cannot access this page')}};
  const response=await new Promise(resolve=>listeners[0]({type:'ferro-connect',connection:{base:'http://127.0.0.1:4173',token:'fixture-token',tabId:42}},{id:'test-extension',url:'chrome-extension://test-extension/sidepanel.html'},resolve));
  assert.match(response.error,/cannot attach/);
  assert.equal(paired,0);
});

test('switching tabs releases the old pairing before pairing the new tab',async()=>{
  const previous={base:'http://127.0.0.1:4173',token:'old-token',tabId:41};
  let stored=previous;
  const requests=[];
  const {context,listeners}=worker({fetch:async(url,options)=>{
    requests.push({url,method:options.method,tab:options.headers['X-Ferro-Tab-Id']});
    return {ok:true,status:200};
  }});
  context.chrome.storage.session.get=async()=>({connection:stored});
  context.chrome.storage.session.set=async value=>{stored=value.connection};
  context.ensureContentReady=async()=>{};
  let stopped=0,started=0;
  context.stopPolling=()=>{stopped++};
  context.startPolling=()=>{started++};
  const response=await new Promise(resolve=>listeners[0]({type:'ferro-connect',confirmDisconnectTab:'41',connection:{...previous,tabId:42}},{id:'test-extension',url:'chrome-extension://test-extension/sidepanel.html'},resolve));
  assert.equal(response.ok,true);
  assert.deepEqual(requests.map(r=>[new URL(r.url).pathname,r.method,r.tab]),[
    ['/disconnect','POST','41'],['/pair','POST','42'],
  ]);
  assert.equal(stored.tabId,42);
  assert.equal(stopped,1);
  assert.equal(started,1);
});

test('reconnecting the exact current tab does not interrupt a poll or action',async()=>{
  const connection={base:'http://127.0.0.1:4173',token:'token',tabId:41};
  const {context,listeners}=worker({fetch:async()=>{throw new Error('unexpected fetch')}});
  context.chrome.storage.session.get=async()=>({connection});
  context.ensureContentReady=async()=>{};
  let stopped=0,started=0;
  context.stopPolling=()=>{stopped++};
  context.startPolling=()=>{started++};
  const response=await new Promise(resolve=>listeners[0]({type:'ferro-connect',connection},{id:'test-extension',url:'chrome-extension://test-extension/sidepanel.html'},resolve));
  assert.equal(response.ok,true);
  assert.equal(response.error,undefined);
  assert.equal(stopped,0);
  assert.equal(started,0);
});

test('disconnect keeps the connection and resumes polling when the server rejects it',async()=>{
  const connection={base:'http://127.0.0.1:4173',token:'token',tabId:41};
  let removed=0,stopped=0,started=0;
  const {context,listeners}=worker({fetch:async()=>({ok:false,status:409,text:async()=> 'an action reply is still pending'})});
  context.chrome.storage.session.get=async()=>({connection});
  context.chrome.storage.session.remove=async()=>{removed++};
  context.stopPolling=()=>{stopped++};
  context.startPolling=()=>{started++};
  const response=await new Promise(resolve=>listeners[0]({type:'ferro-disconnect'},{id:'test-extension',url:'chrome-extension://test-extension/popup.html'},resolve));
  assert.match(response.error,/action reply is still pending/);
  assert.equal(removed,0);
  assert.equal(stopped,1);
  assert.equal(started,1);
});

test('disconnect does not stop polling while a browser action is active',async()=>{
  let stopped=0,fetches=0;
  const {context,listeners}=worker({fetch:async()=>{fetches++;return {ok:true,status:204}}});
  vm.runInContext('activeAction = {commandID:"running"}',context);
  context.stopPolling=()=>{stopped++};
  const response=await new Promise(resolve=>listeners[0]({type:'ferro-disconnect'},{id:'test-extension',url:'chrome-extension://test-extension/popup.html'},resolve));
  assert.match(response.error,/action is still running/);
  assert.equal(stopped,0);
  assert.equal(fetches,0);
});

test('failed tab switch restores the previous pairing and resumes polling',async()=>{
  const previous={base:'http://127.0.0.1:4173',token:'old-token',tabId:41};
  let stored=previous;
  const requests=[];
  const {context,listeners}=worker({fetch:async(url,options)=>{
    const tab=options.headers['X-Ferro-Tab-Id'];
    requests.push({url,method:options.method,tab});
    if (new URL(url).pathname==='/pair' && tab==='42') return {ok:false,status:403,text:async()=> 'access denied'};
    return {ok:true,status:200};
  }});
  context.chrome.storage.session.get=async()=>({connection:stored});
  context.chrome.storage.session.set=async value=>{stored=value.connection};
  context.ensureContentReady=async()=>{};
  let stopped=0,started=0;
  context.stopPolling=()=>{stopped++};
  context.startPolling=()=>{started++};
  const response=await new Promise(resolve=>listeners[0]({type:'ferro-connect',confirmDisconnectTab:'41',connection:{...previous,tabId:42}},{id:'test-extension',url:'chrome-extension://test-extension/sidepanel.html'},resolve));
  assert.match(response.error,/Pairing failed: access denied/);
  assert.deepEqual(requests.map(r=>[new URL(r.url).pathname,r.method,r.tab]),[
    ['/disconnect','POST','41'],['/pair','POST','42'],['/pair','POST','41'],
  ]);
  assert.equal(stored.tabId,41);
  assert.equal(stopped,1);
  assert.equal(started,1);
});

test('concurrent readiness checks share one attachment',async()=>{
  const {context}=worker();
  let ready=false,injections=0;
  context.chrome.tabs.sendMessage=async()=>{if(!ready)throw new Error('missing');return {ready:true}};
  context.chrome.scripting={executeScript:async()=>{injections++;ready=true}};
  await vm.runInContext('Promise.all([ensureContentReady(42),ensureContentReady(42)])',context);
  assert.equal(injections,1);
});

test('reinjecting the content relay never duplicates command listeners',async()=>{
  const listeners=[];let actions=0;
  const context=vm.createContext({
    chrome:{runtime:{sendMessage:()=>{},onMessage:{addListener:f=>listeners.push(f)}}},
    FerroAdapter:{perform:async()=>{actions++;return {}}},
  });
  const source=fs.readFileSync(path.join(__dirname,'content.js'),'utf8');
  vm.runInContext(source,context);vm.runInContext(source,context);
  assert.equal(listeners.length,1);
  await new Promise(resolve=>listeners[0]({type:'ferro-perform',action:{}},{},resolve));
  assert.equal(actions,1);
});

test('popup status distinguishes live and stale pairing without revealing the token', async()=>{
  const connection={base:'http://127.0.0.1:4175',token:'private-token',tabId:42};
  const {context,listeners}=worker();
  context.testConnection=connection;
  vm.runInContext('chrome.storage.session.get = async () => ({connection: testConnection})',context);
  const sender={id:'test-extension',url:'chrome-extension://test-extension/popup.html'};
  const ask=()=>new Promise(resolve=>listeners[0]({type:'ferro-status'},sender,resolve));
  const stale=await ask();
  assert.equal(stale.configured,true);
  assert.equal(stale.connected,false);
  assert.equal(JSON.stringify(stale).includes('private-token'),false);
  vm.runInContext('lastPollSuccessAt = Date.now()',context);
  const live=await ask();
  assert.equal(live.connected,true);
});

test('startup repairs disabled panel overrides without changing pairing', async () => {
  const {context}=worker();
  const options=[];
  let behavior;
  context.chrome.sidePanel={setOptions:async value=>options.push(value),setPanelBehavior:async value=>{behavior=value;}};
  context.chrome.tabs.query=async()=>[{id:42},{id:43}];
  context.chrome.storage.session.set=async()=>{throw new Error('Panel recovery must not change pairing');};
  await vm.runInContext('restoreSidePanelAccess()',context);
  assert.equal(behavior.openPanelOnActionClick,false);
  assert.deepEqual(options.map(x=>x.tabId),[undefined,42,43]);
  assert.ok(options.every(x=>x.enabled && x.path==='sidepanel.html'));
});

for (const busy of [false,true]) test(`lost Chrome pairing state: connect recovers only when idle (busy=${busy})`,async()=>{
  const requests=[];let stored=null;
  const {context,listeners}=worker({fetch:async(url,options)=>{
    const route=new URL(url).pathname;requests.push([route,options.headers['X-Ferro-Tab-Id']]);
    if(route==='/chat/status')return {ok:true,json:async()=>({paired_tab:'41',busy,leased:false})};
    if(route==='/pair' && requests.length===1)return {ok:false,status:409,text:async()=> 'already paired'};
    return {ok:true,status:204};
  }});
  context.ensureContentReady=async()=>{};context.startPolling=()=>{};
  context.chrome.storage.session.set=async value=>{stored=value.connection};
  const response=await new Promise(resolve=>listeners[0]({type:'ferro-connect',confirmDisconnectTab:'41',connection:{base:'http://127.0.0.1:4175',token:'token',tabId:42}},{id:'test-extension',url:'chrome-extension://test-extension/sidepanel.html'},resolve));
  if(busy){assert.match(response.error,/running|busy/);assert.equal(stored,null);assert.equal(requests.some(x=>x[0]==='/disconnect'),false);}
  else {assert.equal(response.ok,true);assert.equal(stored.tabId,42);assert.deepEqual(requests.map(x=>x[0]),['/chat/status','/disconnect','/pair']);assert.equal(requests[1][1],'41');}
});

test('switching an existing pairing requires confirmation before any disconnect',async()=>{
 const previous={base:'http://127.0.0.1:4175',token:'token',tabId:41};
 const {context,listeners}=worker({fetch:async()=>{throw new Error('Must not contact service before confirmation');}});
 context.chrome.storage.session.get=async()=>({connection:previous});context.ensureContentReady=async()=>{};
 const response=await new Promise(resolve=>listeners[0]({type:'ferro-connect',connection:{...previous,tabId:42}},{id:'test-extension',url:'chrome-extension://test-extension/sidepanel.html'},resolve));
 assert.equal(response.requiresConfirmation,true);assert.equal(response.pairedTab,'41');
});

test('lost local pairing asks for confirmation before releasing service pairing',async()=>{
 const routes=[];
 const {context,listeners}=worker({fetch:async(url)=>{routes.push(new URL(url).pathname);return {ok:true,json:async()=>({paired_tab:'41',busy:false,leased:false})};}});
 context.ensureContentReady=async()=>{};
 const response=await new Promise(resolve=>listeners[0]({type:'ferro-connect',connection:{base:'http://127.0.0.1:4175',token:'token',tabId:42}},{id:'test-extension',url:'chrome-extension://test-extension/sidepanel.html'},resolve));
 assert.equal(response.requiresConfirmation,true);assert.deepEqual(routes,['/chat/status']);
});

test('disconnected panel closes only on its tab and toolbar can reopen it',async()=>{
 const {context}=worker();const calls=[];
 context.chrome.sidePanel={setOptions:async value=>calls.push(['options',value.tabId,value.enabled,value.path]),open:async value=>calls.push(['open',value.tabId])};
 await vm.runInContext('closeDisconnectedPanel(41)',context);
 await vm.runInContext('openFerroPanel({id:41})',context);
 assert.deepEqual(calls,[['options',41,false,'sidepanel.html'],['options',41,true,'sidepanel.html'],['open',41]]);
});
