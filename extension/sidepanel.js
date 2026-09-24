'use strict';
const $ = id => document.getElementById(id);
let connection = null;
let session = '';
let messages = [];
let chats = [];
let activeChatId = '';
let running = false;
let request = null;
let clock = null;
let saveQueue = Promise.resolve();

function notice(text) { $('notice').textContent = text; }
function settings(open) { $('settings').hidden = !open; document.body.classList.toggle('settings-open', open); }
function saveChat() {
  const chat = chats.find(item => item.id === activeChatId);
  if (chat) {
    chat.messages = messages.slice(-100);
    chat.updatedAt = new Date().toISOString();
    chat.unfinished = running;
  }
  chats.sort((a,b) => b.updatedAt.localeCompare(a.updatedAt));
  chats = chats.slice(0,30);
  const recent=[chats.find(item=>item.id===activeChatId),...chats.filter(item=>item.id!==activeChatId)].filter(Boolean);
  const stored=[];
  for (const chat of recent) {
    for (const limit of [100,50,20,5]) {
      const candidate={...chat,messages:chat.messages.slice(-limit)};
      const next=[...stored,candidate];
      if (new TextEncoder().encode(JSON.stringify({activeId:activeChatId,chats:next})).length<=7*1024*1024) {
        stored.push(candidate);
        break;
      }
    }
  }
  // Serial writes prevent an older snapshot overwriting a completed result.
  const value = {activeId:activeChatId,chats:stored};
  saveQueue = saveQueue.catch(() => {}).then(() => chrome.storage.local.set({ferroChats:value}));
  return saveQueue;
}
function titleFor(text) { return text.replace(/\s+/g,' ').trim().slice(0,48) || 'New chat'; }
function renderChatPicker() {
  const picker=$('chat-picker');
  picker.replaceChildren();
  for (const chat of chats) {
    const option=document.createElement('option');
    option.value=chat.id; option.textContent=chat.title || 'New chat'; picker.append(option);
  }
  picker.value=activeChatId;
}
function renderMessages() {
  $('messages').replaceChildren();
  for (const m of messages) bubble(m.role,m.text,m.detail);
  $('history-toggle').disabled=messages.length===0;
}
function bubble(role, text, detail) {
  const article = document.createElement('article');
  article.className = 'gc-bubble' + (role === 'user' ? ' gc-user' : '');
  const label = document.createElement('div'); label.className = 'gc-label'; label.textContent = role === 'user' ? 'You' : 'Ferro';
  const p = document.createElement('p'); p.textContent = text;
  article.append(label, p);
  if (detail) {
    const details = document.createElement('details');
    const summary = document.createElement('summary'); summary.textContent = 'Run details';
    const pre = document.createElement('pre'); pre.textContent = typeof detail === 'string' ? detail : JSON.stringify(detail, null, 2);
    details.append(summary, pre); article.append(details);
  }
  $('messages').append(article);
  const stream = document.querySelector('.gc-stream'); stream.scrollTop = stream.scrollHeight;
}
function add(role, text, detail) {
  // Bound local retention; never interpret model or page output as HTML.
  const entry = {role, text: String(text).slice(0, 16000), time: new Date().toISOString()};
  if (detail) entry.detail = JSON.stringify(detail).slice(0, 16000);
  messages.push(entry); messages = messages.slice(-100);
  const chat=chats.find(item=>item.id===activeChatId);
  if (chat && role==='user' && !chat.messages.some(item=>item.role==='user')) {
    chat.title=titleFor(text);
    renderChatPicker();
  }
  bubble(role, entry.text, entry.detail);
  return saveChat();
}
function setRunning(value) {
  running = value;
  $('send').disabled = value;
  $('message').disabled = value;
  $('interactions').disabled = value;
  $('chat-picker').disabled = value;
  $('new-chat').disabled = value;
  $('stop').hidden = !value;
  $('stop').disabled = false;
}
async function api(route, body = {}, signal) {
  if (!connection) throw new Error('Connect your Chrome tab in Settings first.');
  const response = await fetch(`${connection.base}/chat/${route}`, {
    method:'POST', headers:{'Authorization':`Bearer ${connection.token}`, 'Content-Type':'application/json', 'X-Ferro-Chat-Session':session},
    body:JSON.stringify(body), signal:signal || AbortSignal.timeout(10000),
  });
  let data;
  try { data = await response.json(); } catch (_) { throw new Error(`Service returned HTTP ${response.status}. Check the service and pairing token.`); }
  if (!response.ok) throw new Error(data.error || `Service returned HTTP ${response.status}.`);
  return data;
}
async function refresh() {
  if (!connection) { $('connection-state').textContent = 'Connect a tab to begin'; return; }
  const state = await api('status');
  const tab = await chrome.tabs.get(connection.tabId).catch(() => null);
  $('connection-state').textContent = state.connected && state.paired_tab === String(connection.tabId) ? (tab?.title || 'Tab connected') : 'Tab disconnected';
  $('connection-state').title = tab?.url || '';
  return state;
}
async function loadSettings() {
  if (!connection) return;
  const m = await api('settings');
  $('model-url').value = m.base_url || 'https://openrouter.ai/api/v1';
  $('model').value = m.model || '';
  $('api-key').value = '';
  $('api-key').placeholder = m.has_key ? 'Key saved · leave blank to keep' : 'Enter your provider key';
  const policy = await api('origins');
  $('origins').value = (policy.origins || []).join('\n');
}
function theme(color) {
  document.body.classList.remove('custom-dark', 'custom-light');
  document.body.style.removeProperty('--frame');
  if (/^#[a-f0-9]{6}$/i.test(color || '')) {
    document.body.style.setProperty('--frame', color);
    const rgb = [1,3,5].map(i => parseInt(color.slice(i,i+2),16));
    document.body.classList.add((rgb[0]*.2126+rgb[1]*.7152+rgb[2]*.0722)<140 ? 'custom-dark' : 'custom-light');
    $('frame-color').value = color;
  } else { $('frame-color').value = matchMedia('(prefers-color-scheme:dark)').matches ? '#202124' : '#dee1e6'; }
}
$('settings-toggle').onclick = async () => { settings(true); try { await loadSettings(); } catch (e) { notice(e.message); } };
$('settings-close').onclick = () => settings(false);
$('connection-form').onsubmit = async event => {
  event.preventDefault();
  if (running) { notice('Stop the task before changing the paired tab.'); return; }
  try {
    const [tab] = await chrome.tabs.query({active:true, currentWindow:true});
    if (!tab || !/^https?:\/\//.test(tab.url || '')) throw new Error('Open the website tab you want to work in first.');
    const base = $('base').value.trim().replace(/\/+$/, '');
    // Use the literal loopback host so the server can enforce its Host header.
    if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(base)) throw new Error('Use http://127.0.0.1:<port> for the local service.');
    const token = $('token').value.trim() || connection?.token;
    if (!token) throw new Error('Paste the pairing token from bridge-token.');
    const next = {base, token, tabId:tab.id};
    const reply = await chrome.runtime.sendMessage({type:'ferro-connect', connection:next});
    if (!reply || reply.error) throw new Error(reply?.error || 'Could not pair this tab.');
    connection = next; $('token').value = '';
    await refresh(); await loadSettings();
    if (!$('origins').value.trim()) $('origins').value = new URL(tab.url).origin;
    notice('Connected. Set your model and allowed websites, then close Settings.');
  } catch (e) { notice(e.message); }
};
$('disconnect').onclick = async () => {
  if (running) { notice('Stop the task before disconnecting.'); return; }
  const reply = await chrome.runtime.sendMessage({type:'ferro-disconnect'});
  if (reply?.error) { notice(reply.error); return; }
  connection = null; $('token').value = ''; await refresh(); notice('Disconnected.');
};
$('model-form').onsubmit = async event => {
  event.preventDefault();
  try {
    await api('settings', {save:true,base_url:$('model-url').value.trim(),model:$('model').value.trim(),api_key:$('api-key').value.trim(),clear_key:$('clear-key').checked});
    $('api-key').value=''; $('clear-key').checked=false;
    await loadSettings(); notice('Model settings saved on this computer.');
  } catch (e) { notice(e.message); }
};
$('origins-form').onsubmit = async event => {
  event.preventDefault();
  try { await api('origins',{origins:[...new Set($('origins').value.split('\n').map(s=>s.trim()).filter(Boolean))]}); notice('Allowed websites saved.'); }
  catch (e) { notice(e.message); }
};
$('frame-color').oninput = async event => { const color=event.target.value; theme(color); await chrome.storage.local.set({ferroFrame:color}); };
$('reset-color').onclick = async () => { theme(null); await chrome.storage.local.remove('ferroFrame'); };
$('history-toggle').onclick = () => {
  const open=document.querySelector('main').classList.toggle('gc-history');
  $('history-toggle').setAttribute('aria-pressed',String(open));
  $('history-toggle').textContent=open?'Fade history':'Full history';
};
$('chat-picker').onchange = async () => {
  if (running) return;
  const chat=chats.find(item=>item.id===$('chat-picker').value);
  if (!chat) return;
  activeChatId=chat.id; messages=chat.messages.slice(-100);
  renderMessages(); await saveChat();
  if (chat.unfinished) { chat.unfinished=false; await add('assistant','The panel closed before the previous result was saved. Inspect the page before retrying; the task has not been restarted.'); }
  notice('');
};
$('new-chat').onclick = async () => {
  if (running) { notice('Stop the task before starting a new chat.'); return; }
  const chat={id:crypto.randomUUID(),title:'New chat',messages:[],updatedAt:new Date().toISOString(),unfinished:false};
  chats.unshift(chat); activeChatId=chat.id; messages=[];
  renderChatPicker(); renderMessages(); await saveChat(); notice('');
};
$('clear').onclick = async () => {
  if (running) { notice('Stop the task before clearing chat.'); return; }
  messages=[]; renderMessages(); await saveChat(); notice('Chat cleared from this device.');
};
$('export').onclick = () => {
  const body=messages.map(m=>`## ${m.role === 'user' ? 'You' : 'Ferro'} · ${m.time}\n\n${m.text}${m.detail ? '\n\nRun details:\n\n```json\n'+m.detail+'\n```' : ''}`).join('\n\n');
  const url=URL.createObjectURL(new Blob([body],{type:'text/markdown'}));
  const a=document.createElement('a'); a.href=url; a.download='ferro-chat.md'; a.click(); setTimeout(()=>URL.revokeObjectURL(url),1000);
};
$('stop').onclick = async () => {
  $('stop').disabled=true;
  notice('Stopping. An action already dispatched may have completed.');
  try { await api('cancel'); } catch (_) { /* abort the request even when transport is down */ }
  request?.abort();
};
$('message').onkeydown = event => {
  if (event.key==='Enter' && !event.shiftKey && !event.isComposing) { event.preventDefault(); $('composer').requestSubmit(); }
};
$('composer').onsubmit = async event => {
  event.preventDefault();
  if (running) return;
  const goal=$('message').value.trim();
  if (!goal) return;
  if (!connection) { settings(true); notice('Connect a tab first.'); return; }
  const historyText=messages.slice(-8).map(m=>`${m.role}: ${m.text}`).join('\n');
  const historyChars=Array.from(historyText);
  let low=0,high=historyChars.length;
  while(low<high){const mid=Math.floor((low+high)/2);if(new TextEncoder().encode(historyChars.slice(mid).join('')).length<=14000)high=mid;else low=mid+1;}
  const history=historyChars.slice(low).join('');
  setRunning(true); request=new AbortController();
  const started=Date.now();
  try {
    await add('user',goal);
    $('message').value='';
    notice('Planning and working in your paired tab…');
    clock=setInterval(()=>notice(`Working · ${Math.floor((Date.now()-started)/1000)}s · Keep the paired tab open`),1000);
    const response=await api('run',{id:crypto.randomUUID(),goal,history,interactions:$('interactions').checked},AbortSignal.any([request.signal,AbortSignal.timeout(310000)]));
    const out=response.output;
    const result=out?.result ?? out?.message ?? out;
    await add('assistant',(response.is_error?'Stopped: ':'')+(typeof result==='string'?result:JSON.stringify(result,null,2)),out?.metrics);
    notice(response.is_error?'Inspect the page before starting another task.':'Task finished.');
  } catch (e) {
    await add('assistant',`Task interrupted: ${e.name==='AbortError'?'stopped':e.message}. The result may be incomplete; inspect the page before retrying.`);
    notice('Task was not retried.');
  } finally {
    clearInterval(clock); request=null; setRunning(false); await saveChat(); $('message').focus();
    await refresh().catch(()=>{ $('connection-state').textContent='Service unavailable'; });
  }
};
(async () => {
  const state=await chrome.storage.session.get(['connection','ferroChatSession']);
  connection=state.connection || null;
  session=state.ferroChatSession || crypto.randomUUID();
  await chrome.storage.session.set({ferroChatSession:session});
  const local=await chrome.storage.local.get(['ferroChat','ferroChats','ferroFrame']);
  theme(local.ferroFrame);
  const archive=local.ferroChats;
  if (archive?.chats?.length) {
    chats=archive.chats.filter(chat=>chat && typeof chat.id==='string' && Array.isArray(chat.messages)).slice(0,30);
    activeChatId=chats.some(chat=>chat.id===archive.activeId)?archive.activeId:(chats[0]?.id || '');
  } else if (local.ferroChat?.messages?.length) {
    activeChatId=crypto.randomUUID();
    chats=[{id:activeChatId,title:titleFor(local.ferroChat.messages.find(m=>m.role==='user')?.text || 'Previous chat'),messages:local.ferroChat.messages.slice(-100),updatedAt:new Date().toISOString(),unfinished:!!local.ferroChat.unfinished}];
  }
  if (!chats.length) {
    activeChatId=crypto.randomUUID();
    chats=[{id:activeChatId,title:'New chat',messages:[],updatedAt:new Date().toISOString(),unfinished:false}];
  }
  messages=chats.find(chat=>chat.id===activeChatId).messages.slice(-100);
  const current=chats.find(chat=>chat.id===activeChatId);
  const unfinished=!!current.unfinished;
  current.unfinished=false;
  renderChatPicker(); renderMessages();
  await saveChat();
  if (unfinished) await add('assistant','The panel closed before the previous result was saved. Inspect the page before retrying; the task has not been restarted.');
  if (!messages.length) bubble('assistant','A little help with the tab in front of you.\n\nConnect a tab in Settings, then ask me to read a request, pull out the requirements, or draft a response here.');
  if (connection) { $('base').value=connection.base; await refresh(); }
  else { settings(true); await refresh(); }
})().catch(e=>notice(e.message));
