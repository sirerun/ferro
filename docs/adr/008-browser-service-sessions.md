# ADR 008: Complete the Chrome extension service and reserve tabs per agent

Accepted — 2026-09-22.

The extension backend now implements the existing browser engine instead of
launching a second Chrome profile. A local authenticated bridge carries resolved
driver commands (`op`, `selector`, deadlines and expected origin), rather than
planner actions (`kind`, numbered refs). Both the runner's initial snapshot and
its repair snapshot use the selected driver. The public `RunDriver` entrypoint
requires no synthetic CDP context.

## Agent interface and ownership

MCP remains the agent API. Stdio and Unix-socket relay serve local clients;
Streamable HTTP serves remote clients on the verified local Tailscale IPv4
address. Remote service operation is opt-in with `FERRO_MCP_REMOTE=true`.
The remote listener requires a separate bearer token. Its host must match the
Tailscale CLI result and an assigned local interface; public, wildcard, loopback,
and LAN overrides are rejected. Tests exercise the same transport on loopback
without opening a production loopback MCP listener.

Direct extension tools require `acquire_tab`. The lease belongs to the MCP
session, survives individual tool calls, defaults to five minutes and is capped
at fifteen minutes per renewal. `release_tab` clears refs and extraction state.
`run_task` can instead hold exclusive access for its whole call without a lease.
An active lease held by another session rejects it. Expiry never permits a new
call to interrupt an already executing call. CDP clients retain their existing
single-call behavior when no explicit lease exists.

`browser_status` and `cancel_task` remain responsive during a browser call.
Only the calling session can cancel its task. Relay disconnects cancel the
owner-side call. Failed relay responses are not replayed: the browser may already
have performed the action. A lease is a concurrency boundary, not separate
account authorization; everyone holding the service credential has the same
origin permissions.

## Content and action boundaries

This supersedes ADR 005's ungated snapshot policy: snapshots contain private page
content and are now allowlisted. Every driver action in a generated plan is
checked, including subsequent reads after redirects. DOM waits are checked too.
A fixed-duration wait reads no page content. Policy edits, deletions and malformed
files take effect immediately and fail closed. No model is required for direct
tools; `run_task` reports a configuration error unless a model is configured.

The extension validates the expected origin and deadline before touching the
page, and again after asynchronous preparation before input. Expired queued
commands are never dispatched. An uncertain dispatched action stops the runner
without model repair. Login/challenge detection returns a structured terminal
status immediately; it does not poll for fifteen minutes or solve the challenge.
`FERRO_MCP_BLOCK_TIMEOUT` bounds the overall call (default fifteen minutes);
individual executor actions and bridge round trips retain five-second budgets.
Pairing generations bind each call to its original tab pairing; re-pairing stops
the old task instead of transferring its remaining actions.

Pairing remains explicit through the extension popup. It is session-only and
must be repeated after Chrome restarts. `/pair` verifies the token before the
popup reports success; `/disconnect` releases the bridge pairing. Neither token
is printed in service logs or returned by agent tools. The bridge binds only a
loopback IP. Authentication tokens are private regular files with mode 0600.

The allowlist gates automation and disclosure, not all browser network traffic:
a site can still redirect, open windows, or submit requests as part of its own
behavior. The extension controls one paired top-level tab; multi-tab workflows,
file upload/download APIs, canvas interaction and account isolation are outside
this change. Ignitionphase remains an independent potential consumer.
