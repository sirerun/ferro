/* extension/popup.js -- single-tab pairing flow (T12.2, ADR 006 decision
 * #3), ported from ox/extension/popup.js: the extension only ever acts on
 * the one tab a human explicitly paired here. Generalized vs. ox in one
 * way -- there is no fixed target site (ox hardcodes oxalpha.com), so this
 * accepts any http(s) tab, and the bridge base URL is a field here rather
 * than a fixed local port, since internal/extbridge's actual bind address
 * is configured on the Go side (T12.1) and simply typed in here.
 */
const status = document.getElementById('status');
const toggle = document.getElementById('connection-toggle');
const baseInput = document.getElementById('base');
const tokenInput = document.getElementById('token');
let connection = null;
let actionError = '';
let pending = false;
let statusRequest = 0;

async function refreshStatus() {
  const request = ++statusRequest;
  try {
    const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
    const response = await chrome.runtime.sendMessage({ type: 'ferro-status' });
    if (request !== statusRequest) return;
    if (!response || response.error) throw new Error(response?.error || 'Could not read Ferro status.');
    connection = response;
    if (response.configured) {
      baseInput.value = response.base;
      baseInput.disabled = true;
      tokenInput.disabled = true;
      toggle.textContent = 'Disconnect';
      const tabLabel = tab?.id === response.tabId ? 'this tab' : 'another tab';
      status.textContent = response.connected
        ? `Connected to ${tabLabel} through ${response.base}.`
        : `Trying to reconnect to ${tabLabel} through ${response.base}. ${response.transportError || 'The bridge has not answered recently.'} Disconnect to change the bridge URL.`;
    } else {
      baseInput.disabled = false;
      tokenInput.disabled = false;
      toggle.textContent = 'Connect this tab';
      status.textContent = 'Disconnected. Open the tab you want Ferro to use, then connect it.';
    }
  } catch (error) {
    if (request !== statusRequest) return;
    status.textContent = error.message || String(error);
    toggle.textContent = 'Connection unavailable';
    toggle.disabled = true;
    return;
  }
  if (actionError) status.textContent = actionError + " " + status.textContent;
  toggle.disabled = pending;
}

toggle.onclick = async () => {
  if (pending) return;
  pending = true;
  toggle.disabled = true;
  actionError = '';
  try {
    if (connection?.configured) {
      const response = await chrome.runtime.sendMessage({ type: 'ferro-disconnect' });
      if (!response || response.error) throw new Error(response?.error || 'Disconnect failed.');
      tokenInput.value = '';
      if (response.warning) actionError = `Disconnected locally. ${response.warning}`;
    } else {
      const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
      if (!tab || !/^https?:\/\//.test(tab.url || '')) {
        throw new Error('Open the http(s) tab you want Ferro to drive first.');
      }
      const base = baseInput.value.trim().replace(/\/+$/, '');
      const token = tokenInput.value.trim();
      if (!/^http:\/\/(127\.0\.0\.1|localhost):\d+$/.test(base)) {
        throw new Error('Bridge URL must be http://127.0.0.1:<port>. Check ferro-mcp status for the port.');
      }
      if (!token) throw new Error('Enter the pairing token from the local bridge-token file.');
      const response = await chrome.runtime.sendMessage({
        type: 'ferro-connect', connection: { base, token, tabId: tab.id },
      });
      if (!response || response.error) throw new Error(response?.error || 'Connect failed.');
    }
  } catch (error) {
    actionError = error.message || String(error);
  } finally {
    pending = false;
    await refreshStatus();
  }
};

refreshStatus();
setInterval(refreshStatus, 2000);
