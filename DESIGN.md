# DESIGN.md

## Architecture

ferro treats the LLM as a compiler, not an agent: the model is asked once
(or rarely) for a declarative JSON plan, and deterministic Go code executes
that plan mechanically against Chrome via CDP (`chromedp`). Re-planning
happens only on failure. This is the core bet the RFC and `WORKPLAN.md`
recorded, and it drives the package split below.

```
ferro/
├── ferro.go                  # public API: Run, Runner, Task, RunMetrics, Option
│
├── internal/
│   ├── core/                 # the engine — unexported to users
│   │   ├── plan.go           # Action, Plan, Validate
│   │   ├── snapshot.go       # page compiler, Element, Render
│   │   ├── executor.go       # mechanical loop, ExecuteFrom, BrowserContext
│   │   ├── locator.go        # ref resolution (ref -> selector, cache-first)
│   │   ├── settle.go         # domSettle (DOM-mutation-quiescence wait)
│   │   ├── runner.go         # planner call, run loop, PlanAgain, repair loop
│   │   ├── templates.go      # {{extract.*}} expansion
│   │   ├── repair.go         # error taxonomy, patch-a-step repair prompt
│   │   ├── cache.go          # resolution cache (M4) + plan-replay support
│   │   └── metrics.go        # RunMetrics
│   │
│   ├── browser/               # chromedp pool
│   │   ├── browser.go         # Browser, Config, tab launch/acquire/close
│   │   └── pool.go            # taskContext: Navigate/CDP/Release
│   │
│   └── llm/                   # LLM client implementations
│       ├── client.go          # the Complete-shaped interface
│       └── openai.go          # OpenAICompatible: any /v1/chat/completions endpoint
│
├── examples/shop/main.go      # runnable demonstration of the public API
├── testdata/pages/            # fixture HTML for future golden/snapshot tests
└── integration/                # build-tag-gated real-model suite (go test -tags=integration)
```

## Why internal/core, internal/browser, internal/llm

The engine (`internal/core`) is the part with real design risk: plan
validation, the DOM-to-snapshot compiler, the zero-LLM executor, ref
resolution, the repair taxonomy, and the resolution cache. Keeping it
`internal/` means the module can restructure or rewrite any of that — swap
`chromedp` for `go-rod`, change the cache format, add vision grounding later
— without breaking anyone importing `github.com/sirerun/ferro`.

`internal/browser` and `internal/llm` are kept as separate internal
packages, not folded into `core`, because they are the two places an
integrator is most likely to want an alternative implementation (a
different browser driver; a non-OpenAI-compatible LLM backend). Isolating
them means a future second implementation of either doesn't touch `core` at
all. `internal/browser` depends on `internal/core` only for the
`BrowserContext` interface shape (structurally, no import needed on the
`taskContext` side — `browser.Acquire` returns `core.BrowserContext`
directly). `internal/llm` has no dependency on `core`: its `Client`
interface mirrors `core.LLMClient` structurally, so `OpenAICompatible`
satisfies both without an import.

## Why ferro.go is a thin facade

`ferro.go` at the repo root is deliberately small: type aliases
(`Task = core.Task`, `RunMetrics = core.RunMetrics`, ...) plus a handful of
constructors and two entry points, `Runner.Run` and the convenience
`ferro.Run`. It exists so that:

1. The public API surface is auditable in one file.
2. Internal refactors (splitting a file, renaming an unexported helper,
   changing the repair prompt) never require a version bump for API
   consumers, because nothing in `internal/` is part of the compatibility
   contract.
3. Godoc for the module reads as a short, intentional surface rather than
   the full engine.

## Design principles (from the RFC)

- The LLM plans once; Go executes. Deterministic code owns waits, retries,
  navigation, and form input. `Executor` (internal/core/executor.go)
  contains zero LLM calls — that's architectural, not incidental.
- Text over vision by default: the page compiler (`internal/core/snapshot.go`)
  emits a compact, numbered `[N]` list of interactive/visible elements, not
  a screenshot.
- Schema-constrained LLM output, validated before execution
  (`Plan.Validate`).
- Any OpenAI-compatible endpoint works, local models included
  (`internal/llm.OpenAICompatible`).
- Library, not platform — no cloud, no daemon.

## RunMetrics and the token-efficiency claim

