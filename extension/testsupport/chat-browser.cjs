'use strict';
const readline=require('node:readline');
const path=require('node:path');
const fs=require('node:fs');
const assert=require('node:assert/strict');
const {launchChrome}=require('./cdp.cjs');
(async()=>{
 const lines=readline.createInterface({input:process.stdin});
 const input=lines[Symbol.asyncIterator]();
 const config=JSON.parse((await input.next()).value);
 const browser=await launchChrome({windowSize:[390,850],extraArgs:['--enable-unsafe-extension-debugging','--no-proxy-server']});
 for(const signal of ['SIGTERM','SIGINT'])process.once(signal,async()=>{await browser.close();process.exit(1)});
 try {
  const {id}=await browser.browserSend('Extensions.loadUnpacked',{path:path.resolve(__dirname,'..')});
  await browser.navigate(`chrome-extension://${id}/sidepanel.html`);
  async function until(expression,timeout=15000){const end=Date.now()+timeout;while(!await browser.evaluate(expression)){if(Date.now()>end)throw new Error('Timed out: '+expression+'; UI: '+await browser.evaluate('document.body.innerText'));await new Promise(r=>setTimeout(r,80));}}
  await until('!!session');
  // Pair from the actual trusted side-panel document, exactly as its Connect button does.
  await browser.evaluate(`(async()=>{
   const tab=await chrome.tabs.create({url:${JSON.stringify(config.url)},active:true});
   connection={base:${JSON.stringify(config.base)},token:${JSON.stringify(config.token)},tabId:tab.id};
   const reply=await chrome.runtime.sendMessage({type:'ferro-connect',connection});if(reply.error)throw new Error(reply.error);
   await refresh();
   document.getElementById('model-url').value=${JSON.stringify(config.modelURL)};
   document.getElementById('model').value='fixture';document.getElementById('api-key').value='fixture-key';
   document.getElementById('model-form').requestSubmit();
  })()`);
  await until(`document.getElementById('notice').textContent.includes('Model settings saved')`);
  await browser.evaluate(`document.getElementById('origins').value=${JSON.stringify(config.url)};document.getElementById('origins-form').requestSubmit()`);
  await until(`document.getElementById('notice').textContent.includes('Allowed websites saved')`);
  await browser.evaluate(`settings(false);document.getElementById('message').value='Fill and submit the fixture';document.getElementById('composer').requestSubmit()`);
  await until('!running && messages.some(m=>m.text.includes("cannot click, type"))');
  await browser.evaluate(`document.getElementById('interactions').checked=true;document.getElementById('message').value='Fill and submit the fixture';document.getElementById('composer').requestSubmit()`);
  await until('!running && messages.some(m=>m.role==="assistant" && m.text==="from sidepanel")');
  assert.equal(await browser.evaluate(`document.getElementById('api-key').value`),'');
  assert.equal(await browser.evaluate(`document.body.innerHTML.includes('fixture-key')`),false);
  const count=await browser.evaluate('messages.length');
  await browser.navigate(`chrome-extension://${id}/sidepanel.html`);
  await until(`messages.length===${count} && !!connection`);
  assert.equal(await browser.evaluate('running'),false);
  // Model-provided markup remains text, never executable HTML.
  await browser.evaluate(`bubble('assistant','<img src=x onerror="window.injected=true">')`);
  assert.equal(await browser.evaluate(`!!document.querySelector('#messages img')`),false);
  await browser.evaluate(`document.querySelector('#messages').lastElementChild.remove()`);
  if(config.screenshot){const {data}=await browser.send('Page.captureScreenshot',{format:'png'});fs.writeFileSync(config.screenshot,Buffer.from(data,'base64'));}
  await browser.evaluate(`document.getElementById('message').value='PROVIDER_ERROR';document.getElementById('composer').requestSubmit()`);
  await until('!running && messages.some(m=>m.text.includes("[redacted]"))');
  assert.equal(await browser.evaluate(`JSON.stringify(messages).includes('fixture-key')`),false);
  await browser.evaluate(`document.getElementById('message').value='WAIT_FOR_CANCEL';document.getElementById('composer').requestSubmit()`);
  await until(`(async()=>await (await fetch(${JSON.stringify(config.modelURL + '/waiting')})).json())()`);
  const foreign=await browser.evaluate(`(async()=>{
   const response=await fetch(connection.base+'/chat/cancel',{method:'POST',headers:{Authorization:'Bearer '+connection.token,'X-Ferro-Chat-Session':'other-chat-session-1234'},body:'{}'});
   return response.json();
  })()`);
  assert.equal(foreign.output.status,'tab_busy');
  await browser.evaluate(`document.getElementById('stop').click()`);
  await until('!running && document.getElementById("notice").textContent.includes("not retried")');
  await until(`(async()=>!(await (await fetch(${JSON.stringify(config.modelURL + '/waiting')})).json()))()`);
  process.stdout.write('chat verified\n');
 }finally{await browser.close();lines.close()}
})().catch(e=>{console.error(e.stack);process.exitCode=1});
