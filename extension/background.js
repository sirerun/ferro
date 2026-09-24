/* extension/background.js -- service worker: pairing state, the bridge
 * poll/reply loop (ADR 006 decision #2), page navigation, and
 * chrome.debugger-issued trusted click/key input (T12.2).
 *
 * Split vs. ~/Code/dndungu/ox/extension/background.js:
 *   - ox's background.js is purely reactive: content.js owns the poll
 *     loop and background.js only answers "poll"/"reply"/"click" messages
 *     it relays to. ferro's action vocabulary includes "goto" (real page
 *     navigation), which destroys and reinjects the content script -- a
 *     poll loop living there would die mid-flight on every navigate. So
 *     this file owns the poll loop itself (a service worker survives page
 *     navigation) and content.js is reduced to a thin per-page relay (see
 *     its header comment). "goto" is handled entirely here, via
 *     chrome.tabs.update + chrome.tabs.onUpdated, never reaching adapter.js.
 *   - The chrome.debugger trusted-click technique, including the
 *     retry-on-dropped-debugger logic, is ported verbatim from ox's
 *     background.js (see dispatchTrustedInput below) and generalized to
 *     also cover trusted key dispatch (ferro's core.Action has a "key"
 *     kind ox's fixed single-site vocabulary never needed).
 *
 * Bridge wire protocol (internal/extbridge, T12.1 -- built in parallel;
 * this is this task's own best-effort encoding of ADR 006 decision #2's
 * literal envelope, `{"id","action"}` for GET /next and
 * `{"id","snapshot","result","error"}` for POST /reply; if T12.1 lands
 * with a different shape, that's a follow-up integration fix, not solved
 * here):
 *   GET  {base}/next   -> 200 {"id": "...", "action": {"op": ..., ...}}
 *                          or 204 (nothing queued)
 *   POST {base}/reply  <- {"id": "...", "result"?: ..., "snapshot"?: ...,
 *                          "blocked"?: "...", "error"?: "..."}
 * action.op is one of: navigate, click, fill, select, key, scroll,
 * wait_visible, extract, snapshot -- see adapter.js's perform() for the
 * per-op payload shape (selector/text/value/to/fields/maxElements/budgetMs).
 */

const POLL_TIMEOUT_MS = 30000; // one long-poll GET at a time
const POLL_ERROR_BACKOFF_MS = 2000; // bridge unreachable (laptop asleep, etc.)
const NAV_TIMEOUT_MS = 20000;
const CONTENT_READY_TIMEOUT_MS = 8000;
const NAV_SETTLE_MS = 250; // mirrors WaitStrategy's default SettleDebounce

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function getConnection() {
  const { connection } = await chrome.storage.session.get('connection');
  return connection || null;
}

// ---------------------------------------------------------------------
// Trusted click/key via chrome.debugger, ported from ox's background.js.
// The retry structure (3 attempts, "already attached"/"another debugger"
// classification, inputStarted distinguishing an uncertain outcome from a
// safe-to-retry one, 400*(attempt+1) backoff) is verbatim ox logic;
// dispatchTrustedInput is generalized to carry either a mouse click or a
// key-event sequence through that same wrapper, since ferro's action
// vocabulary needs both and ox only ever needed the former.
// ---------------------------------------------------------------------

const NAMED_KEYS = {
  Enter: { key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13, text: '\r' },
  Tab: { key: 'Tab', code: 'Tab', windowsVirtualKeyCode: 9 },
  Escape: { key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 },
  Backspace: { key: 'Backspace', code: 'Backspace', windowsVirtualKeyCode: 8 },
  ArrowUp: { key: 'ArrowUp', code: 'ArrowUp', windowsVirtualKeyCode: 38 },
  ArrowDown: { key: 'ArrowDown', code: 'ArrowDown', windowsVirtualKeyCode: 40 },
  ArrowLeft: { key: 'ArrowLeft', code: 'ArrowLeft', windowsVirtualKeyCode: 37 },
  ArrowRight: { key: 'ArrowRight', code: 'ArrowRight', windowsVirtualKeyCode: 39 },
};

