# ADR 005: Per-origin allowlist gates state-changing MCP tool calls

## Status
Accepted

## Date
2026-09-10

## Context

`cmd/ferro-mcp` (ADR 004) points a persistent, signed-in Chrome profile at
whatever origins the operator has logged into: email, banking, internal
tools, anything. Every MCP client that connects to it inherits those
authenticated sessions. Two threats follow directly from that:

1. A malicious or compromised page visited during an autonomous `run_task`
   plan can attempt prompt injection: text on the page that the planning
   LLM reads and treats as instructions, potentially steering the plan
   toward an action ferro then executes with the operator's live session
   (submit a form, follow a link, exfiltrate page content via `extract`).
2. An MCP client with no ill intent can still be pointed at the wrong site
   by a bad plan or a bad goal string, and there is currently no signal to
   the server about which origins the operator actually meant to authorize
   for a given run.

ferro has no UI and the daemon is headless, so a live "approve this
action" popup is not available in v1. The realistic v1 control is a
config-file allowlist the operator edits by hand, not an interactive
consent flow.

## Decision

Revised 2026-09-10 to note: `$FERRO_MCP_HOME` defaults to
`$HOME/.ferro-mcp` (the existing, already-signed-in profile directory from
the prototype ADR 004 now builds on), not a new path, and the gated tool
set now includes the prototype's `run_task` alongside the primitive tools.

- `$FERRO_MCP_HOME/allowlist.json` holds a flat list of allowed origins
  (scheme+host+port, e.g. `https://mail.google.com`). No wildcards in v1.
- Before executing any state-changing tool call --
  `navigate`, `click`, `fill`, `select`, `key`, `scroll`, `run_task` -- the
  server resolves the current tab's origin (or, for `navigate`, the target
  URL's origin) and checks it against the allowlist. `extract` is also
  gated, because reading authenticated page content is itself a
  disclosure risk, not just a mutation risk.
- `snapshot` and `wait` are read-only/no-network and are not gated -- an
  agent can always see what page it is on and ask "is this origin
  allowed" without needing to be pre-approved for it.
- A denied call returns a normal MCP tool error (not a protocol-level
  failure) naming the blocked origin and the exact line to add to
  `allowlist.json` to permit it. No browser action occurs.
- The allowlist is loaded once at daemon startup and re-read on `SIGHUP`
  or the next tool call after the file's mtime changes (whichever tasks
  implement first is fine; the requirement is "no restart needed to add an
  origin"), so the operator can extend it without killing the shared
  session.
- `run_task` additionally requires that `Task.StartURL` (or the tab's
  current origin, if `StartURL` is empty) be allowlisted before the first
  LLM planning call is made at all -- an autonomous plan never even gets a
  snapshot of a non-allowlisted origin, closing off the prompt-injection
  vector at the source rather than only at the point of action.

## Consequences

- The operator must explicitly allowlist every origin they want agents to
  touch. This is friction by design: the default is deny, matching "give
  my agents browser use in my signed-in sessions" being an explicit,
  bounded grant, not blanket access to every logged-in account.
- Read-only `snapshot`/`wait` calls against a non-allowlisted origin still
  leak page structure (element list) to the connected agent, though not
  page content or the ability to act. This is accepted for v1; if it turns
  out to matter, gating `snapshot` too is a one-line change to the same
  check (T11.3 leaves the gate as a single function so this is cheap to
  tighten later).
- No interactive consent UI exists yet. If a future need for
  per-call human approval emerges (as opposed to per-origin standing
  approval), that is a new ADR, not an amendment to this one -- it changes
  the trust model from "operator pre-approved this origin" to "operator
  approves this specific action," which is a materially different design.
