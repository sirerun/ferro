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
— without breaking anyone importing `github.com/dndungu/ferro`.

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

## Known sharp edges (carried over from WORKPLAN.md, not yet fully resolved)

- `settle.go`: a navigation mid-`domSettle` can destroy the JS execution
  context and surface as an error; it should instead be treated as
  "settled" (navigation implies the DOM changed). Not yet fixed — tracked
  as kazi task **E1-T3**.
- `locator.go` / `buildSelector`: signature-based CSS selectors
  (`button[aria-label="..."]`) can match multiple elements or drift on
  dynamic text. Mitigated by routing failures to the repairer and, on
  repeat runs, by the resolution cache — but not eliminated. See kazi
  **E2-T1**-**E2-T3**.
- The planner prompt has not been validated against a real small model —
  whether a 7-14B local model reliably emits valid plans at temperature 0
  is the core, still-untested bet of the design. See `integration/` and
  kazi **E5**.

## Status

Design sketch turned into a compiling, restructured Go module — not yet a
verified build against real Chrome/LLM traffic. `go build ./...`,
`go vet ./...`, and `go test ./...` are green; the browser- and
model-gated suites (`FERRO_TEST_BROWSER=1`, `-tags=integration`) still need
a real run to validate, per `WORKPLAN.md`'s Definition of Done.