// dispatchTrustedKeys sends one CDP Input.dispatchKeyEvent triple
// (keyDown/char/keyUp) per character, or a single named-key triple for a
// handful of common control keys. This is a real, working implementation
// for ASCII text plus the common named keys above -- not an exhaustive
// keycode table for every possible key combination; see the PR
// description for that documented limitation.
async function dispatchTrustedKeys(target, text) {
  const named = NAMED_KEYS[text];
  const sequence = named
    ? [named]
    : Array.from(text).map((ch) => ({ key: ch, code: '', text: ch, windowsVirtualKeyCode: ch.charCodeAt(0) }));
  for (const ch of sequence) {
    await chrome.debugger.sendCommand(target, 'Input.dispatchKeyEvent', {
      type: 'keyDown',
      key: ch.key,
      code: ch.code,
      windowsVirtualKeyCode: ch.windowsVirtualKeyCode,
    });
    if (ch.text) {
      await chrome.debugger.sendCommand(target, 'Input.dispatchKeyEvent', {
        type: 'char',
        key: ch.key,
        text: ch.text,
      });
    }
    await chrome.debugger.sendCommand(target, 'Input.dispatchKeyEvent', {
      type: 'keyUp',
      key: ch.key,
      code: ch.code,
      windowsVirtualKeyCode: ch.windowsVirtualKeyCode,
    });
  }
}

async function dispatchTrustedInput(tabId, kind, payload) {
  const target = { tabId };
  let lastError;
  for (let attempt = 0; attempt < 3; attempt++) {
    try {
      await chrome.debugger.attach(target, '1.3');
    } catch (error) {
      if (!/already attached/i.test(error.message)) throw error;
      if (/another debugger/i.test(error.message)) {
        throw new Error('DevTools or another extension is debugging this tab; close it and resume');
      }
    }
    let inputStarted = false;
    try {
      if (!activeAction || payload.commandID !== activeAction.commandID || Date.now() >= activeAction.deadlineMs) throw new Error('action expired before input');
      if (kind === 'click') {
        const { x, y } = payload;
        if (!Number.isFinite(x) || !Number.isFinite(y) || x < 0 || y < 0) {
          throw new Error('invalid click coordinates');
        }
        await chrome.debugger.sendCommand(target, 'Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
        inputStarted = true;
        await chrome.debugger.sendCommand(target, 'Input.dispatchMouseEvent', {
          type: 'mousePressed', button: 'left', clickCount: 1, x, y,
        });
        await chrome.debugger.sendCommand(target, 'Input.dispatchMouseEvent', {
          type: 'mouseReleased', button: 'left', clickCount: 1, x, y,
        });
      } else if (kind === 'key') {
        inputStarted = true;
        await dispatchTrustedKeys(target, payload.text || '');
      } else {
        throw new Error(`unknown trusted-input kind ${JSON.stringify(kind)}`);
      }
      lastError = null;
    } catch (error) {
      lastError = error;
    } finally {
      try {
        await chrome.debugger.detach(target);
      } catch (_) {
        /* already detached */
      }
    }
    if (!lastError) return {};
    if (inputStarted) {
      // Chrome dropped the debugger *after* input began: we cannot tell
      // whether the click/key landed. Do not retry into a possible
      // duplicate action -- surface it and let the caller inspect the page.
      throw new Error(`${kind} outcome is uncertain: ${lastError.message}. Inspect the page before resuming`);
    }
    if (!/not attached|detached/i.test(lastError.message)) throw lastError;
    await sleep(400 * (attempt + 1));
  }
  throw new Error(
    `Trusted ${kind} failed after retries: ${lastError.message}. Keep the tab focused and do not dismiss Chrome's debugging bar; then resume`
  );
}

// ---------------------------------------------------------------------
// Content-script readiness tracking (needed because "goto" reinjects it).
// ---------------------------------------------------------------------
const readyTabs = new Set();

chrome.tabs.onRemoved.addListener((tabId) => readyTabs.delete(tabId));

async function waitForContentReady(tabId, timeoutMs) {
  try { if ((await chrome.tabs.sendMessage(tabId, { type:'ferro-ping' }))?.ready) return; } catch (_) {}
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try { if ((await chrome.tabs.sendMessage(tabId, { type:'ferro-ping' }))?.ready) return; } catch (_) {}
    await sleep(150);
  }
  throw new Error('content script did not become ready after navigation');
}

