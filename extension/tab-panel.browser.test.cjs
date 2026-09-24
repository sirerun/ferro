'use strict';
const test=require('node:test');
const assert=require('node:assert/strict');
const http=require('node:http');
const path=require('node:path');
const {launchChrome}=require('./testsupport/cdp.cjs');

test('real Chrome pairs one tab, confirms handoff, reuses token and restores scoped panels', {skip:process.env.FERRO_TEST_BROWSER!=='1',timeout:30000}, async()=>{
 let paired='';
 const server=http.createServer((req,res)=>{
  if(req.url==='/fixture'){res.end('<!doctype html><title>Ferro pairing fixture</title><p>Read only fixture</p>');return;}
  if(req.headers.authorization!=='Bearer fixture-token'){res.writeHead(401).end();return;}
  if(req.url==='/chat/status'){res.setHeader('Content-Type','application/json');res.end(JSON.stringify({paired_tab:paired,busy:false,leased:false}));return;}
  if(req.url==='/pair'){paired=req.headers['x-ferro-tab-id'];res.writeHead(204).end();return;}
  if(req.url==='/disconnect'){assert.equal(req.headers['x-ferro-tab-id'],paired);paired='';res.writeHead(204).end();return;}
  if(req.url.startsWith('/next')){setTimeout(()=>res.writeHead(204).end(),100);return;}
  res.writeHead(404).end();
 });
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
 const base=`http://127.0.0.1:${server.address().port}`;
 let browser;
 try{
  browser=await launchChrome({extraArgs:['--enable-unsafe-extension-debugging']});
  const {id}=await browser.browserSend('Extensions.loadUnpacked',{path:path.resolve(__dirname)});
  await browser.navigate(`chrome-extension://${id}/popup.html`);
  const tabs=await browser.evaluate(`(async()=>{const a=await chrome.tabs.create({url:${JSON.stringify(base+'/fixture')},active:false});const b=await chrome.tabs.create({url:${JSON.stringify(base+'/fixture')},active:false});return [a.id,b.id];})()`);
  const deadline=Date.now()+10000;
  while(!await browser.evaluate(`(async()=>{const tabs=await Promise.all(${JSON.stringify(tabs)}.map(id=>chrome.tabs.get(id)));return tabs.every(t=>t.status==='complete' && t.url.startsWith(${JSON.stringify(base)}));})()`)) {
    if(Date.now()>deadline)throw new Error('Fixture tabs did not finish loading');
    await new Promise(resolve=>setTimeout(resolve,30));
  }
  const send=msg=>browser.evaluate(`chrome.runtime.sendMessage(${JSON.stringify(msg)})`);
  const options=()=>browser.evaluate(`(async()=>({global:await chrome.sidePanel.getOptions({}),tabs:await Promise.all(${JSON.stringify(tabs)}.map(tabId=>chrome.sidePanel.getOptions({tabId}))),popups:await Promise.all(${JSON.stringify(tabs)}.map(tabId=>chrome.action.getPopup({tabId})))}))()`);
  let view=await options();assert.equal(view.global.enabled,false);assert.ok(view.tabs.every(x=>!x.enabled));
  let reply=await send({type:'ferro-connect',connection:{base,token:'fixture-token',tabId:tabs[0]}});assert.equal(reply.ok,true,JSON.stringify(reply));
  view=await options();assert.deepEqual(view.tabs.map(x=>x.enabled),[true,false]);assert.deepEqual(view.popups,['',`chrome-extension://${id}/popup.html`]);
  reply=await send({type:'ferro-connect',connection:{base,tabId:tabs[1]}});assert.equal(reply.requiresConfirmation,true,JSON.stringify(reply));assert.equal(paired,String(tabs[0]));
  reply=await send({type:'ferro-connect',connection:{base,tabId:tabs[1]},confirmDisconnectTab:reply.pairedTab});assert.equal(reply.ok,true,JSON.stringify(reply));
  view=await options();assert.deepEqual(view.tabs.map(x=>x.enabled),[false,true]);assert.deepEqual(view.popups,[`chrome-extension://${id}/popup.html`,'']);
  // Native open from an extension page user gesture, without an async detour.
  const open=await browser.send('Runtime.evaluate',{expression:`chrome.sidePanel.open({tabId:${tabs[1]}})`,userGesture:true,awaitPromise:true});assert.equal(open.exceptionDetails,undefined);
  // Deliberately introduce an old all-tabs override and execute the same
  // reconciliation routine run after worker startup.
  await browser.evaluate(`chrome.sidePanel.setOptions({tabId:${tabs[0]},path:'sidepanel.html',enabled:true})`);
  const targets=await browser.browserSend('Target.getTargets');const worker=targets.targetInfos.find(t=>t.url===`chrome-extension://${id}/background.js`);
  const {sessionId}=await browser.browserSend('Target.attachToTarget',{targetId:worker.targetId,flatten:true});
  const restored=await browser.browserSend('Runtime.evaluate',{expression:'restoreSidePanelAccess()',awaitPromise:true},sessionId);assert.equal(restored.exceptionDetails,undefined);
  view=await options();assert.deepEqual(view.tabs.map(x=>x.enabled),[false,true]);
  await browser.send('ServiceWorker.enable');
  await browser.send('ServiceWorker.stopAllWorkers');
  await send({type:'ferro-status'});
  view=await options();assert.deepEqual(view.tabs.map(x=>x.enabled),[false,true]);
  reply=await send({type:'ferro-disconnect'});assert.equal(reply.ok,true);view=await options();assert.ok(view.tabs.every(x=>!x.enabled));assert.deepEqual(view.popups,[`chrome-extension://${id}/popup.html`,`chrome-extension://${id}/popup.html`]);
  const status=await send({type:'ferro-status'});assert.equal(status.credentialsAvailable,true);assert.equal(JSON.stringify(status).includes('fixture-token'),false);
  assert.equal(await browser.evaluate(`(async()=>{await chrome.tabs.get(${tabs[0]});await chrome.tabs.get(${tabs[1]});return true;})()`),true);
 } finally {if(browser)await browser.close();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}
});
