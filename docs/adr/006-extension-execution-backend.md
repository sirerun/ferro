# ADR 006: Browser-extension execution backend for sites without CDP access

## Status
Accepted

## Date
2026-09-10

## Context

`cmd/ferro-mcp` (ADR 004) drives Chrome via CDP (`chromedp`), which requires
a dedicated, non-default Chrome profile -- Chrome refuses to enable remote
debugging against a real, actual-use profile at all, as a deliberate
guardrail against automating a user's real logged-in session. That is
correct for the daemon's original goal (any MCP client can drive a shared,
signed-in automation profile) but does not satisfy a different, narrower
goal: driving David's REAL, daily-use Chrome profile -- the one with his
actual logged-in sessions, not a separately-signed-in dedicated one -- so
that agents running on another machine (DGX, or the "Rakazo" fleet) can keep
doing browser work overnight while he sleeps, on websites that have no
MCP/API surface at all.

A separate, already-working project (`~/Code/dndungu/ox`, specifically
`ox/extension/`) proves the relevant technique: a Manifest V3 Chrome
extension, loaded unpacked into a real Chrome profile, can read the live DOM
via a content script and issue *trusted* synthetic mouse clicks via the
`chrome.debugger` API scoped to one explicitly paired tab -- no CDP
`--remote-debugging-port` involved, so Chrome's default-profile guardrail
never triggers. `ox`'s extension is hardcoded to one site (`oxalpha.com`)
via CSS selectors and talks only to a `127.0.0.1`-bound local bridge. This
ADR generalizes that proven technique into a second execution backend for
ferro's existing engine, so it works against arbitrary sites using the same
`internal/core.Action` vocabulary and ref-numbering scheme ferro's CDP
backend already uses -- not a second, parallel automation engine.

## Decision

**1. `core.PageDriver`: extract the chromedp-specific calls out of
`internal/core/executor.go` into an interface.** Today `doFill`, `doClick`,
`doExtract`, and the snapshot-generation path call `chromedp.Run`/
`chromedp.Evaluate` directly. `PageDriver` abstracts exactly those calls
(navigate, click a resolved selector, fill a resolved selector, select an
option, send a key, scroll, extract fields, run the snapshot-compiler
script, wait-visible) so `Executor` depends on the interface, not on
chromedp. `ChromedpDriver` wraps the existing chromedp calls with zero
behavior change -- this is a pure refactor with no new capability, and its
own acceptance criterion is that every existing test still passes unchanged.
This is the foundation everything below builds on; without it, `run_task`
(the full planner-driven loop) could never work against a non-CDP backend,
and ferro would need a second, drifting implementation of plan execution
just for the extension case -- exactly what this ADR exists to avoid.

