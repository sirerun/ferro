'use strict';
// Browser-gated Go integration-test helper. Credentials arrive only on stdin.
const readline = require('node:readline');
const path = require('node:path');
const { launchChrome } = require('./cdp.cjs');
(async () => {
  const lines = readline.createInterface({ input: process.stdin });
  const input = lines[Symbol.asyncIterator]();
  const { value } = await input.next();
  const config = JSON.parse(value);
  const extension = path.resolve(__dirname, '..');
  const chrome = await launchChrome({extraArgs:["--enable-unsafe-extension-debugging", "--no-proxy-server"]});
  for (const signal of ['SIGTERM','SIGINT']) process.once(signal, async () => { await chrome.close(); process.exit(1); });
  try {
    const { id } = await chrome.browserSend('Extensions.loadUnpacked', {path:extension});
    console.error('extension test: loading popup');
    await chrome.navigate(`chrome-extension://${id}/popup.html`);
    const readyDeadline = Date.now() + 5000;
    while (!(await chrome.evaluate(`!!(globalThis.chrome?.tabs && globalThis.chrome?.runtime?.id)`))) {
      if (Date.now() > readyDeadline) throw new Error('extension popup APIs unavailable: ' + JSON.stringify(await chrome.evaluate('({url:location.href,title:document.title,body:document.body.innerText.slice(0,300)})')));
      await new Promise(r => setTimeout(r, 50));
    }
    console.error('extension test: pairing fixture tab');
    const paired = await chrome.evaluate(`(async () => {
      const tab = await chrome.tabs.create({url:${JSON.stringify(config.url)}, active:true});
      const reply = await chrome.runtime.sendMessage({type:'ferro-connect',connection:{base:${JSON.stringify(config.base)},token:${JSON.stringify(config.token)},tabId:tab.id}});
      if (reply.error) throw new Error(reply.error);
      return true;
    })()`);
    if (!paired) throw new Error('pair failed');
    process.stdout.write('paired\n');
    await input.next(); // Go closes stdin after completing its assertions.
  } finally { await chrome.close(); lines.close(); }
})().catch(error => { console.error(error.message); process.exitCode=1; });
