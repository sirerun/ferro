'use strict';
/* extension/testsupport/cdp.cjs -- a minimal, zero-dependency Chrome
 * DevTools Protocol client used ONLY by extension/snapshot.test.cjs
 * (T12.2's Node-side parity test). Not part of the shipped extension.
 *
 * Why this exists instead of jsdom or a hand-mocked DOM (the two
 * alternatives docs/plan.md's T12.2 task text explicitly allows): the
 * function under test (adapter.js's takeSnapshot(), ported from
 * internal/core/snapshot.go) filters elements by real CSS layout --
 * getComputedStyle() and getBoundingClientRect() results for display:none,
 * visibility:hidden, opacity:0, zero-size and off-screen elements. jsdom
 * does not run a layout engine (getBoundingClientRect() is always 0x0),
 * which would make every element in this fixture look "invisible" and
 * silently pass a parity test that isn't testing anything. A hand-mocked
 * DOM (the pattern ox/extension/adapter.test.cjs uses, via node:vm) would
 * require reimplementing enough of TreeWalker + computed style + layout to
 * arguably BE a second, divergent copy of the algorithm's visibility
 * rules -- exactly the drift risk this parity test exists to catch. Real
 * headless Chrome, the same engine internal/core/snapshot_parity_test.go's
 * chromedp-driven Go test uses, is the only way to get identical geometry
 * semantics on both sides. Node 22 ships a stable global `fetch` and a
 * global `WebSocket` client, which is enough to speak CDP directly with no
 * npm dependency (verified against Node v22.23.2 during T12.2).
 */

const { spawn } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const CANDIDATE_CHROME_PATHS = [
  process.env.CHROME_PATH,
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  '/Applications/Chromium.app/Contents/MacOS/Chromium',
  '/usr/bin/google-chrome',
  '/usr/bin/google-chrome-stable',
  '/usr/bin/chromium',
  '/usr/bin/chromium-browser',
].filter(Boolean);

function findChrome() {
  for (const candidate of CANDIDATE_CHROME_PATHS) {
    if (fs.existsSync(candidate)) return candidate;
  }
  return null;
}

async function waitFor(predicate, timeoutMs, intervalMs) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (predicate()) return;
    if (Date.now() > deadline) throw new Error('timed out waiting for condition');
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
  }
}

// launchChrome starts a throwaway headless Chrome (a fresh temp
// --user-data-dir every run, matching how chromedp.DefaultExecAllocatorOptions
// behaves on the Go side -- see snapshot_parity_test.go's header comment)
// and returns a small client exposing navigate()/evaluate()/close().
// windowSize MUST match snapshot_parity_test.go's chromedp.WindowSize(...)
// call exactly -- see that file's comment on why.
async function launchChrome({ windowSize = [1280, 3000] } = {}) {
  const chromePath = findChrome();
  if (!chromePath) {
    throw new Error(
      'no Chrome binary found (set CHROME_PATH, or install Google Chrome/Chromium at a standard location)'
    );
  }

  const userDataDir = fs.mkdtempSync(path.join(os.tmpdir(), 'ferro-cdp-'));
  const proc = spawn(
    chromePath,
    [
      '--headless=new',
      '--disable-gpu',
      '--remote-debugging-port=0',
      `--window-size=${windowSize[0]},${windowSize[1]}`,
      `--user-data-dir=${userDataDir}`,
      'about:blank',
    ],
    { stdio: 'ignore' }
  );

  const portFile = path.join(userDataDir, 'DevToolsActivePort');
  try {
    await waitFor(() => fs.existsSync(portFile), 10000, 100);
  } catch (error) {
    proc.kill();
    throw new Error(`Chrome did not open its DevTools port in time: ${error.message}`);
  }
  const port = Number(fs.readFileSync(portFile, 'utf8').split('\n')[0]);

  const newTabRes = await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' });
  if (!newTabRes.ok) throw new Error(`CDP /json/new: HTTP ${newTabRes.status}`);
  const tab = await newTabRes.json();

  const ws = new WebSocket(tab.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true });
    ws.addEventListener('error', (event) => reject(new Error(`CDP websocket error: ${event.message || event}`)), {
      once: true,
    });
  });

  let nextId = 1;
  const pending = new Map();
  ws.addEventListener('message', (event) => {
    const msg = JSON.parse(event.data);
    if (msg.id !== undefined && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id);
      pending.delete(msg.id);
      if (msg.error) reject(new Error(`CDP ${msg.error.code}: ${msg.error.message}`));
      else resolve(msg.result);
    }
  });

  function send(method, params) {
    const id = nextId++;
    return new Promise((resolve, reject) => {
      pending.set(id, { resolve, reject });
      ws.send(JSON.stringify({ id, method, params }));
    });
  }

  async function navigate(url) {
    await send('Page.enable', {});
    const loaded = new Promise((resolve) => {
      const handler = (event) => {
        const msg = JSON.parse(event.data);
        if (msg.method === 'Page.loadEventFired') {
          ws.removeEventListener('message', handler);
          resolve();
        }
      };
      ws.addEventListener('message', handler);
    });
    await send('Page.navigate', { url });
    await loaded;
  }

  async function evaluate(expression) {
    const result = await send('Runtime.evaluate', {
      expression,
      returnByValue: true,
      awaitPromise: true,
    });
    if (result.exceptionDetails) {
      const detail = result.exceptionDetails;
      throw new Error(`page evaluate error: ${detail.text}${detail.exception ? ': ' + detail.exception.description : ''}`);
    }
    return result.result.value;
  }

  async function close() {
    try {
      ws.close();
    } catch (_) {
      /* ignore */
    }
    proc.kill();
    await new Promise((resolve) => proc.once('exit', resolve)).catch(() => {});
    try {
      fs.rmSync(userDataDir, { recursive: true, force: true });
    } catch (_) {
      /* ignore */
    }
  }

  return { navigate, evaluate, close };
}

module.exports = { launchChrome, findChrome };
