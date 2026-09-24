# Standalone Chrome chat

Ferro's side panel works with one tab in your normal, logged-in Chrome profile.
A local Go process calls your model provider and runs bounded browser tasks.
Claude Code, Codex, a local model and a second Chrome profile are not required.

## Start

Build the checkout you intend to run (Go 1.25+, Chrome 116+):

```sh
GOWORK=off go build -o ferro-mcp ./cmd/ferro-mcp
FERRO_MCP_BACKEND=extension FERRO_MCP_BRIDGE_ADDR=127.0.0.1:4175 ./ferro-mcp serve
```

The foreground process remains running. In Chrome, open `chrome://extensions`,
enable Developer mode, and **Load unpacked** this checkout's `extension/` folder.
Ferro uses a cyan cursor focus mark as its logo in Chrome and its side panel.
Pin Ferro, open the website tab you want to use, and click Ferro to open its panel.
Ferro checks the page receiver and attaches it when needed, including tabs
opened before installation. If Chrome denies attachment, click the Ferro toolbar
icon on that website tab and reconnect, or refresh the website.

In Settings:

1. Keep the service URL `http://127.0.0.1:4175`. Paste the token from
   `~/.ferro-mcp/bridge-token`, then **Connect current tab**. On macOS,
   `pbcopy < ~/.ferro-mcp/bridge-token` copies it without printing it.
2. Set your provider's API base URL, model ID and API key. For OpenRouter the
   base URL is `https://openrouter.ai/api/v1`; use a model ID enabled for your
   account. **Save model**. A provider API key is separate from a chat subscription.
3. Review **Allowed websites**, enter exact origins without trailing slashes
   (for example `https://example.com`), and **Save websites**.
4. Close Settings and enter a task. Keep the paired tab open.

API keys live in a mode-0600 `chat-model.json` file under `FERRO_MCP_HOME`
(default `~/.ferro-mcp`), never in the repository or Chrome's persistent storage.
The pairing token lives in Chrome's session storage and must be entered again
after a browser restart. Ferro controls one tab at a time. To switch, open
Settings on the tab you want and choose **Connect current tab**; Ferro releases
the previous tab before pairing the new one. If the new pairing fails, the
extension attempts to restore the previous pairing.

Use the conversation picker at the top of the panel to reopen an earlier chat or
start a new one. Ferro keeps up to 30 conversations and 100 messages per chat in
this Chrome profile's local extension storage. The history is not synced to the
service or another device. **Clear chat** removes the current conversation;
**Export chat** saves it as Markdown.

To stop the service, use `./ferro-mcp stop` with the same `FERRO_MCP_HOME`.

## First research-to-draft session

Start with one page you are authorized to inspect. Leave **Allow interactions**
off. Example task:

> Read this request and return a brief with the source URL, original posting
> date if visible, requested outcome, stack, stated budget and deadline,
> missing facts, and three discovery questions. Mark absent facts unknown.
> Do not send anything or fill any form.

Then ask for a draft response in chat. The recent conversation accompanies the
next task as context; it is not a complete durable CRM. Verify extracted facts
against the page. **Export chat** saves a Markdown artifact you can put in your
private workflow workspace. Reloading the panel restores its saved chat; it does
not resubmit tasks or resume a campaign.

Read-only tasks can read, extract, scroll and navigate to allowlisted origins.
Clicks, typing, selections and key presses are blocked in the executor.
**Allow interactions** permits those inputs for tasks while enabled. Use it only
for an intended action. An origin allowlist and this toggle do not implement
recipient-specific outbound approval or payment authorization.

**Stop task** cancels work. Dispatched actions may already have happened. When a
result is interrupted, inspect the page before starting a new task. There is no
automatic retry. Tasks are bounded to five minutes and three planning passes.

This first version supports one top-level tab. Popups, cross-origin frames,
file transfer and image/canvas interaction are outside its current capabilities.
Use permitted manual imports when a source does not support browser automation.
Scheduling, prospect records, deduplication, financial tracking and commercial
approvals belong in the existing workflow system, not the side panel.

## Appearance and resource use

The panel background approximates Chrome's light/dark frame. Use **Match Chrome's
frame** for a custom theme color; messages and the composer float over that
surface. Chrome controls the panel's outside border and header.

The extension uses DOM snapshots and one command long-poll. It does not stream
screenshots, run a local model or add a browser process to your existing session.
The chat has no idle model calls; its elapsed-time timer runs only during a task.
These are architectural properties, not a measured performance comparison with
another extension. A real provider and live source still need qualification.
