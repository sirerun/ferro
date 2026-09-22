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

Requires Go 1.25 or later and a Chrome or Chromium binary on the machine.

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
	result, m, err := runner.RunOn(ctx, tab, ferro.Task{Goal: goal})
	...
}
```

`Runner.RunOn` and `ferro.RunOn` never release the tab; the caller owns its
lifetime. `Runner.Run` remains an equivalent alias.

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
| `WithResolutionCache(path)` | Persist learned selector mappings and successful plans between runners; empty path keeps it in memory |

`BrowserConfig` also exposes `PoolSize`, `MaxElements`, `AllocateTimeout`,
`ExecPath`, `Proxy`, and `UserAgent`.

Every run returns `RunMetrics`:

| Field | Meaning |
|---|---|
| `LLMCalls` | Total model calls (planning, repair, extract structuring) |
| `Plannings` | Re-plans after the first |
| `Repairs` | Single-step repairs |
| `CacheHits` | Ref resolutions served from the resolution cache |
| `ReplayHits` | Plans served from the plan cache |
| `PlannerRetries` | Retries after malformed planner output (at most one per planning call) |
| `CacheErrors` | Nonfatal cache read/write errors |
| `ExtractErrors` | Per-field extraction failures |
| `EstimatedTokens` | Rough token count, `len(prompt+response)/4` |
| `Duration` | Wall time |
| `ErrorClass` | Error taxonomy label on failure (`stale_ref`, `timeout`, ...) |

## Replay and extraction

Set `Task.ReplayKey` to opt into replay. A reused `Runner` caches in memory;
`WithResolutionCache(path)` retains plans and selectors between runners and
process restarts. The key also includes the goal, starting URL, result schema,
and initial snapshot. Changed refs or page structure cause a fresh planning
call. Only successful, unrepaired plans are saved; secret-fill plans are never
cached. A failed replay is invalidated and repaired in place without restarting
already-executed actions. A warm plan containing schema extraction still needs
its extraction model call; pure-selector plans can run with zero model calls.
Use extraction templates for changing data rather than caching a literal answer.

Each run flushes the versioned cache atomically, on success or failure. Cache
files use mode 0600. Corrupt/obsolete caches start empty and report a warning
in `CacheErrors`; write failures also appear there without undoing a successful
task. Share one Runner across goroutines, but give each process its own cache
file. Concurrent independent cache objects writing the same file are unsupported.

CSS extraction resolves `[N]` selectors as snapshot refs. Missing elements or
invalid selectors produce an empty field and `ExtractErrors`; the step fails
only if every field fails. `{{extract.last.field}}` works in subsequent actions
and nested `done.result` values. A whole-result `{{extract.last}}` preserves the
object/array type. Missing templates return errors.

Schema extraction pauses the deterministic executor, asks the model to structure
the page text, validates the response, then resumes at the next step. `Task.Schema`
also validates the final result. Supported JSON Schema keywords: `type` (single
object/array/string/number/integer/boolean/null), `properties`, `required`,
`additionalProperties` (boolean), `items` (single schema), `enum`, `minimum`,
`maximum`, `minLength`, `maxLength`, `minItems`, `maxItems`, and annotation strings
`$schema`, `title`, `description`. Unsupported keywords fail explicitly.

Planner envelopes and repair actions are validated locally. Envelope drift,
unknown fields, and fields belonging to the wrong action kind produce
`*ferro.ErrPlanShape`. The planner gets one bounded correction attempt.
`OpenAICompatible.UseJSONSchema` optionally sends a JSON Schema with `strict:false`
to capable endpoints (the action vocabulary allows arbitrary result objects).
The default remains `json_object` for compatible local endpoints. Custom clients
can implement `ferro.SchemaCompleter`; local checks apply either way.

`WithMaxRepairs(0)` disables repairs. Each action has a five-second default
budget, and caller cancellation interrupts a run without releasing its tab.
Runner and OpenAICompatible configurations must not be mutated during use.

## Model endpoints

`OpenAICompatible` talks to any `/v1/chat/completions` endpoint: OpenAI,
Ollama, vLLM, LM Studio, OpenRouter, and compatible gateways. Set `APIKey`
for hosted providers. Temperature defaults to 0 and `MaxTokens` to 2048,
because a plan is a short JSON document.

Any type with `Complete(ctx, system, user string) (string, error)`
satisfies `ferro.LLMClient`, so other backends need no changes to the
engine.

## MCP server

`cmd/ferro-mcp` is a [Model Context Protocol](https://modelcontextprotocol.io)
server that lets an MCP client (Claude Code, or any other MCP-speaking
agent) drive ferro's browser engine against one persistent, already
signed-in Chrome profile. It's a new consumer of the library
(`ferro.go`/`internal/core`), not a change to it — see DESIGN.md.

Install the binary:

```sh
go install github.com/dndungu/ferro/cmd/ferro-mcp@latest
```

Point an MCP client at it. For Claude Code, add to `.mcp.json`:

```json
{
  "mcpServers": {
    "ferro": {
      "command": "ferro-mcp",
      "env": {
        "FERRO_MCP_LLM_BASE_URL": "http://localhost:11434/v1",
        "FERRO_MCP_LLM_MODEL": "qwen2.5:14b"
      }
    }
  }
}
```

Any number of MCP clients (one per Claude Code session, for example) can
point at the same `$FERRO_MCP_HOME` at once: the first to start becomes the
owner and is the only one that opens Chrome; the rest become shims that
relay tool calls to it over a Unix socket, so every connected client drives
the same shared tab. See
`docs/adr/004-mcp-server-shared-browser-daemon.md`.

Environment variables:

| Variable | Meaning |
|---|---|
| `FERRO_MCP_HOME` | Where the lock file, relay socket, allowlist, and (by default) the Chrome profile live. Default `~/.ferro-mcp` |
| `FERRO_MCP_LLM_BASE_URL` | Optional for direct tools; required for `run_task`. An OpenAI-compatible `/v1` endpoint |
| `FERRO_MCP_LLM_MODEL` | Required with the LLM endpoint; otherwise omit both |
| `FERRO_MCP_LLM_API_KEY` | API key, if the endpoint needs one |
| `FERRO_MCP_CHROME_USER_DATA_DIR` | Chrome profile directory. Default `$FERRO_MCP_HOME/chrome-profile` |
| `FERRO_MCP_CHROME_PROFILE_DIRECTORY` | Chrome's `--profile-directory` value, for a user data dir holding more than one profile |
| `FERRO_MCP_START_URL` | Optional initial navigation when the owner starts |
| `FERRO_MCP_HEADLESS` | `true` to run headless (default `false`) |
| `FERRO_MCP_CACHE_PATH` | Resolution/replay cache path (see Replay and extraction, above) |
| `FERRO_MCP_MAX_REPAIRS` | Per-task repair budget (default 2) |
| `FERRO_MCP_MAX_ELEMENTS` | Snapshot element cap |

Tools: `run_task` runs an autonomous goal end to end (the MCP equivalent of
`Runner.RunOn`). The rest map 1:1 onto the engine's action vocabulary:
`snapshot`, `navigate`, `click`, `fill`, `select`, `key`, `scroll`, `wait`,
`extract`.

Page reads (including `snapshot`), actions, DOM waits and every step inside
`run_task` are gated by a deny-by-default per-origin allowlist at
`$FERRO_MCP_HOME/allowlist.json` — a flat JSON array of allowed origins
(scheme + host + port):

```json
[
  "https://example-shop.test",
  "https://mail.example.com"
]
```

A call against a page whose origin isn't listed fails with an error naming
the blocked origin and the file to edit. No restart needed — the file is
re-read on every check; deletion or malformed edits revoke access. See
`docs/adr/005-mcp-origin-allowlist.md`.

```sh
ferro-mcp status   # "not running", or the owner's PID and socket path
ferro-mcp stop     # ask the owner to close the browser pool and exit
```

**Security note:** every MCP client connected to the same `$FERRO_MCP_HOME`
shares one Chrome profile and whatever it's signed into. A client that can
call `extract` or `run_task` against an allowlisted origin can read and act
on that origin using the profile's live sessions — only allowlist origins
you're willing to expose to every client you connect, and treat
`$FERRO_MCP_HOME` as sensitive: it holds a live, authenticated browser
profile, not just configuration.

## Package layout

| Path | Contents |
|---|---|
| `ferro.go` | Public API: `Run`, `Runner`, `Task`, `RunMetrics`, `Option`, `Browser`, `OpenAICompatible` |
| `internal/core/` | Engine: plan types, page compiler, executor, ref resolution, repair, resolution cache |
| `internal/browser/` | chromedp tab pool |
| `internal/llm/` | LLM client implementations |
| `cmd/ferro-mcp/` | MCP server binary (see MCP server, above) |
| `internal/mcp/` | Leader election, origin allowlist, and tool relay behind `cmd/ferro-mcp` |
| `examples/shop/` | Runnable demonstration |
| `testdata/pages/` | Fixture HTML |
| `integration/` | Build-tag-gated real-model suite |
| `docs/plan.md` | Current work plan |
| `docs/adr/` | Architecture decision records |
| `DESIGN.md` | Package split, principles, known sharp edges |
| `conversation.md` | The design conversation and RFC the code grew from |

## Your existing Chrome session

The `extension` backend works inside a tab in your normal Chrome profile, using
its existing login. It does not launch Chrome or copy cookies. The default `cdp`
backend still launches a dedicated automation profile.

Build this checkout and start the local service:

```sh
go build -o .claude/scratch/ferro-mcp ./cmd/ferro-mcp
FERRO_MCP_BACKEND=extension .claude/scratch/ferro-mcp serve
```

The default state directory is `~/.ferro-mcp`. Create `allowlist.json` there
containing the exact origins you want to authorize, for example:

```json
["https://gemini.google.com"]
```

Open `chrome://extensions` in the Chrome profile you use, enable Developer mode,
and choose **Load unpacked**, selecting this checkout's `extension/` directory.
Reload the website tab if it was open before installing the extension. Open the
Ferro extension popup on that tab, enter `http://127.0.0.1:4173` and the token from
`~/.ferro-mcp/bridge-token`, and choose **Connect this tab**. The service writes
that token to a private file; it never prints it. Keep Chrome and the tab open.
Disconnect before pairing a different tab. Chrome restart requires pairing again.

