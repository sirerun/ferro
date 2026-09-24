'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');

function worker(overrides = {}) {
  const listeners = [];
  const localState = {};
  const context = vm.createContext({
    console, URL, AbortController, AbortSignal, setTimeout, clearTimeout,
    crypto:{randomUUID:()=> 'browser-context-default-1234'},
    fetch: async () => ({ok:true,status:204}),
    chrome: {
      storage: {session:{get:async()=>({}),set:async()=>{},remove:async()=>{}},local:{get:async key=>({[key]:localState[key]}),set:async value=>Object.assign(localState,value)}},
      tabs:{onRemoved:{addListener(){}},onUpdated:{addListener(){},removeListener(){}},get:async()=>({url:'https://allowed.test/'}),sendMessage:async()=>({result:'content'})},
      runtime:{id:'test-extension',getURL:p=>`chrome-extension://test-extension/${p}`,onMessage:{addListener:f=>listeners.push(f)}},
    },
    ...overrides,
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname,'background.js'),'utf8'), context);
  return {context,listeners};
}

function fakeClockWorker(fetch) {
  const clock={now:1000};
  class FakeDate extends Date { static now(){return clock.now;} }
  const {context,listeners}=worker({
    fetch, Date:FakeDate,
    setTimeout:(callback,ms)=>{clock.now+=ms;queueMicrotask(callback);return clock.now;},
    clearTimeout:()=>{},
  });
  return {context,listeners,clock};
}

function connectHosted(listeners, {tabId=42,token='pilot-token',confirmDisconnectTab}={}) {
  return new Promise(resolve=>listeners[0]({type:'ferro-connect',connection:{base:'https://ferro.sire.run/bridge',token,tabId},confirmDisconnectTab},{id:'test-extension',url:'chrome-extension://test-extension/popup.html'},resolve));
}

