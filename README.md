# ferro

A token-efficient AI browser automation framework for Go: the LLM is asked
once (or rarely) for a declarative JSON plan; deterministic Go code executes
that plan mechanically against Chrome via CDP (`chromedp`). Re-planning
happens only on failure.

This repository is a direct transcript of a design conversation with
**Ox Alpha** (a chat AI) that produced the RFC, the file-by-file
implementation sketch, a "kazi" engineering work plan, and the M4 resolution
cache design. See `conversation.md` for the full conversation and
`WORKPLAN.md` for the detailed task breakdown. `DESIGN.md` documents the
package split below and the architecture decisions made along the way.

## Status

**Restructured into a real Go package split; not yet verified against real
Chrome/LLM traffic.** `go build ./...`, `go vet ./...`, and `go test ./...`
are green. See `WORKPLAN.md` Epic 1 ("Green Walking Skeleton") and
`DESIGN.md`'s "Known sharp edges" section for what's still unverified.

## Install

```sh
go get github.com/dndungu/ferro
```

## Quickstart

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/dndungu/ferro"
)

func main() {
	client := &ferro.OpenAICompatible{
		BaseURL: "http://localhost:11434/v1", // any OpenAI-compatible endpoint
		Model:   "qwen2.5:14b",
	}

	b, err := ferro.NewBrowser(ferro.BrowserConfig{Headless: true})
	if err != nil {
		log.Fatal(err)
	}
	defer b.Close()

	ctx := context.Background()
	result, metrics, err := ferro.Run(ctx, b, client, ferro.Task{
		Goal:     "search for coffee and report the cheapest price",
		StartURL: "https://example-shop.test",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result, metrics.LLMCalls)
}
```

See `examples/shop/main.go` for a runnable version.

## Package layout

| Path | Contents |
|---|---|
| `ferro.go` | The public API: `Run`, `Runner`, `Task`, `RunMetrics`, `Option`, `Browser`, `OpenAICompatible` |
| `internal/core/` | The engine: plan types, page compiler, executor, ref resolution, repair, resolution cache |
| `internal/browser/` | The chromedp tab pool |
| `internal/llm/` | LLM client implementations (`OpenAICompatible`) |
| `examples/shop/` | A runnable demonstration of the public API |
| `testdata/pages/` | Fixture HTML |
| `integration/` | Build-tag-gated real-model smoke suite (`go test -tags=integration ./integration/...`) |
| `conversation.md` | The full design conversation this code was extracted from |
| `WORKPLAN.md` | The "kazi" engineering work plan: epics, tasks, waves, risks, definition of done |

## Design principles (from the RFC)

- The LLM plans once; Go executes. Deterministic code owns waits, retries,
  navigation, and form input.
- Text over vision by default.
- Compact page representation: only interactive/visible elements, numbered
  `[N]` refs.
- Schema-constrained LLM output, validated before execution.
- Any OpenAI-compatible endpoint works, local models included.
- Library, not platform — no cloud, no daemon.

```
Go orchestrator
├─ Browser pool (chromedp, warm contexts)                internal/browser
├─ Page compiler: DOM → compact accessibility snapshot    internal/core (snapshot.go)
├─ Planner: one LLM call → JSON plan                      internal/core (runner.go)
├─ Deterministic executor: runs actions, waits/retries    internal/core (executor.go)
├─ Repairer: one patched step on failure                  internal/core (repair.go)
└─ Resolution cache: zero LLM calls on repeat runs        internal/core (cache.go)
```

## Running the tests

```sh
go test ./...                            # unit tests only (plan validation, parsePlan, ...)
FERRO_TEST_BROWSER=1 go test ./...       # also runs the browser-backed tests (needs Chrome)
go test -tags=integration ./integration/...  # real-model suite (needs FERRO_MODEL_URL/FERRO_MODEL_NAME)
```

## License

MIT — see `LICENSE`.
