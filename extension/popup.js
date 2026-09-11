/* extension/popup.js -- single-tab pairing flow (T12.2, ADR 006 decision
 * #3), ported from ox/extension/popup.js: the extension only ever acts on
 * the one tab a human explicitly paired here. Generalized vs. ox in one
 * way -- there is no fixed target site (ox hardcodes oxalpha.com), so this
 * accepts any http(s) tab, and the bridge base URL is a field here rather
 * than a fixed local port, since internal/extbridge's actual bind address
 * is configured on the Go side (T12.1) and simply typed in here.
 */
const status = document.getElementById('status');

document.getElementById('connect').onclick = async () => {
  try {
    const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
    if (!tab || !/^https?:\/\//.test(tab.url || '')) {
      throw new Error('Open the http(s) tab you want ferro to drive first.');
    }
    const base = document.getElementById('base').value.trim().replace(/\/+$/, '');
    const token = document.getElementById('token').value.trim();
    if (!/^https?:\/\/127\.0\.0\.1(:\d+)?$|^https?:\/\/localhost(:\d+)?$/.test(base)) {
      throw new Error('Bridge URL must be a loopback address (http://127.0.0.1:<port>).');
    }
    if (!token) throw new Error('Enter the pairing token ferro-mcp printed on startup.');

    const response = await chrome.runtime.sendMessage({
      type: 'ferro-connect',
      connection: { base, token, tabId: tab.id },
    });
    if (!response || response.error) throw new Error(response?.error || 'connect failed');
    status.textContent = `Connected to tab "${tab.title || tab.url}". Keep it open; a CAPTCHA or login page pauses the runner until you clear it.`;
  } catch (error) {
    status.textContent = error.message;
  }
};

document.getElementById('disconnect').onclick = async () => {
  await chrome.runtime.sendMessage({ type: 'ferro-disconnect' });
  status.textContent = 'Disconnected.';
};
