# ferro

Token-efficient AI browser automation for Go.

ferro treats the LLM as a compiler, not an agent. The model is asked once
for a declarative JSON plan. Deterministic Go code then executes that plan
against Chrome through the Chrome DevTools Protocol (via `chromedp`), owning
every wait, retry, navigation, and form input. The model is consulted again
only when a step fails, and then only to patch that one step. Repeat runs
against a stable site can complete with zero model calls.

Typical agent loops send a page snapshot to the model before every click.
ferro sends it once per plan.

## Status

Early. The engine compiles, unit tests pass, and the library has been used
against real sites with a real model. The public API in `ferro.go` is small
and may still change before a tagged release. See `docs/plan.md` for the
current work plan and `DESIGN.md` for known sharp edges.

## Install

```sh
go get github.com/dndungu/ferro
```

Requires Go 1.22 or later and a Chrome or Chromium binary on the machine.

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
		BaseURL: "http://localhost:11434/v1", // Ollama, vLLM, OpenAI, OpenRouter, ...
		Model:   "qwen2.5:14b",
	}

	b, err := ferro.NewBrowser(ferro.BrowserConfig{Headless: true})
	if err != nil {
		log.Fatal(err)
	}
	defer b.Close()

	result, metrics, err := ferro.Run(context.Background(), b, client, ferro.Task{
		Goal:     "search for coffee and report the cheapest price",
		StartURL: "https://example-shop.test",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
	fmt.Printf("llm calls: %d, est tokens: %d\n", metrics.LLMCalls, metrics.EstimatedTokens)
}
```

`examples/shop/main.go` is a runnable version that reads the endpoint,
model, and start URL from environment variables:

```sh
FERRO_LLM_BASE_URL=http://localhost:11434/v1 \
FERRO_LLM_MODEL=qwen2.5:14b \
FERRO_START_URL=https://example.com \
go run ./examples/shop
```

## How a run works

1. **Snapshot.** The page compiler walks the DOM and emits a compact,
   numbered list of the visible, interactive elements: `[3] button "Add to
   cart"`. No screenshot, no full HTML. Capped at `MaxElements` (default 200).
2. **Plan.** One model call receives the goal and the snapshot and returns a
   JSON plan: a flat list of steps that reference elements by their `[N]`
   ref. The plan is validated before anything runs.
3. **Execute.** The executor runs each step with no model involvement. It
   resolves refs to live elements, waits for the DOM to settle after clicks
   and navigations, and enforces a hard budget on every wait (default 5s).
4. **Repair.** If a step fails (element gone, selector drifted), the runner
   takes a fresh snapshot and asks the model for a replacement for that one
   step, then resumes from it. Bounded by `WithMaxRepairs` (default 2).
5. **Replan.** If the model emits `plan_again`, or the plan ends without a
   `done` step, the runner re-plans from a fresh snapshot. Bounded by
   `Task.MaxPlannings` (default 3).

### Step vocabulary

| Kind | Fields | Effect |
|---|---|---|
| `goto` | `url` | Navigate and wait for the DOM to settle |
| `click` | `ref` | Click the element |
| `fill` | `ref`, `text`, `secret` | Clear and type into an input; `secret` masks the value in logs |
| `select` | `ref`, `value` | Choose an option by visible text or value |
| `key` | `text` | Send a key sequence such as `Enter` |
| `scroll` | `to` | `top` or `bottom` |
| `wait` | `for` | `dom_settle`, a duration like `2s`, or a CSS selector |
| `extract` | `fields` or `schema` | Pull data: per-field CSS selectors run as pure JS; a schema falls back to one model call to structure page text |
| `plan_again` | `reason` | Ask the runner to re-plan |
| `done` | `result` | Finish with a result |

Plans are capped at 30 steps.

## Running many tasks on one tab

`ferro.Run` acquires a tab from the pool and releases it when the task
ends. For a long-lived session, acquire once and reuse the tab with a
`Runner`:

```go
runner := ferro.NewRunner(client, ferro.WithResolutionCache(".ferro-cache.json"))

tab, err := b.Acquire(ctx)
if err != nil {
	log.Fatal(err)
}
defer tab.Release()

for _, goal := range goals {
	// Empty StartURL runs against whatever page the tab is on.
	result, m, err := runner.Run(ctx, tab, ferro.Task{Goal: goal})
	...
}
```

`Runner.Run` never releases the tab; the caller owns its lifetime. A
dedicated `RunOn` entry point is planned (see `docs/plan.md`, epic E7).

## Staying signed in

Set `BrowserConfig.UserDataDir` to a persistent Chrome profile directory.
Sign in once by hand in a headful session, then run tasks headless against
the same directory. Cookies and sessions survive across runs. ferro never
automates the sign-in itself.

```go
b, err := ferro.NewBrowser(ferro.BrowserConfig{
	Headless:    true,
	UserDataDir: "./chrome-profile",
})
```

## Options and metrics

| Option | Effect |
|---|---|
| `WithMaxRepairs(n)` | Cap single-step repairs per failed action (default 2) |
| `WithResolutionCache(path)` | Persist learned ref-to-selector mappings so repeat runs skip model-driven resolution; empty path keeps it in memory |

`BrowserConfig` also exposes `PoolSize`, `MaxElements`, `AllocateTimeout`,
`ExecPath`, `Proxy`, and `UserAgent`.

Every run returns `RunMetrics`:

| Field | Meaning |
|---|---|
| `LLMCalls` | Total model calls (planning, repair, extract structuring) |
| `Plannings` | Re-plans after the first |
| `Repairs` | Single-step repairs |
| `CacheHits` | Ref resolutions served from the resolution cache |
| `EstimatedTokens` | Rough token count, `len(prompt+response)/4` |
| `Duration` | Wall time |
| `ErrorClass` | Error taxonomy label on failure (`stale_ref`, `timeout`, ...) |

## Model endpoints

`OpenAICompatible` talks to any `/v1/chat/completions` endpoint: OpenAI,
Ollama, vLLM, LM Studio, OpenRouter, and compatible gateways. Set `APIKey`
for hosted providers. Temperature defaults to 0 and `MaxTokens` to 2048,
because a plan is a short JSON document.

Any type with `Complete(ctx, system, user string) (string, error)`
satisfies `ferro.LLMClient`, so other backends need no changes to the
engine.

## Package layout

| Path | Contents |
|---|---|
| `ferro.go` | Public API: `Run`, `Runner`, `Task`, `RunMetrics`, `Option`, `Browser`, `OpenAICompatible` |
| `internal/core/` | Engine: plan types, page compiler, executor, ref resolution, repair, resolution cache |
| `internal/browser/` | chromedp tab pool |
| `internal/llm/` | LLM client implementations |
| `examples/shop/` | Runnable demonstration |
| `testdata/pages/` | Fixture HTML |
| `integration/` | Build-tag-gated real-model suite |
| `docs/plan.md` | Current work plan |
| `docs/adr/` | Architecture decision records |
| `DESIGN.md` | Package split, principles, known sharp edges |
| `conversation.md` | The design conversation and RFC the code grew from |

## Development

```sh
go build ./... && go vet ./...
go test ./...                                  # unit tests
FERRO_TEST_BROWSER=1 go test ./...             # adds browser-backed tests (needs Chrome)
FERRO_MODEL_URL=http://localhost:11434/v1 FERRO_MODEL_NAME=qwen2.5:14b \
  go test -tags=integration ./integration/...  # real-model suite
```

If this checkout sits under a directory with a parent `go.work` file, set
`GOWORK=off`.

Two rules for contributors, both recorded in `docs/adr/`:

- The first context passed to `chromedp.Run` on a tab owns that tab's
  lifetime. Never wrap it in `WithTimeout` or `WithCancel` before the first
  `Run`. See `docs/adr/001-chromedp-context-lifetime.md`.
- The executor contains zero model calls. Model involvement lives only in
  the planner, the repairer, and the extract-structuring fallback.

## Limitations

- Element targeting resolves refs to CSS selectors by signature. Identical
  elements under different headings can collide; the repairer and the
  resolution cache mitigate this but do not eliminate it.
- The snapshot is text only. Canvas-heavy or image-driven pages are out of
  scope for now.
- No anti-bot evasion beyond standard launch flags.
- The pool size governs pre-warming, not a hard cap on live tabs.

## License

MIT. See `LICENSE`.
