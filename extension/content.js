/* extension/content.js -- thin per-tab relay between background.js's
 * poll/reply loop and adapter.js's generic action executor (T12.2).
 *
 * Deliberately much thinner than ox/extension/content.js: ox's content
 * script owns the outer poll loop itself (it never navigates away from
 * oxalpha.com's single-page chat UI, so its JS context survives for the
 * whole session). ferro's action vocabulary includes real navigation
 * ("goto"), which destroys and reinjects this content script -- any state
 * held here would be lost on every navigate. So the persistent poll loop
 * and pairing state live in background.js (a service worker, which
 * survives page navigation) instead; this file's only job is to announce
 * itself as ready after each (re)injection and execute whatever single
 * action background.js hands it via chrome.tabs.sendMessage.
 */
(() => {
  // Pair-time attachment may overlap Chrome's normal document_idle injection.
  // Exactly one listener may execute each command in this isolated world.
  if (globalThis.__ferroContentInstalled) return;
  globalThis.__ferroContentInstalled = true;
  try {
    chrome.runtime.sendMessage({ type: 'ferro-content-ready' });
  } catch (_) {
    // Extension context can be mid-reload right after an update; the next
    // navigation's re-injection will send the ready ping successfully.
  }

  chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
    if (message?.type === 'ferro-ping') { sendResponse({ready:typeof globalThis.FerroAdapter?.perform === 'function'}); return false; }
    if (!message || message.type !== 'ferro-perform') return false;
    globalThis.FerroAdapter.perform(message.action)
      .then(sendResponse)
      .catch((error) => sendResponse({ error: error.message }));
    return true; // keep the message channel open for the async response
  });
})();