// Static content scripts do not attach to documents opened before installation.
// Prepare only the requested tab, before dispatching any action. Coalesce
// concurrent preparation and never retry a possibly delivered browser input.
const preparingTabs = new Map();
async function ensureContentReady(tabId) {
  if (preparingTabs.has(tabId)) return preparingTabs.get(tabId);
  const prepare = (async () => {
    const tab = await chrome.tabs.get(tabId);
    if (!/^https?:\/\//.test(tab.url || '')) throw new Error('Choose a normal website tab; Chrome internal pages cannot be controlled.');
    try {
      if ((await chrome.tabs.sendMessage(tabId, {type:'ferro-ping'}, {frameId:0}))?.ready) return;
    } catch (_) { /* an existing document may not have a receiver yet */ }
    try {
      await chrome.scripting.executeScript({target:{tabId, frameIds:[0]}, files:['adapter.js','content.js']});
    } catch (_) {
      throw new Error('Ferro cannot attach to this page. Click the Ferro toolbar icon on the website tab and reconnect, or refresh that tab. Chrome internal pages, the Web Store and built-in PDF pages are not supported.');
    }
    if (!(await chrome.tabs.sendMessage(tabId, {type:'ferro-ping'}, {frameId:0}))?.ready) {
      throw new Error('The page is not ready for Ferro. Refresh the website tab and reconnect.');
    }
  })().catch(error => { error.code = 'page_unavailable'; throw error; });
  preparingTabs.set(tabId, prepare);
  try { await prepare; } finally { preparingTabs.delete(tabId); }
}

async function sendToContent(tabId, message) {
  await ensureContentReady(tabId);
  // A failure after this send may mean input happened and the response was
  // lost. Surface uncertainty instead of blindly sending the action again.
  return chrome.tabs.sendMessage(tabId, message, {frameId:0});
}

// ---------------------------------------------------------------------
// Navigation -- owned here, not by adapter.js/content.js (see file header).
// ---------------------------------------------------------------------
function waitForTabComplete(tabId, timeoutMs) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      chrome.tabs.onUpdated.removeListener(listener);
      reject(new Error('navigation did not complete in time'));
    }, timeoutMs);
    function listener(updatedTabId, info) {
      if (updatedTabId === tabId && info.status === 'complete') {
        clearTimeout(timer);
        chrome.tabs.onUpdated.removeListener(listener);
        resolve();
      }
    }
    chrome.tabs.onUpdated.addListener(listener);
  });
}

async function performNavigate(tabId, url) {
  readyTabs.delete(tabId);
  const complete = waitForTabComplete(tabId, NAV_TIMEOUT_MS);
  await chrome.tabs.update(tabId, { url });
  await complete;
  await waitForContentReady(tabId, CONTENT_READY_TIMEOUT_MS);
  await sleep(NAV_SETTLE_MS);
  return {};
}

async function handleAction(tabId, action) {
  if (!action || Date.now() >= action.deadlineMs) throw new Error('action expired before execution');
  if (action.op === 'location') {
    const tab = await chrome.tabs.get(tabId);
    return { result: tab.url };
  }
  const expected = action.op === 'navigate' ? new URL(action.url).origin : new URL((await chrome.tabs.get(tabId)).url).origin;
  if (!action.origin || expected !== action.origin) return { code: 'origin_changed', error: 'Tab origin changed; take a fresh snapshot.' };
  if (action.op === 'navigate') return performNavigate(tabId, action.url);
  return sendToContent(tabId, { type: 'ferro-perform', action });
}

// ---------------------------------------------------------------------
// Bridge poll/reply loop (ADR 006 decision #2).
// ---------------------------------------------------------------------
async function fetchNext(base, token, tabId) {
  pendingPoll = new AbortController();
  const res = await fetch(`${base}/next`, {
    headers: { Authorization: `Bearer ${token}`, 'X-Ferro-Tab-Id': String(tabId) },
    signal: AbortSignal.any([pendingPoll.signal, AbortSignal.timeout(POLL_TIMEOUT_MS)]),
  });
  if (res.status === 204) return null;
  if (!res.ok) throw new Error(`bridge GET /next: HTTP ${res.status}`);
  return res.json();
}

async function postReply(base, token, reply) {
  const res = await fetch(`${base}/reply`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    body: JSON.stringify(reply),
    signal: AbortSignal.timeout(10000),
  });
  if (!res.ok) throw new Error(`bridge POST /reply: HTTP ${res.status}`);
}

let loopGeneration = 0;
let activeAction = null;
let pendingPoll = null;
let lastPollSuccessAt = 0;
let lastPollError = "";

async function pollLoop(myGeneration) {
  while (loopGeneration === myGeneration) {
    const connection = await getConnection();
    if (!connection) {
      await sleep(1000);
      continue;
    }
    try {
      const next = await fetchNext(connection.base, connection.token, connection.tabId);
      if (loopGeneration !== myGeneration) return; // paired out from under us
      lastPollSuccessAt = Date.now();
      lastPollError = "";
      if (!next) continue;
      let reply;
      try {
        activeAction = {...next.action, commandID:next.id};
        const result = await handleAction(connection.tabId, activeAction);
        reply = { id: next.id, ...result };
      } catch (error) {
        reply = { id: next.id, code: error.code || 'outcome_uncertain', error: error.message };
      }
      try {
        await postReply(connection.base, connection.token, reply);
      } finally {
        activeAction = null;
      }
    } catch (error) {
      if (loopGeneration !== myGeneration) return;
      lastPollError = error.message || String(error);
      // Transport failure (bridge not running, laptop asleep, network
      // hiccup) -- distinct from a "blocked" page: nothing gets POSTed
      // here because there is no request id to reply to. Back off and
      // keep trying; T12.7 verifies the caller-visible signal for this.
      await sleep(POLL_ERROR_BACKOFF_MS);
    }
  }
}