Configure a local MCP client to run the same binary with
`FERRO_MCP_BACKEND=extension` and the same `FERRO_MCP_HOME`. It will relay to the
running service. A minimal stdio configuration is:

```json
{
  "mcpServers": {
    "ferro": {
      "command": "/absolute/path/to/ferro-mcp",
      "env": {"FERRO_MCP_BACKEND": "extension"}
    }
  }
}
```

An agent calls `browser_status`, then `acquire_tab`, `snapshot`, and whichever
actions it needs, followed by `release_tab`. Call `acquire_tab` again to renew
(default 300 seconds, maximum 900). Refs belong to the latest snapshot; take a
new one after navigation. Another MCP session cannot act while your lease is
active. `run_task` can reserve the tab for its whole call without an explicit
lease, but needs `FERRO_MCP_LLM_BASE_URL` and `FERRO_MCP_LLM_MODEL`. Set
`FERRO_MCP_LLM_API_KEY` if the selected model endpoint needs it. Page snapshots
and extracted text used by `run_task` are sent to that model endpoint.

`cancel_task` cancels the calling session's active task. It does not undo actions
already performed. Results with `status` equal to `outcome_uncertain` must be
inspected before retrying. `blocked` and `login_required` require human
intervention; retry after resolving the page. `disconnected` means no live
extension is paired. `tab_busy` means another session holds the lease. `pairing_changed` means the
selected tab changed; take a fresh snapshot before continuing.

