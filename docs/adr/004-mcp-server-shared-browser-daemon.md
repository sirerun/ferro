# ADR 004: MCP server as a leader-elected daemon over a single shared browser

## Status
Accepted

## Date
2026-09-10

## Context

We want ferro to expose browser control to MCP clients (Claude Code sessions
and other agents) against a persistent, signed-in Chrome profile
(`BrowserConfig.UserDataDir`, already supported by the library). The
operator runs roughly a dozen Claude Code sessions in parallel, each of
which would configure the same MCP server in its own `.mcp.json`.

The standard local MCP transport is stdio: the client spawns the server as
a subprocess and speaks JSON-RPC over its stdin/stdout. If each of a dozen
sessions independently spawns `ferro-mcp`, each spawned process would try
to open Chrome against the same `UserDataDir`. Chrome (like most browsers)
takes an exclusive lock on a profile directory; a second process pointed at
the same profile either fails to launch or forces the first instance to
hand off, which would silently kill whichever session started first.

Running a second, separate daemon binary that all `ferro-mcp` stdio shims
proxy to would solve this, but doubles the binaries to build, package, and
document, and adds a "did you remember to start the daemon" step that
defeats the point of an MCP client auto-launching its server.

## Decision

`cmd/ferro-mcp` is a single binary with two roles, chosen at startup by
leader election, not by a flag:

1. On start, it attempts an exclusive `flock` on `$FERRO_MCP_HOME/mcp.lock`
   (default `$FERRO_MCP_HOME` is `$HOME/.ferro`).
2. If it wins the lock, it is the **owner**: it opens the real
   `ferro.Browser` pool against `$FERRO_MCP_HOME/chrome-profile`, listens on
   a Unix domain socket at `$FERRO_MCP_HOME/mcp.sock`, and serves MCP
   JSON-RPC directly over its own stdio to whichever client launched it.
3. If the lock is held, it is a **shim**: it does not touch Chrome. It
   dials the existing owner's Unix socket, and for every JSON-RPC request
   it receives on its own stdio, forwards the request over the socket,
   waits for the owner's response, and writes that response back to its
   stdio. From the MCP client's point of view a shim is indistinguishable
   from an owner; only one process per machine ever holds the browser.
4. The owner keeps a **single shared tab** (not a tab per client). Tool
   calls from every connected client (owner's own stdio, or forwarded from
   any shim) are serialized through one mutex before touching the tab. This
   matches the mental model this feature is for: one signed-in browser
   window, agents take turns driving it, the same way a human would hand
   the keyboard to different tools one at a time.
5. `ferro-mcp status` and `ferro-mcp stop` dial the socket (or report "not
   running" if the lock file is absent/stale) rather than duplicating
   lifecycle state.

The Unix socket is created with `0600` permissions in
`$FERRO_MCP_HOME` (itself `0700`), so only the invoking user's processes
can reach it. No TCP port is opened.

## Consequences

- Any number of MCP clients can each configure `ferro-mcp` as their stdio
  server with zero coordination between them; exactly one of the resulting
  processes ever opens Chrome.
- If the owner process exits (crash, `ferro-mcp stop`, machine sleep
  killing it), its socket and lock become stale. The next tool call from a
  shim fails to dial; the shim must detect this, attempt to become the new
  owner (re-flock), and only then retry the call. This handoff logic is the
  main source of complexity this ADR accepts (T11.2).
- All state-changing tool calls funnel through one mutex, so this design
  does not give agents parallel tabs. If concurrent multi-tab automation is
  ever wanted, it is a new tool (`browser_new_tab`) added later, not a
  redesign of the leader-election model.
- `internal/core`, `internal/browser`, `internal/llm`, and `ferro.go` are
  untouched. `cmd/ferro-mcp` and `internal/mcp` are a new consumer of the
  library, the same as `examples/shop` is, so DESIGN.md's "library, not
  platform -- no cloud, no daemon" principle continues to describe the
  library itself; the daemon lives one layer above it.