function startPolling() {
  pendingPoll?.abort();
  loopGeneration++;
  pollLoop(loopGeneration);
}

function stopPolling() {
  pendingPoll?.abort();
  loopGeneration++; // orphans any in-flight pollLoop invocation
  lastPollSuccessAt = 0;
  lastPollError = "";
}

// ---------------------------------------------------------------------
// Message handlers: popup pairing, content-script readiness, trusted input.
// ---------------------------------------------------------------------
chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (!message) return false;

  if (message.type === 'ferro-connect') {
    (async () => {
      let previous = null;
      let restartPreviousPoll = false;
      try {
        if (activeAction) throw new Error('A browser action is still running. Stop it before reconnecting.');
        // Pairing may only be requested by our own popup or panel, never page content.
        if (sender.id !== chrome.runtime.id || ![chrome.runtime.getURL('popup.html'), chrome.runtime.getURL('sidepanel.html')].includes(sender.url)) throw new Error('pair using the extension popup or side panel');
        const c = message.connection;
        if (!/^http:\/\/(127\.0\.0\.1|localhost):[0-9]+$/.test(c.base)) throw new Error('use a local bridge URL');
        if (!Number.isInteger(c.tabId) || c.tabId < 0) throw new Error('Choose a website tab to connect.');
        await ensureContentReady(c.tabId);
        previous = await getConnection();
        const hadLocalConnection = !!previous;
        if (!previous) {
          const statusResponse = await fetch(`${c.base}/chat/status`, {
            method:'POST', headers:{Authorization:`Bearer ${c.token}`, 'Content-Type':'application/json', 'X-Ferro-Chat-Session':'extension-pairing-recovery'},
            body:'{}', signal:AbortSignal.timeout(5000),
          });
          if (!statusResponse.ok) throw new Error('Could not check the existing pairing. Retry when the service is available.');
          const state = await statusResponse.json();
          if (state.busy !== false || state.leased !== false) throw new Error('A task or agent lease is still running. Stop it before connecting another tab.');
          if (state.paired_tab) {
            if (!/^[0-9]+$/.test(state.paired_tab)) throw new Error('Invalid pairing status from the service.');
            previous = {base:c.base, token:c.token, tabId:Number(state.paired_tab)};
          }
        }
        if (previous && previous.base === c.base && previous.token === c.token && previous.tabId === c.tabId) {
          if (!hadLocalConnection) {
            await chrome.storage.session.set({connection:c});
            startPolling();
          }
          sendResponse({ ok: true });
          return;
        }
        if (previous && message.confirmDisconnectTab !== String(previous.tabId)) {
          sendResponse({requiresConfirmation:true, pairedTab:String(previous.tabId)});
          return;
        }
        if (previous) {
          if (activeAction) throw new Error('A browser action is still running. Stop it before reconnecting.');
          stopPolling();
          restartPreviousPoll = true;
        }
        if (previous && previous.tabId !== c.tabId) {
          const disconnected = await fetch(`${previous.base}/disconnect`, {
            method:'POST',
            headers:{Authorization:`Bearer ${previous.token}`,'X-Ferro-Tab-Id':String(previous.tabId)},
            signal:AbortSignal.timeout(5000),
          });
          if (!disconnected.ok) {
            throw new Error(`Could not release the previous tab: HTTP ${disconnected.status}`);
          }
        }
        const response = await fetch(`${c.base}/pair`, { method: 'POST', headers: { Authorization: `Bearer ${c.token}`, 'X-Ferro-Tab-Id': String(c.tabId) }, signal: AbortSignal.timeout(5000) });
        if (!response.ok) {
          const detail = (await response.text()).trim();
          const retry = response.status === 409 ? ' Retry after the previous tab’s action or poll has stopped.' : '';
          throw new Error(`Pairing failed: ${detail || `HTTP ${response.status}`}.${retry}`);
        }
        await chrome.storage.session.set({ connection: c });
        restartPreviousPoll = false;
        lastPollSuccessAt = Date.now();
        lastPollError = "";
        startPolling();
        sendResponse({ ok: true });
      } catch (error) {
        let message = error.message;
        if (restartPreviousPoll) {
          try {
            const restored = await fetch(`${previous.base}/pair`, {
              method: 'POST',
              headers: { Authorization: `Bearer ${previous.token}`, 'X-Ferro-Tab-Id': String(previous.tabId) },
              signal: AbortSignal.timeout(5000),
            });
            if (!restored.ok) message += ` The previous tab could not be restored (HTTP ${restored.status}).`;
          } catch (restoreError) {
            message += ` The previous tab could not be restored: ${restoreError.message}.`;
          }
          startPolling();
        }
        sendResponse({ error: message });
      }
    })();
    return true;
  }

  if (message.type === 'ferro-status') {
    (async () => {
      if (sender.id !== chrome.runtime.id || sender.url !== chrome.runtime.getURL('popup.html')) { sendResponse({error:'read status using the popup'}); return; }
      const c = await getConnection();
      sendResponse(c ? {
        configured: true,
        connected: !lastPollError && Date.now() - lastPollSuccessAt < 45000,
        base: c.base,
        tabId: c.tabId,
        transportError: lastPollError,
      } : { configured: false, connected: false });
    })();
    return true;
  }

  if (message.type === 'ferro-disconnect') {
    (async () => {
      if (sender.id !== chrome.runtime.id || ![chrome.runtime.getURL('popup.html'), chrome.runtime.getURL('sidepanel.html')].includes(sender.url)) { sendResponse({error:'disconnect using the popup'}); return; }
      if (activeAction) { sendResponse({error:'A browser action is still running. Wait for it to finish before disconnecting.'}); return; }
      const c = await getConnection();
      stopPolling();
      let warning = '';
      try {
        if (c) {
          const response = await fetch(`${c.base}/disconnect`, { method:'POST', headers:{Authorization:`Bearer ${c.token}`, 'X-Ferro-Tab-Id':String(c.tabId)}, signal:AbortSignal.timeout(5000) });
          if (!response.ok) {
            const detail = (await response.text()).trim();
            if (response.status === 409) {
              startPolling();
              sendResponse({error:`Disconnect failed: ${detail || `HTTP ${response.status}`}`});
              return;
            }
            warning = `The bridge did not confirm disconnect (HTTP ${response.status}).`;
          }
        }
      } catch (error) {
        warning = `The bridge could not confirm disconnect: ${error.message}`;
      }
      await chrome.storage.session.remove('connection');
      sendResponse({ ok:true, warning });
    })();
    return true;
  }

  if (message.type === 'ferro-content-ready') {
    if (sender.tab) readyTabs.add(sender.tab.id);
    return false;
  }

  if (message.type === 'ferro-trusted-input') {
    if (!sender.tab) return false;
    (async () => {
      try {
        const connection = await getConnection();
        if (!connection || connection.tabId !== sender.tab.id || !activeAction || message.commandID !== activeAction.commandID || Date.now() >= activeAction.deadlineMs || new URL(sender.url).origin !== activeAction.origin || new URL((await chrome.tabs.get(sender.tab.id)).url).origin !== activeAction.origin) {
          sendResponse({ error: 'this tab is not the paired tab' });
          return;
        }
        const result = await dispatchTrustedInput(sender.tab.id, message.kind, message);
        sendResponse(result);
      } catch (error) {
        sendResponse({ error: error.message });
      }
    })();
    return true;
  }

  return false;
});

// Resume polling if the service worker restarts (MV3 idle eviction, Chrome
// restart) while a pairing is still active in session storage.
chrome.storage.session.get('connection').then(({ connection }) => {
  if (connection) startPolling();
});

// Panel visibility is independent of browser-task pairing. Older local builds
// disabled the panel per tab during handoff; repair those overrides on startup.
async function restoreSidePanelAccess() {
  if (!chrome.sidePanel) return;
  await chrome.sidePanel.setOptions({path:'sidepanel.html', enabled:true});
  await chrome.sidePanel.setPanelBehavior({openPanelOnActionClick:true});
  const tabs = await chrome.tabs.query({});
  await Promise.all(tabs.filter(tab => Number.isInteger(tab.id)).map(async tab => {
    try {
      await chrome.sidePanel.setOptions({tabId:tab.id, path:'sidepanel.html', enabled:true});
    } catch (error) {
      // A tab may close between the query and this call.
      console.warn('Could not restore Ferro panel for tab', tab.id, error);
    }
  }));
}
void restoreSidePanelAccess().catch(error => console.error('Could not initialize Ferro side panel', error));