## Remote/DGX access

Keep the service on the machine running Chrome. Both machines must be connected
to your Tailscale network, and the `tailscale` CLI must be on the service's PATH:

```sh
FERRO_MCP_BACKEND=extension FERRO_MCP_REMOTE=true \
  .claude/scratch/ferro-mcp serve
```

Connect the remote agent's MCP client using Streamable HTTP at
`http://<browser-machine-tailscale-ip>:4174/mcp`, with an
`Authorization: Bearer <contents-of-remote-token>` header. The separate token is
stored at `$FERRO_MCP_HOME/remote-token` with mode 0600. Configure it through your
client's secret storage. It is not the extension pairing token. Remote clients
must retain the MCP session across acquire/action/release calls.

| Setting | Default / meaning |
|---|---|
| `FERRO_MCP_BACKEND` | `cdp` or `extension` |
| `FERRO_MCP_BRIDGE_ADDR` | `127.0.0.1:4173`; loopback IP only |
| `FERRO_MCP_REMOTE` | `false`; enable the private remote MCP listener |
| `FERRO_MCP_REMOTE_PORT` | `4174` |
| `FERRO_MCP_BIND_HOST` | Optional; must equal the verified local Tailscale IPv4 address |
| `FERRO_MCP_BLOCK_TIMEOUT` | `15m`; overall call deadline, maximum `1h`; blocked pages return immediately |

If Tailscale is unavailable, automatic remote binding is skipped and local
service operation continues. An unverifiable explicit bind fails startup. The
service never listens for remote MCP on a wildcard, public or LAN address.
Tailscale provides network encryption; the bearer token authorizes the agent.

Use a **10-second connection timeout** in remote clients. If the browser machine
is asleep or offline, connection refusal/timeout is a transport failure, distinct
from a live MCP `blocked` result. `browser_status` reports pairing and active-call
state without disclosing page content or tokens. `ferro-mcp status` and
`ferro-mcp stop` control the local owner.

**Security note:** this backend acts in your real logged-in sessions. The origin
allowlist, explicit tab pairing and bearer token limit access, but do not isolate
agents into different accounts. Install only on the Chrome profile you intend to
expose, and allow only the sites those agents should use. Human use of the paired
tab can still change its page while automation runs.

Current scope is one paired top-level tab. File transfer, popup/multi-tab flows,
cross-origin frames and canvas-based interaction are not implemented. See
[ADR 006](docs/adr/006-extension-execution-backend.md),
[ADR 007](docs/adr/007-tailscale-remote-transport.md), and
[ADR 008](docs/adr/008-browser-service-sessions.md).


## Development

```sh
go build ./... && go vet ./...
go test ./...                                  # unit tests
FERRO_TEST_BROWSER=1 go test ./...             # browser tests, including the real unpacked extension
FERRO_TEST_BROWSER=1 node --test extension/*.test.cjs # extension snapshot parity and protocol tests
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

For unpacked-extension tests, use a recent Chrome with the browser-target `Extensions.loadUnpacked` debugging API (`CHROME_PATH` overrides discovery). The harness enables extension debugging only in a disposable profile. Tests use local fixture sites.
