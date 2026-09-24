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
const openChat = document.getElementById('open-chat');
let activeTab = null;
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
    activeTab = tab;
    openChat.hidden = !response.configured || tab?.id !== response.tabId;
    if (response.configured) {
      baseInput.value = response.base;
      baseInput.disabled = true;
      tokenInput.disabled = true;
      toggle.textContent = tab?.id === response.tabId ? 'Disconnect' : 'Connect this tab';
      const tabLabel = tab?.id === response.tabId ? 'this tab' : 'another tab';
      status.textContent = response.connected
        ? `Connected to ${tabLabel} through ${response.base}.`
        : `Trying to reconnect to ${tabLabel} through ${response.base}. ${response.transportError || 'The bridge has not answered recently.'} Disconnect to change the bridge URL.`;
    } else {
      if (response.base) baseInput.value = response.base;
      baseInput.disabled = !!response.credentialsAvailable;
      tokenInput.disabled = !!response.credentialsAvailable;
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
    const [tab] = await chrome.tabs.query({ active:true, currentWindow:true });
    if (connection?.configured && connection.tabId === tab?.id) {
      const response = await chrome.runtime.sendMessage({ type: 'ferro-disconnect' });
      if (!response || response.error) throw new Error(response?.error || 'Disconnect failed.');
      tokenInput.value = '';
      if (response.warning) actionError = `Disconnected locally. ${response.warning}`;
    } else {
      if (!tab || !/^https?:\/\//.test(tab.url || '')) {
        throw new Error('Open the http(s) tab you want Ferro to drive first.');
      }
      const base = baseInput.value.trim().replace(/\/+$/, '');
      const token = connection?.configured || connection?.credentialsAvailable ? '' : tokenInput.value.trim();
      if (!/^http:\/\/(127\.0\.0\.1|localhost):\d+$/.test(base)) {
        throw new Error('Bridge URL must be http://127.0.0.1:<port>. Check ferro-mcp status for the port.');
      }
      if (!token && !connection?.configured && !connection?.credentialsAvailable) throw new Error('Enter the pairing token from the local bridge-token file.');
      let response = await chrome.runtime.sendMessage({
        type: 'ferro-connect', connection: { base, token, tabId: tab.id },
      });
      if (response?.requiresConfirmation) {
        if (!confirm('Disconnect the current tab and connect this one?')) { actionError = 'Connection unchanged.'; return; }
        response = await chrome.runtime.sendMessage({type:'ferro-connect', connection:{base,token,tabId:tab.id}, confirmDisconnectTab:response.pairedTab});
        if (response?.requiresConfirmation) throw new Error('The paired tab changed. Connect again to confirm.');
      }
      if (!response || response.error) throw new Error(response?.error || 'Connect failed.');
      tokenInput.value = '';
      actionError = 'Connected. Click Open chat to continue.';
    }
  } catch (error) {
    actionError = error.message || String(error);
  } finally {
    pending = false;
    await refreshStatus();
  }
};

openChat.onclick = () => {
  if (activeTab?.id !== connection?.tabId || !connection?.configured) return;
  // Must be called directly from this click, before any async operation.
  chrome.sidePanel.open({tabId:activeTab.id}).then(() => window.close()).catch(error => { status.textContent = error.message; });
};

refreshStatus();
setInterval(refreshStatus, 2000);

// Match the frame color already chosen in the chat panel.
if (chrome.storage?.local) chrome.storage.local.get('ferroFrame').then(({ferroFrame}) => {
  if (!/^#[a-f0-9]{6}$/i.test(ferroFrame || '')) return;
  document.body.style.setProperty('--frame',ferroFrame);
  const rgb=[1,3,5].map(i=>parseInt(ferroFrame.slice(i,i+2),16));
  document.body.classList.add(rgb[0]*.2126+rgb[1]*.7152+rgb[2]*.0722<140?'custom-dark':'custom-light');
}).catch(()=>{});
