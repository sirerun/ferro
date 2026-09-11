# ADR 007: Tailscale-bound remote transport for the shared browser daemon

## Status
Accepted

## Date
2026-09-10

## Context

ADR 004 designed `cmd/ferro-mcp` for clients on the same machine as the
daemon: stdio to whichever process launched it, plus a Unix socket for
shim-to-owner relay, both inherently local. E12 (docs/plan.md) requires
agents running on a different machine -- David's DGX GPU server, and the
"Rakazo" agent fleet -- to drive the daemon on his laptop overnight while he
sleeps. That means the daemon needs a listener actually reachable from
another machine, which is a new attack surface this ADR must bound
carefully: this daemon (with ADR 006's extension backend active) can act
inside every site David's real browser profile is logged into.

David's machines are already joined to a private Tailscale network (a
`*.ts.net` hostname is in active use elsewhere in his setup). A public
listener was never considered: the combination of "drives a real logged-in
profile" and "reachable from the public internet" is not an acceptable
trade for this project at any point.

## Decision

- The owner process (whichever `ferro-mcp` process holds ADR 004's
  exclusive flock) additionally binds a second listener to the host's
  Tailscale interface address specifically -- resolved at startup via
  `tailscale ip -4` (shelling out to the `tailscale` CLI, already present
  since the machine is joined to the mesh) or an explicit
  `FERRO_MCP_BIND_HOST` override -- **never** `0.0.0.0` and never the
  machine's public/LAN interface. If Tailscale is not running or the
  interface can't be resolved, the remote listener does not start (the
  daemon still serves local stdio/socket clients per ADR 004); this is a
  fail-closed default, not an error that blocks local use.
- The remote listener speaks MCP over the official SDK's HTTP-based
  transport (the exact transport name is confirmed against the vendored
  `github.com/modelcontextprotocol/go-sdk` version during implementation,
  T12.6) rather than requiring a DGX-side SSH tunnel onto the Unix socket.
  This is a genuine second transport, not a repurposing of ADR 004's
  owner/shim socket relay, which stays exactly as it is for local clients.
- **A second, independent gate**, layered on top of ADR 005's per-origin
  allowlist: every request on the remote listener must carry a bearer token
  read from `$FERRO_MCP_HOME/remote-token` (mode 0600, generated on first
  run if absent), checked before any tool dispatch. This exists because
  "on the Tailscale mesh" is a weaker boundary than "on this machine" --
  another device on the same tailnet should not get free access to this
  daemon just by being able to route to it.
- A rejected/missing token returns a normal, distinguishable authentication
  error, never a silent hang or a fallback to unauthenticated access.

## Consequences

- When David's laptop is asleep or off the network, the Tailscale address
  becomes unreachable and a remote client's connection attempt fails fast
  (connection refused or a bounded timeout) -- this is a different, and
  deliberately distinguishable, failure from ADR 006's "blocked" tool
  result (a live browser stuck on a CAPTCHA). A remote agent can tell "the
  laptop is off" from "a page needs a human" without guessing.
- Availability is bounded by David's laptop actually being on and connected
  to Tailscale overnight -- this was an explicit, informed trade (laptop
  chosen over an always-on machine) accepted for this project; it is not a
  design flaw of this ADR, but the plan's risk register names it so it
  isn't forgotten.
- No public exposure exists at any point: a network scan of David's public
  IP or LAN never finds this listener, only devices already authorized on
  his tailnet can attempt to reach it, and even then the bearer token gates
  every call.
- This introduces a second credential to protect (`remote-token`, alongside
  ADR 005's `allowlist.json`) -- both live under `$FERRO_MCP_HOME`, so the
  existing "treat `$FERRO_MCP_HOME` as sensitive" guidance from `ferro-mcp`'s
  README already covers it; no new documentation category is needed, but
  the README's security note (T12.11) must mention the token explicitly,
  not just the allowlist.
