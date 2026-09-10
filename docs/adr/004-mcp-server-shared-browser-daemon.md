# ADR 004: MCP server as a leader-elected daemon over a single shared browser

## Status
Accepted (revised 2026-09-10 -- see "Revision" below)

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

### Revision (2026-09-10): builds on an existing local prototype

Before writing any code against this ADR, a local-only worktree
(`ferro-wt-mcp`, branch `mcp-server`, 7 commits from 2026-09-05, never
pushed) was found already containing a working, live-tested MCP server:
`cmd/mcp/main.go`, built on the **official**
`github.com/modelcontextprotocol/go-sdk/mcp` (v1.7.0), exposing one
`run_task` tool over one tab acquired once at startup. It was driven
live against a real external chat site (`oxalpha.com/chat`) and its
Chrome profile at `~/.ferro-mcp/chrome-profile` is already signed in by
hand. This ADR's original text specified hand-rolling MCP's JSON-RPC 2.0
framing from the standard library; that is now rejected in favor of the
already-adopted official SDK -- there is no benefit to re-implementing a
wire protocol an official, maintained SDK already covers, and the operator
had already made this call. The rest of this ADR (leader election, one
shared tab, deny-by-default allowlist in ADR 005) remains necessary: the
prototype has no answer for a dozen MCP clients sharing one Chrome profile,
which is exactly the gap this ADR closes.

## Decision

`cmd/ferro-mcp` (renamed from the prototype's `cmd/mcp` for a distinct
binary name on `PATH`) is a single binary with two roles, chosen at
startup by leader election, not by a flag. Both roles run an identical
`mcp.NewServer` from `github.com/modelcontextprotocol/go-sdk/mcp` with the
same registered tools on their own stdio -- an MCP client cannot tell which
role it talked to. The roles differ only in how a tool handler gets its
answer:

1. On start, it attempts an exclusive `flock` on `$FERRO_MCP_HOME/mcp.lock`
   (default `$FERRO_MCP_HOME` is `$HOME/.ferro-mcp`, the prototype's
   existing, already-signed-in directory -- not a new path).
2. If it wins the lock, it is the **owner**: its tool handlers call
   `ferro.RunOn`/the executor directly against the real `ferro.Browser`
   pool opened on `$FERRO_MCP_HOME/chrome-profile`, and it also listens on
   a Unix domain socket at `$FERRO_MCP_HOME/mcp.sock` to serve the same
   calls for shims.
3. If the lock is held, it is a **shim**: it does not touch Chrome. Each of
   its tool handlers encodes its own arguments, sends them to the owner
   over the Unix socket using a small internal request/response encoding
   (JSON lines; this is ferro's own wire format between owner and shim, not
   MCP JSON-RPC -- the MCP protocol is only ever spoken by each process to
   its own client on stdio), and returns the owner's answer as its own
   tool result. From the MCP client's point of view a shim is
   indistinguishable from an owner; only one process per machine ever
   holds the browser.
4. The owner keeps a **single shared tab** (not a tab per client). Tool
   calls from every connected client (owner's own stdio, or relayed from
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
  untouched by this ADR itself (T11.0's doFill fix, ported from the
  prototype, does touch `internal/core`, but that is an independent bug
  fix discovered via dogfooding, not a consequence of the MCP server
  design). `cmd/ferro-mcp` and `internal/mcp` are a new consumer of the
  library, the same as `examples/shop` is, so DESIGN.md's "library, not
  platform -- no cloud, no daemon" principle continues to describe the
  library itself; the daemon lives one layer above it.
- `go.mod` gains `github.com/modelcontextprotocol/go-sdk` and its
  transitive dependencies (`google/jsonschema-go`, `segmentio/encoding`,
  etc.) -- the first non-chromedp dependencies in this module. This is
  accepted because it is the same official SDK the prototype already
  proved out, not a new dependency decision made independently here.