`RunMetrics` (`internal/core/metrics.go`) is the machine-checkable evidence
for the RFC's headline claim: `LLMCalls`, `Plannings`, `Repairs`,
`CacheHits`, and a coarse `EstimatedTokens` (len(prompt+response)/4) are
threaded through `Runner.Run` and returned alongside the task result. This
was flagged in `WORKPLAN.md` as task E1-T7/E3-T1 ("Run result must expose a
metrics struct") and now exists as part of the public API rather than a
future addition.

## Runtime hardening (2026-09-07)

Plan replay and selector persistence now share a versioned, atomically written
cache. The Runner creates per-run executor state and flushes the shared cache
on every exit. See README's replay/extraction contract and ADR 003 for boundaries.

Schema extraction uses a typed control transfer from executor to runner; the
executor still makes zero model calls. The runner validates extracted JSON before
resuming, validates Task.Schema on completion, and reports real cache/repair metrics.

Planner shape validation, one correction retry, bare-action repair parsing,
caller-owned RunOn APIs, and per-field extraction recovery are implemented.

Remaining limitations: semantic selector collisions; no hard tab-count limit;
no cross-process cache-file locking; real-model validity/latency benchmarks remain
separate from the deterministic Chrome fixture suite. The old chromedp/CDP versions
can log unknown modern Chrome event-enum values during fixture tests.

## Context lifetime

Two separate dogfood defects (bugs #1 and #7, 2026-09-04) shared one root
cause: a chromedp context was wrapped (`context.WithTimeout`,
`context.WithCancel`) and the wrapper was passed to the *first*
`chromedp.Run` call on a tab. chromedp starts the tab's CDP
event-listener goroutine on that first Run and binds it to whichever
context it was given — cancelling the wrapper later, even after a
successful call, tears the whole session down, and every subsequent Run on
that tab fails with "invalid context" or "context canceled". The failure
surfaces far from the cause (a later, unrelated action), so this is a
landmine for the next contributor, not a one-off bug.

The rule: the bare tab context (`pooledContext.cdpCtx` in
`internal/browser/browser.go`) is the only context ever passed to the
*first* `chromedp.Run` on a tab; `newTab` enforces its launch deadline with
a `select` on a goroutine rather than a `WithTimeout` wrapper for exactly
this reason. After launch, callers may derive short-lived children from
that context for individual actions. Snapshot and executor code must
receive `BrowserContext.CDP()` (or a context derived from it) — never a
caller's unrelated `ctx` — which is why `internal/core/executor.go`
documents this on the `CDP()` method itself and `internal/core/runner.go`'s
call sites point back to it instead of re-explaining the landmine each
time.

This convention is written down in three places so a grep for any one of
them finds the others: `docs/adr/001-chromedp-context-lifetime.md`, the
package doc in `internal/browser/browser.go`, and this section. Any new
call site that wraps a CDP context should cite the ADR in its own comment.
`cmd/ferro-mcp`'s daemon (ADR 004) holds the tab pool open for the life of
the process, which is exactly the shape of long-lived-tab surface area
this landmine bites hardest.

## cmd/ferro-mcp and internal/mcp (2026-09-10)

`cmd/ferro-mcp` (a Model Context Protocol server) and `internal/mcp` (its
leader election, origin allowlist, and tool relay) are a new consumer of
the library, added alongside `examples/shop/main.go` — not a change to it.
`ferro.go` and `internal/core` remain exactly the LLM-plans-once,
deterministic-executor engine described above, with zero knowledge of MCP,
sockets, or process lifecycle. "Library, not platform" (the last bullet
under Design principles) still describes `ferro.go`/`internal/core`
precisely; the daemon and its Unix-socket relay live entirely in
`cmd/ferro-mcp`/`internal/mcp`, which drive the library through the same
`ferro.NewBrowser`, `ferro.NewRunner`, and `Runner.RunOn` entry points any
other caller would use.

The one addition to the engine itself is `browser.Config.ProfileDirectory`
(`internal/browser/browser.go`), backing `FERRO_MCP_CHROME_PROFILE_DIRECTORY`:
passed through to `chromedp.Flag("profile-directory", ...)` for a caller
whose Chrome user-data directory holds more than one profile. It's a small,
generally useful engine option, not MCP-specific plumbing — `cmd/ferro-mcp`
just happens to be its first caller.

See `docs/adr/004-mcp-server-shared-browser-daemon.md` (leader election, one
shared tab serialized through a mutex, Unix-socket relay) and
`docs/adr/005-mcp-origin-allowlist.md` (the deny-by-default per-origin gate).


## Existing-profile browser service (2026-09-22)

`Runner.RunDriver` routes both planning/repair snapshots and action execution
through `core.PageDriver`. `internal/extbridge.ExtensionDriver` translates
resolved driver operations into the extension's `op` protocol; the original CDP
path remains supported. The extension service owner never launches a browser.
`internal/mcp` retains the same tools across backends, adds per-session tab leases,
and exposes authenticated Streamable HTTP on a verified local Tailscale IP.
The extension poll/reply bridge stays loopback-only and has a separate credential.

See `docs/adr/006-extension-execution-backend.md`,
`docs/adr/007-tailscale-remote-transport.md`, and
`docs/adr/008-browser-service-sessions.md` for protocol, ownership, cancellation,
and the revised content-read authorization policy. Page data remains untrusted
input. An allowlist is not a browser-network firewall or a substitute for agent
instructions about authorized account actions.