**2. `internal/extbridge`: a generalized version of `ox`'s local bridge.**
A Go HTTP server, bound to loopback by default (network exposure is ADR
007's decision, layered on top of this one, not part of it) speaking a
poll/reply protocol:

- `GET /next` (bearer-token authed): returns the next pending action as
  `{"id": "...", "action": <core.Action JSON>}`, or 204 if none is queued.
- `POST /reply`: the paired extension posts
  `{"id": "...", "snapshot": <core.Snapshot JSON, if requested>, "result": ..., "error": "..."}`.
- Exactly one active pairing at a time (a random token + a `chrome.tabs`
  tab id, mirroring `ox`'s popup-driven pairing flow) -- this matches
  `ferro-mcp`'s existing single-shared-tab model (ADR 004), so no new
  multi-session complexity is introduced by this backend.

**3. `extension/`: a new, generic Chrome extension in ferro's own repo**
(not a modification of `ox`, which stays independent), adapted from `ox`'s
`background.js`/`content.js`/`popup.html`/`popup.js` structure:

- The content script's adapter is generic, not site-specific: it executes
  whatever `core.Action` the bridge hands it against the live DOM, rather
  than `ox/extension/adapter.js`'s hardcoded `.msg-user`/`.new-chat-btn`
  selectors.
- **Ref-numbering parity is the hardest and most safety-critical part of
  this ADR.** `internal/core/snapshot.go`'s element-selection and numbering
  algorithm (which elements count as interactive/visible, and in what
  order) must be reimplemented in the extension's content script so that
  "ref 3" means the identical element whether a snapshot came from
  `ChromedpDriver` or the extension. This is not a one-time port: any future
  change to `snapshot.go`'s selection rules must be mirrored in the
  extension's JS, and E12's task breakdown requires a parity test (the same
  fixture page snapshotted through both drivers must produce the same
  numbered list) specifically to catch drift, not just an initial port.
- Click and key input use `chrome.debugger`-issued trusted synthetic events
  (`ox`'s proven technique, including its retry-on-dropped-debugger logic);
  fill uses the native property setter plus a dispatched `input` event
  (also `ox`'s technique) since text entry does not need trusted-event
  status the way click does on most sites.
- **The extension only ever acts on the one tab a human explicitly paired**
  via the popup, exactly like `ox`. This is a second, independent gate
  alongside ADR 005's per-origin allowlist, not a replacement for it: the
  allowlist check happens Go-side, before the bridge ever queues an action,
  so a compromised or buggy extension cannot act on a page the allowlist
  would have denied, and the pairing requirement means the extension cannot
  be silently pointed at an arbitrary tab without a human (or a
  pre-established pairing) having selected it first.

**4. `ExtensionDriver`, implementing `core.PageDriver`.** Lives in
`internal/extbridge`, encodes an `Action` to the bridge, blocks until the
paired extension's poll picks it up and replies, and decodes the result into
the same shapes `ChromedpDriver` produces. Once this exists, `run_task`
(`Runner.RunOn`) and every primitive MCP tool work against either backend
with no change to `internal/core`'s planner, repair, or resolution-cache
logic -- backend selection is a config choice in `cmd/ferro-mcp`
(`internal/mcp`), not a fork of the engine.

**5. Unattended blocked-state handling.** The extension's adapter carries a
generalized version of `ox/extension/adapter.js`'s `blocked()` heuristic
(human-verification banners, Cloudflare challenge iframes, a visible
password/email input implying a login gate) -- not site-specific text
matching, since ferro has no fixed target site. When blocked, the bridge
reports a structured `{"blocked": "<reason>"}` result rather than leaving
the caller's tool call pending indefinitely; the caller (a primitive tool or
`run_task`) surfaces this as a normal, distinguishable MCP tool result
within a bounded wait (default 15 minutes, matching `ox`'s
`--block-timeout` convention) rather than hanging. This is required
specifically because nobody is watching a cursor overnight the way `ox`'s
and `ferro-mcp`'s existing designs assume a human is.

## Consequences

- This backend can act inside every site David's real profile is logged
  into, gated only by ADR 005's allowlist file plus the pairing requirement
  above -- a materially larger blast radius than `ferro-mcp`'s existing
  CDP backend, which only ever touches a dedicated, separately-signed-in
  profile. Accepted because the whole point of this ADR is driving the real
  profile; the allowlist and pairing gates are the mitigations, not a
  claim that the risk is eliminated.
- `core.PageDriver` is a real, non-trivial refactor of `internal/core`, not
  additive-only the way `cmd/ferro-mcp` (ADR 004) was. `internal/core`
  remains "library, not platform" in the sense that it still has zero
  knowledge of MCP, sockets, or the extension protocol -- it only gains an
  interface boundary where it previously called chromedp directly.
- Ref-numbering parity between Go and JS is an ongoing maintenance
  obligation, not a one-time task -- a future `snapshot.go` change that
  isn't mirrored in the extension will silently misdirect clicks/fills
  against the extension backend while the CDP backend keeps working,
  making this a subtle, hard-to-notice class of bug if the parity test is
  ever skipped or allowed to go stale.
- `run_task`'s planner-driven loop now works uniformly across backends,
  which was the explicit design goal (reuse the engine, don't reinvent step
  semantics per backend) -- this is the payoff for taking on the
  `PageDriver` refactor instead of building a second, extension-only
  mini-engine the way a narrower reading of the ask might have produced.