test('poll includes the pairing tab id and bearer credential', async () => {
  let sent;
  const {context}=worker({fetch:async(url,opts)=>{sent={url,opts};return {ok:true,status:204}}});
  await vm.runInContext('fetchNext("http://127.0.0.1:4173","test-token",42)',context);
  assert.equal(sent.opts.headers['X-Ferro-Tab-Id'],'42');
  assert.equal(sent.opts.headers.Authorization,'Bearer test-token');
});
test('hosted polls carry the installation identity separately from the numeric Chrome tab id', async()=>{
  let sent;
  const {context}=worker({fetch:async(url,opts)=>{sent=opts;return {ok:true,status:204}}});
  await vm.runInContext('fetchNext("https://ferro.sire.run/bridge","test-token",42,"browser-context-a-123456")',context);
  assert.equal(sent.headers['X-Ferro-Tab-Id'],'42');
  assert.equal(sent.headers['X-Ferro-Browser-Id'],'browser-context-a-123456');
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

test('background accepts only local HTTP or the approved hosted HTTPS bridge', async()=>{
  for (const [base, accepted] of [
    ['http://127.0.0.1:4173', true],
    ['http://localhost:4173', true],
    ['https://ferro.sire.run/bridge', true],
    ['http://ferro.sire.run/bridge', false],
    ['https://ferro.sire.run/other', false],
    ['https://remote.example/bridge', false],
    ['http://remote.example:4173', false],
  ]) {
    let requests = 0;
    const {context,listeners}=worker({fetch:async(url)=>{requests++;return new URL(url).pathname.endsWith('/wake')?{ok:true,status:202}:{ok:true,status:200,json:async()=>({busy:false,leased:false,paired_tab:null}),text:async()=>''}}});
    context.ensureContentReady=async()=>{};
    const response=await new Promise(resolve=>listeners[0]({type:'ferro-connect',connection:{base,token:'fixture-token',tabId:42}},{id:'test-extension',url:'chrome-extension://test-extension/popup.html'},resolve));
    assert.equal(!!response.ok,accepted,base);
    if (!accepted) assert.match(response.error,/local bridge URL or https:\/\/ferro\.sire\.run\/bridge/);
    assert.equal(requests,accepted ? (base === 'https://ferro.sire.run/bridge' ? 3 : 2) : 0,base);
    context.stopPolling();
  }
});

test('hosted cold start wakes, retries 503 readiness, then pairs after authenticated readiness',async()=>{
  const routes=[];let statuses=0,stored=null;
  const {context,listeners}=fakeClockWorker(async(url,options={})=>{
    const path=new URL(url).pathname;routes.push([path,options.method,options.headers?.Authorization]);
    if(path.endsWith('/wake'))return {ok:true,status:202};
    if(path.endsWith('/chat/status')){
      statuses++;
      if(statuses===1)return {ok:false,status:503};
      return {ok:true,status:200,json:async()=>({busy:false,leased:false,paired_tab:null})};
    }
    if(path.endsWith('/pair'))return {ok:true,status:204};
    throw new Error(`unexpected request ${path}`);
  });
  context.ensureContentReady=async()=>{};
  context.chrome.storage.session.get=async()=>({connection:stored});
  context.chrome.storage.session.set=async value=>{stored=value.connection};
  context.startPolling=()=>{};context.restoreSidePanelAccess=async()=>{};
  const response=await connectHosted(listeners);
  assert.equal(response.ok,true);
  assert.deepEqual(routes.map(([path])=>path),['/bridge/wake','/bridge/chat/status','/bridge/chat/status','/bridge/pair']);
  assert.ok(routes.every(([, ,authorization])=>authorization==='Bearer pilot-token'));
  assert.equal(stored.browserId,'browser-context-default-1234');
});

test('hosted readiness reasserts wake after a draining sleep can undo the first wake',async()=>{
  let wakes=0,statuses=0;
  const {context,listeners,clock}=fakeClockWorker(async(url)=>{
    const path=new URL(url).pathname;
    if(path.endsWith('/wake')){wakes++;return {ok:true,status:202};}
    if(path.endsWith('/chat/status')){
      statuses++;
      return wakes < 2
        ? {ok:false,status:503}
        : {ok:true,status:200,json:async()=>({busy:false,leased:false,paired_tab:null})};
    }
    if(path.endsWith('/pair'))return {ok:true,status:204};
    throw new Error(`unexpected request ${path}`);
  });
  context.ensureContentReady=async()=>{};context.startPolling=()=>{};context.restoreSidePanelAccess=async()=>{};
  const response=await connectHosted(listeners);
  assert.equal(response.ok,true);
  assert.equal(wakes,2);
  assert.ok(clock.now-1000>=15000);
  assert.ok(statuses>=2);
});

test('explicit same-pair hosted reconnect restarts polling without replaying actions',async()=>{
  const connection={base:'https://ferro.sire.run/bridge',token:'pilot-token',browserId:'browser-context-default-1234',tabId:42};
  let started=0,actions=0;
  const {context,listeners}=worker({fetch:async(url)=>{
    const path=new URL(url).pathname;
    if(path.endsWith('/wake'))return {ok:true,status:202};
    if(path.endsWith('/chat/status'))return {ok:true,status:200,json:async()=>({busy:false,leased:false,paired_tab:'browser-context-default-1234.42'})};
    if(path.endsWith('/pair'))return {ok:true,status:204};
    throw new Error(`unexpected request ${path}`);
  }});
  context.chrome.storage.session.get=async()=>({connection});
  context.ensureContentReady=async()=>{};context.startPolling=()=>{started++;};context.restoreSidePanelAccess=async()=>{};
  context.handleAction=async()=>{actions++;};
  const response=await connectHosted(listeners);
  assert.equal(response.ok,true);
  assert.equal(started,1);
  assert.equal(actions,0);
});

test('background hosted recovery does not cancel an explicit connection workflow',async()=>{
  const oldConnection={base:'https://ferro.sire.run/bridge',token:'pilot-token',browserId:'browser-context-default-1234',tabId:41};
  let releaseWake,wakeStarted,wakes=0;
  const started=new Promise(resolve=>{wakeStarted=resolve;});
  const pendingWake=new Promise(resolve=>{releaseWake=resolve;});
  let stored=oldConnection;
  const {context,listeners}=worker({fetch:async(url)=>{
    const path=new URL(url).pathname;
    if(path.endsWith('/wake')){wakes++;wakeStarted();return pendingWake;}
    if(path.endsWith('/chat/status'))return {ok:true,status:200,json:async()=>({busy:false,leased:false,paired_tab:null})};
    if(path.endsWith('/pair'))return {ok:true,status:204};
    return {ok:true,status:204};
  }});
  context.chrome.storage.session.get=async()=>({connection:stored});
  context.chrome.storage.session.set=async value=>{stored=value.connection;};
  context.ensureContentReady=async()=>{};context.startPolling=()=>{};context.restoreSidePanelAccess=async()=>{};
  context.connection=oldConnection;
  const manual=connectHosted(listeners,{tabId:42,confirmDisconnectTab:'browser-context-default-1234.41'});
  await started;
  const recovered=await vm.runInContext('recoverHostedConnection(connection, 7)',context);
  assert.equal(recovered,false);
  assert.equal(wakes,1);
  releaseWake({ok:true,status:202});
  assert.equal((await manual).ok,true);
  assert.equal(stored.tabId,42);
});

test('superseded explicit connect cleanup preserves newer connect priority over recovery',async()=>{
  const oldConnection={base:'https://ferro.sire.run/bridge',token:'pilot-token',browserId:'browser-context-default-1234',tabId:41};
  const wakeReleases=[];let wakeCount=0;
  const wakeStarted=[];
  let stored=oldConnection;
  const {context,listeners}=worker({fetch:async(url)=>{
    const path=new URL(url).pathname;
    if(path.endsWith('/wake')){
      const index=wakeCount++;
      wakeStarted[index]();
      return new Promise(resolve=>{wakeReleases[index]=resolve;});
    }
    if(path.endsWith('/chat/status'))return {ok:true,status:200,json:async()=>({busy:false,leased:false,paired_tab:null})};
    if(path.endsWith('/pair'))return {ok:true,status:204};
    return {ok:true,status:204};
  }});
  context.chrome.storage.session.get=async()=>({connection:stored});
  context.chrome.storage.session.set=async value=>{stored=value.connection;};
  context.ensureContentReady=async()=>{};context.startPolling=()=>{};context.restoreSidePanelAccess=async()=>{};
  context.connection=oldConnection;
  const waitWake=index=>new Promise(resolve=>{wakeStarted[index]=resolve;});
  const connectA=connectHosted(listeners,{tabId:42,confirmDisconnectTab:'browser-context-default-1234.41'});
  await waitWake(0);
  const connectB=connectHosted(listeners,{tabId:43,confirmDisconnectTab:'browser-context-default-1234.41'});
  await waitWake(1);
  wakeReleases[0]({ok:true,status:202});
  assert.match((await connectA).error,/canceled/);
  const recovered=await vm.runInContext('recoverHostedConnection(connection, 7)',context);
  assert.equal(recovered,false);
  assert.equal(wakeCount,2,'background recovery must not start a third wake');
  wakeReleases[1]({ok:true,status:202});
  assert.equal((await connectB).ok,true);
  assert.equal(stored.tabId,43);
});

test('invalid hosted token fails at wake without retry or credential disclosure',async()=>{
  let requests=0;
  const {context,listeners}=worker({fetch:async(_url,options)=>{requests++;assert.equal(options.headers.Authorization,'Bearer secret-token');return {ok:false,status:401,text:async()=> 'secret-token echoed'};}});
  context.ensureContentReady=async()=>{};
  const response=await connectHosted(listeners,{token:'secret-token'});
  assert.equal(requests,1);
  assert.match(response.error,/HTTP 401/);
  assert.equal(response.error.includes('secret-token'),false);
});

test('hosted cold start readiness times out within the 180 second budget',async()=>{
  let statuses=0,pairs=0;
  const {context,listeners,clock}=fakeClockWorker(async(url)=>{
    const path=new URL(url).pathname;
    if(path.endsWith('/wake'))return {ok:true,status:202};
    if(path.endsWith('/chat/status')){statuses++;return {ok:false,status:503};}
    if(path.endsWith('/pair'))pairs++;
    return {ok:true,status:204};
  });
  context.ensureContentReady=async()=>{};
  const response=await connectHosted(listeners);
  assert.match(response.error,/within 180 seconds/);
  assert.equal(clock.now-1000,180000);
  assert.ok(statuses>1);
  assert.equal(pairs,0);
});

test('disconnect during pending hosted wake cancels pairing and prevents resurrection',async()=>{
  let releaseWake;let wakeStarted;
  const started=new Promise(resolve=>{wakeStarted=resolve;});
  const pendingWake=new Promise(resolve=>{releaseWake=resolve;});
  let stored=null,pairs=0;
  const {context,listeners}=worker({fetch:async(url)=>{
    const path=new URL(url).pathname;
    if(path.endsWith('/wake')){wakeStarted();return pendingWake;}
    if(path.endsWith('/chat/status'))return {ok:true,status:200,json:async()=>({busy:false,leased:false,paired_tab:null})};
    if(path.endsWith('/pair'))pairs++;
    return {ok:true,status:204};
  }});
  context.ensureContentReady=async()=>{};
  context.chrome.storage.session.get=async()=>({connection:stored});
  context.chrome.storage.session.set=async value=>{stored=value.connection};
  context.chrome.storage.session.remove=async()=>{stored=null};
  context.startPolling=()=>{};context.stopPolling=()=>{};context.restoreSidePanelAccess=async()=>{};
  const connecting=connectHosted(listeners);
  await started;
  const disconnected=await new Promise(resolve=>listeners[0]({type:'ferro-disconnect'},{id:'test-extension',url:'chrome-extension://test-extension/popup.html'},resolve));
  assert.equal(disconnected.ok,true);
  releaseWake({ok:true,status:202});
  const result=await connecting;
  assert.match(result.error,/canceled/);
  assert.equal(pairs,0);
  assert.equal(stored,null);
});

test('starting a newer hosted generation prevents an older wake from storing its pair',async()=>{
  let releaseFirst;let wakeCount=0;
  const firstWake=new Promise(resolve=>{releaseFirst=resolve;});
  let stored=null,pairs=[];
  const {context,listeners}=worker({fetch:async(url,options={})=>{
    const path=new URL(url).pathname;
    if(path.endsWith('/wake')){wakeCount++;return wakeCount===1?firstWake:{ok:true,status:202};}
    if(path.endsWith('/chat/status'))return {ok:true,status:200,json:async()=>({busy:false,leased:false,paired_tab:null})};
    if(path.endsWith('/pair')){pairs.push(options.headers['X-Ferro-Tab-Id']);return {ok:true,status:204};}
    return {ok:true,status:204};
  }});
  context.ensureContentReady=async()=>{};
  context.chrome.storage.session.get=async()=>({connection:stored});
  context.chrome.storage.session.set=async value=>{stored=value.connection};
  context.startPolling=()=>{};context.restoreSidePanelAccess=async()=>{};
  const oldConnect=connectHosted(listeners,{tabId:41});
  while(wakeCount===0) await new Promise(resolve=>setImmediate(resolve));
  const newConnect=connectHosted(listeners,{tabId:42});
  assert.equal((await newConnect).ok,true);
  releaseFirst({ok:true,status:202});
  assert.match((await oldConnect).error,/canceled/);
  assert.deepEqual(pairs,['42']);
  assert.equal(stored.tabId,42);
});

test('hosted retained recovery refuses to take over a different browser identity',async()=>{
  const connection={base:'https://ferro.sire.run/bridge',token:'pilot-token',browserId:'browser-context-own-12345',tabId:42};
  const paths=[];let pairs=0;
  const {context}=worker({fetch:async(url)=>{
    const path=new URL(url).pathname;paths.push(path);
    if(path.endsWith('/wake'))return {ok:true,status:202};
    if(path.endsWith('/chat/status'))return {ok:true,status:200,json:async()=>({busy:false,leased:false,paired_tab:'browser-context-other-1234.42'})};
    if(path.endsWith('/pair'))pairs++;
    return {ok:true,status:204};
  }});
  context.chrome.storage.session.get=async()=>({connection});
  context.connection=connection;
  await assert.rejects(vm.runInContext('recoverHostedConnection(connection, 7)',context),/another browser tab/);
  assert.deepEqual(paths,['/bridge/wake','/bridge/chat/status']);
  assert.equal(pairs,0);
});

test('hosted retained recovery wakes and re-pairs only the saved browser and tab after 409',async()=>{
  const connection={base:'https://ferro.sire.run/bridge',token:'pilot-token',browserId:'browser-context-own-12345',tabId:42};
  const requests=[];let stored=connection,pairIdentity='';
  const {context}=worker({fetch:async(url,options={})=>{
    const path=new URL(url).pathname;requests.push([path,options.headers?.['X-Ferro-Browser-Id'],options.headers?.['X-Ferro-Tab-Id']]);
    if(path.endsWith('/next'))return {ok:false,status:409};
    if(path.endsWith('/wake'))return {ok:true,status:202};
    if(path.endsWith('/chat/status'))return {ok:true,status:200,json:async()=>({busy:false,leased:false,paired_tab:null})};
    if(path.endsWith('/pair')){pairIdentity=`${options.headers['X-Ferro-Browser-Id']}.${options.headers['X-Ferro-Tab-Id']}`;vm.runInContext('loopGeneration=12',context);return {ok:true,status:204};}
    return {ok:true,status:204};
  }});
  context.chrome.storage.session.get=async()=>({connection:stored});
  vm.runInContext('loopGeneration=11',context);
  context.sleep=async()=>{};
  await vm.runInContext('pollLoop(11)',context);
  assert.equal(pairIdentity,'browser-context-own-12345.42');
  assert.deepEqual(requests.map(([path])=>path),['/bridge/next','/bridge/wake','/bridge/chat/status','/bridge/pair']);
});

test('a task action whose reply fails is never rerun or sent through hosted wake recovery',async()=>{
  let actions=0,wakes=0,nextCalls=0;
  const {context}=worker({fetch:async(url)=>{
    const path=new URL(url).pathname;
    if(path.endsWith('/wake'))wakes++;
    return {ok:false,status:500};
  }});
  context.chrome.storage.session.get=async()=>({connection:{base:'https://ferro.sire.run/bridge',token:'pilot-token',browserId:'browser-context-own-12345',tabId:42}});
  vm.runInContext('loopGeneration=7',context);
  context.fetchNext=async()=>{nextCalls++;return {id:'task-1',action:{op:'click'}};};
  context.handleAction=async()=>{actions++;return {result:'clicked'};};
  context.postReply=async()=>{throw new Error('reply unavailable');};
  await vm.runInContext('pollLoop(7)',context);
  assert.equal(actions,1);
  assert.equal(nextCalls,1);
  assert.equal(wakes,0);
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

test('only the paired tab gets a panel; other tabs retain the connection popup', async () => {
 const {context}=worker();await vm.runInContext('panelUpdate',context);
 const options=[],popups=[];let behavior;
 context.chrome.sidePanel={setOptions:async value=>options.push(value),setPanelBehavior:async value=>{behavior=value;}};
 context.chrome.action={setPopup:async value=>popups.push(value)};
 context.chrome.tabs.query=async()=>[{id:42},{id:43}];
 context.chrome.storage.session.get=async()=>({connection:{tabId:42}});
 await vm.runInContext('restoreSidePanelAccess()',context);
 assert.equal(behavior.openPanelOnActionClick,true);
 assert.deepEqual(options.map(x=>[x.tabId,x.enabled]),[[undefined,false],[42,true],[43,false]]);
 assert.deepEqual(popups.map(x=>[x.tabId,x.popup]),[[42,''],[43,'popup.html']]);
 options.length=0;context.chrome.storage.session.get=async()=>({});
 await vm.runInContext('restoreSidePanelAccess()',context);
 assert.ok(options.every(x=>!x.enabled));
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

test('trusted popup switch reuses the stored token without returning it',async()=>{
 const previous={base:'http://127.0.0.1:4175',token:'saved-token',tabId:41};let stored=previous;const auth=[];
 const {context,listeners}=worker({fetch:async(url,opts)=>{auth.push(opts.headers.Authorization);return {ok:true,status:204};}});
 context.ensureContentReady=async()=>{};context.startPolling=()=>{};
 context.chrome.storage.session.get=async()=>({connection:stored});context.chrome.storage.session.set=async v=>{stored=v.connection};
 const response=await new Promise(resolve=>listeners[0]({type:'ferro-connect',connection:{base:previous.base,tabId:42},confirmDisconnectTab:'41'},{id:'test-extension',url:'chrome-extension://test-extension/popup.html'},resolve));
 assert.equal(response.ok,true);assert.equal(stored.token,'saved-token');assert.equal(stored.tabId,42);
 assert.ok(auth.every(x=>x==='Bearer saved-token'));assert.equal(JSON.stringify(response).includes('saved-token'),false);
});


test('hosted pairing scopes equal numeric tabs to separate extension contexts and requires owner disconnect', async()=>{
  let paired='';
  const events=[];
  const serverFetch=async(url,options={})=>{
    const path=new URL(url).pathname;
    const headers=options.headers||{};
    const browser=headers['X-Ferro-Browser-Id'];
    const tab=headers['X-Ferro-Tab-Id'];
    events.push({path,browser,tab});
    if(path.endsWith('/wake'))return {ok:true,status:202};
    if(path.endsWith('/chat/status'))return {ok:true,status:200,json:async()=>({busy:false,leased:false,paired_tab:paired||null})};
    if(path.endsWith('/pair')){
      if(paired)return {ok:false,status:409,text:async()=>'another browser owns the pairing'};
      paired=`${browser}.${tab}`;return {ok:true,status:204};
    }
    if(path.endsWith('/disconnect')){
      if(paired!==`${browser}.${tab}`)return {ok:false,status:409,text:async()=>'identity mismatch'};
      paired='';return {ok:true,status:204};
    }
    return {ok:true,status:204};
  };
  function hostedContext(identity){
    const {context,listeners}=worker({fetch:serverFetch,crypto:{randomUUID:()=>identity}});
    context.ensureContentReady=async()=>{};
    context.startPolling=()=>{};
    context.stopPolling=()=>{};
    context.restoreSidePanelAccess=async()=>{};
    let session={};
    context.chrome.storage.session.get=async key=>key ? {[key]:session[key]} : session;
    context.chrome.storage.session.set=async value=>{session={...session,...value}};
    context.chrome.storage.session.remove=async key=>{delete session[key]};
    return {context,listeners};
  }
  const connect=async(agent,confirmDisconnectTab)=>new Promise(resolve=>agent.listeners[0]({type:'ferro-connect',connection:{base:'https://ferro.sire.run/bridge',token:'shared-token',tabId:42},confirmDisconnectTab},{id:'test-extension',url:'chrome-extension://test-extension/popup.html'},resolve));
  const a=hostedContext('browser-context-a-123456');
  const b=hostedContext('browser-context-b-123456');
  assert.equal((await connect(a)).ok,true);
  assert.equal(paired,'browser-context-a-123456.42');
  const needsConfirmation=await connect(b);
  assert.equal(needsConfirmation.requiresConfirmation,true);
  assert.equal(needsConfirmation.pairedTab,'browser-context-a-123456.42');
  assert.equal(events.some(e=>e.path==='/disconnect'),false,'new browser must not disconnect the remote owner');
  const attempted=await connect(b,needsConfirmation.pairedTab);
  assert.match(attempted.error,/Pairing failed/);
  assert.equal(paired,'browser-context-a-123456.42','confirmed takeover cannot silently displace the owner');
  assert.equal(events.filter(e=>e.path==='/disconnect').length,0);
  const disconnected=await new Promise(resolve=>a.listeners[0]({type:'ferro-disconnect'},{id:'test-extension',url:'chrome-extension://test-extension/popup.html'},resolve));
  assert.equal(disconnected.ok,true);
  assert.equal(paired,'');
  assert.equal((await connect(b)).ok,true);
  assert.equal(paired,'browser-context-b-123456.42');
  assert.ok(events.filter(e=>e.path==='/pair').every(e=>e.tab==='42'&&e.browser));
});

test('hosted worker restart rejoins the same server pair, and explicitly pairs only after server reset', async()=>{
  const localState={};
  const routes=[];
  let paired='';
  const serverFetch=async(url,options={})=>{
    const path=new URL(url).pathname;
    const headers=options.headers||{};
    routes.push([path,headers['X-Ferro-Browser-Id'],headers['X-Ferro-Tab-Id']]);
    if(path.endsWith('/wake'))return {ok:true,status:202};
    if(path.endsWith('/chat/status'))return {ok:true,status:200,json:async()=>({busy:!!paired,leased:false,paired_tab:paired||null})};
    if(path.endsWith('/pair')){
      const identity=`${headers['X-Ferro-Browser-Id']}.${headers['X-Ferro-Tab-Id']}`;
      if(paired && paired!==identity)return {ok:false,status:409,text:async()=>'another browser owns the pairing'};
      paired=identity;
      return {ok:true,status:204};
    }
    return {ok:true,status:204};
  };
  function restartedWorker(){
    const {context,listeners}=worker({fetch:serverFetch,crypto:{randomUUID:()=> 'browser-context-restart-1234'}});
    context.chrome.storage.local.get=async key=>({[key]:localState[key]});
    context.chrome.storage.local.set=async value=>Object.assign(localState,value);
    context.chrome.storage.session.set=async()=>{};
    context.ensureContentReady=async()=>{};
    context.startPolling=()=>{};
    context.restoreSidePanelAccess=async()=>{};
    return {context,listeners};
  }
  const connect=agent=>new Promise(resolve=>agent.listeners[0]({type:'ferro-connect',connection:{base:'https://ferro.sire.run/bridge',token:'shared-token',tabId:42}},{id:'test-extension',url:'chrome-extension://test-extension/popup.html'},resolve));
  const first=restartedWorker();
  const initial=await connect(first);
  assert.equal(initial.ok,true);
  assert.equal(paired,'browser-context-restart-1234.42');
  assert.deepEqual(routes.map(r=>r[0].replace('/bridge','')),['/wake','/chat/status','/pair']);

  // A service-worker restart clears storage.session but keeps the browser's
  // local installation identity and the server's explicit pairing.
  routes.length=0;
  const afterRestart=restartedWorker();
  const rejoined=await connect(afterRestart);
  assert.equal(rejoined.ok,true);
  assert.deepEqual(routes.map(r=>r[0].replace('/bridge','')),['/wake','/chat/status','/pair']);

  // If the service itself restarted and forgot the pair, recovery explicitly
  // claims it with /pair before polling.
  paired='';
  routes.length=0;
  const afterServerRestart=restartedWorker();
  const pairedAgain=await connect(afterServerRestart);
  assert.equal(pairedAgain.ok,true);
  assert.equal(paired,'browser-context-restart-1234.42');
  assert.deepEqual(routes.map(r=>r[0].replace('/bridge','')),['/wake','/chat/status','/pair']);
});

test('hosted retained connection explicitly re-pairs after server restart', async()=>{
  const connection={base:'https://ferro.sire.run/bridge',token:'shared-token',browserId:'browser-context-live-1234',tabId:42};
  const routes=[];
  let paired='';
  const {context,listeners}=worker({fetch:async(url,options={})=>{
    const path=new URL(url).pathname;
    routes.push(path);
    if(path.endsWith('/wake'))return {ok:true,status:202};
    if(path.endsWith('/pair')){
      paired=`${options.headers['X-Ferro-Browser-Id']}.${options.headers['X-Ferro-Tab-Id']}`;
      return {ok:true,status:204};
    }
    return {ok:true,status:200,json:async()=>({busy:false,leased:false,paired_tab:null}),text:async()=>''};
  }});
  context.chrome.storage.local.get=async key=>({[key]:'browser-context-live-1234'});
  context.chrome.storage.session.get=async()=>({connection});
  context.ensureContentReady=async()=>{};
  context.startPolling=()=>{};
  context.restoreSidePanelAccess=async()=>{};

  const response=await new Promise(resolve=>listeners[0]({type:'ferro-connect',connection},{id:'test-extension',url:'chrome-extension://test-extension/popup.html'},resolve));
  assert.equal(response.ok,true);
  assert.equal(paired,'browser-context-live-1234.42');
  assert.deepEqual(routes,['/bridge/wake','/bridge/chat/status','/bridge/pair']);
});

test('disconnect clears only a stale local pair after authoritative empty status', async()=>{
  const connection={base:'https://ferro.sire.run/bridge',token:'shared-token',browserId:'browser-context-live-1234',tabId:42};
  let stored=true;
  const routes=[];
  const {context,listeners}=worker({fetch:async(url)=>{
    const path=new URL(url).pathname;
    routes.push(path);
    if(path.endsWith('/disconnect'))return {ok:false,status:409,text:async()=> 'wrong pairing'};
    return {ok:true,status:200,json:async()=>({busy:false,leased:false,paired_tab:null}),text:async()=>''};
  }});
  context.chrome.storage.session.get=async()=>({connection});
  context.chrome.storage.session.remove=async()=>{stored=false};
  context.stopPolling=()=>{};
  context.startPolling=()=>{};
  context.restoreSidePanelAccess=async()=>{};

  const response=await new Promise(resolve=>listeners[0]({type:'ferro-disconnect'},{id:'test-extension',url:'chrome-extension://test-extension/popup.html'},resolve));
  assert.equal(response.ok,true);
  assert.equal(stored,false);
  assert.deepEqual(routes,['/bridge/disconnect','/bridge/chat/status']);
});

test('disconnect preserves local state when another browser owns the pair', async()=>{
  const connection={base:'https://ferro.sire.run/bridge',token:'shared-token',browserId:'browser-context-stale-1234',tabId:42};
  let stored=true;
  const routes=[];
  const {context,listeners}=worker({fetch:async(url)=>{
    const path=new URL(url).pathname;
    routes.push(path);
    if(path.endsWith('/disconnect'))return {ok:false,status:409,text:async()=> 'wrong pairing'};
    return {ok:true,status:200,json:async()=>({busy:false,leased:false,paired_tab:'browser-context-other-1234.42'}),text:async()=>''};
  }});
  context.chrome.storage.session.get=async()=>({connection});
  context.chrome.storage.session.remove=async()=>{stored=false};
  context.stopPolling=()=>{};
  context.startPolling=()=>{};

  const response=await new Promise(resolve=>listeners[0]({type:'ferro-disconnect'},{id:'test-extension',url:'chrome-extension://test-extension/popup.html'},resolve));
  assert.match(response.error,/wrong pairing/);
  assert.equal(stored,true);
  assert.deepEqual(routes,['/bridge/disconnect','/bridge/chat/status']);
});
