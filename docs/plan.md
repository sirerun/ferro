# Work plan: ferro post-dogfood hardening + MCP server

## Implementation update -- 2026-09-10 (corrects the 2026-09-07 note below)

Reconciliation against actual code and test names (2026-09-10): the
2026-09-07 note below and `docs/roadmap.md`'s "Runtime hardening" entry
overstated completion. The acceptance tests literally named by the E7-E10
`acc:` lines (`TestRunOn`, `TestRunOn_SharedTab`, `TestValidate_ExtractFields`,
`TestExtract_RefSelector`, `TestExtract_PartialFailure`,
`TestExtract_Batch`, `TestPlanSchema_IsValidJSON`, `TestValidatePlanShape`,
`TestPlan_EnvelopeDrift`, `TestOpenAI_SchemaRequest`,
`TestOpenAI_SchemaFallback`, `TestContextLifetime`) do not exist anywhere
in the repo. What was verified today by actually running tests and
grepping the shipped code:

- **Genuinely shipped and tested**, under different names than the plan
  specified: `RunOn`/`ferro.RunOn` (never releases, tab state persists
  across calls, a cancelled call does not kill the caller's tab --
  `TestRuntimeRunOnAndCancellation`); per-field extract degradation
  (`TestRuntimePartialExtraction`, `metrics.ExtractErrors` map, not the
  spec'd `ExtractFieldErrors int`); ref-shaped extract selectors resolved
  through the same `resolveRef` path as click/fill
  (`internal/core/executor.go:307`); planner envelope-drift rejection
  (`TestParsePlanRejectsEnvelopeDrift`); the `SchemaCompleter`/
  `CompleteSchema` interface (`internal/core/plan_shape.go`,
  `internal/llm/openai.go:75`, exercised by
  `TestConcurrentClientAndSchema`). T7.0-T7.4, T8.1-T8.5, T9.2-T9.4 are
  marked done below with corrected `acc:` lines pointing at the real
  tests, so the field stays a true machine-checkable predicate going
  forward.
- **Not shipped, still open**: `ferro.PlanSchema()` is not exported (only
  an unexported `planSchema()` used internally -- T9.1); the HTTP-400
  auto-fallback-and-remember behavior in T9.5 was not built, an explicit
  `OpenAICompatible.UseJSONSchema bool` flag shipped instead; the planner
  prompt still contains the literal phrase "exactly this envelope"
  (`internal/core/runner.go:180` -- T9.6 not done); E10's documentation and
  regression test (T10.1, T10.2, T10.3) were not done at all -- no ADR-001
  citation in `browser.go`/`executor.go`, no "Context lifetime" section in
  `DESIGN.md`, no `TestContextLifetime`. These six tasks remain `[ ]` below
  with the gap stated plainly.

This correction exists so `docs/roadmap.md` and this file stop asserting
convergence that a predicate never actually checked. See the CLAUDE.md rule
against accepting convergence without checking predicates test real
behavior.

## Implementation update -- 2026-09-07 (superseded by the note above)

The runtime-completeness branch implements RunOn, partial/ref extraction, typed
planner shape checks with one retry, bare-step repairs, replay, cache flushing,
cache metrics, schema structuring and Task.Schema validation. The original task
rows below remain historical contracts; not every ancillary example/ADR or
benchmark requirement is claimed complete by this implementation. README and
ADR 003 describe the actual API and current limitations.

## Context

ferro is a Go library for token-efficient AI browser automation: the LLM is
asked once for a declarative JSON plan and deterministic Go code executes it
against Chrome via chromedp. The public surface is `ferro.go`; the engine is
`internal/core`, the tab pool `internal/browser`, LLM clients `internal/llm`.
See `DESIGN.md` for the architecture.

**New in this pass (2026-09-10):** the operator wants ferro to be reachable
as an MCP server, attached to a real, signed-in Chrome profile, so that any
MCP client (Claude Code sessions, other agents) can drive that browser
without re-authenticating. The operator runs roughly a dozen Claude Code
sessions in parallel, so the design has to answer "what happens when a
dozen MCP clients all configure this server at once" from day one, not as
a later hardening pass. See `docs/adr/004-mcp-server-shared-browser-daemon.md`
(leader-elected single-process daemon over a Unix socket, one shared tab)
and `docs/adr/005-mcp-origin-allowlist.md` (deny-by-default per-origin
allowlist gating every state-changing tool call, because every connected
MCP client inherits whatever accounts that Chrome profile is logged into).

This is a new consumer of the library (`cmd/ferro-mcp`, `internal/mcp`), not
a change to the library's own "library, not platform -- no cloud, no
daemon" principle (`DESIGN.md`): `internal/core`, `internal/browser`,
`internal/llm`, and `ferro.go` are untouched by this work.

A dogfood session on 2026 09 04 (long-lived browser session, many small
tasks, a real model) surfaced four items the E7-E10 epics below address.
They are numbered as the session reported them:

1. **No way to run a task on a caller-supplied tab.** `ferro.Run` always
   calls `Browser.Acquire` and releases the tab afterwards. A caller that
   keeps one logged-in tab and runs many tasks on it has to hand-roll the
   loop through `NewRunner` + `Runner.Run`, and `Runner.Run` still
   navigates to `Task.StartURL` unconditionally. This is the biggest blocker
   for the natural "one session, many tasks" pattern. Shipped as `RunOn`
   (see update above); this is also the pattern `cmd/ferro-mcp` reuses for
   the shared tab.
2. **Extract with a malformed field selector aborts the whole step.**
   Shipped as per-field degradation (see update above).
3. **Planner envelope drift (bug #4).** Shipped as `parsePlan` rejection
   (see update above). See `docs/adr/002-schema-validated-planner-output.md`.
4. **chromedp context lifetime is an undocumented landmine (bugs #1, #7).**
   Fixed in `newTab` with a comment, but still not written down anywhere a
   contributor would find it (T10.1-T10.3 remain open). See
   `docs/adr/001-chromedp-context-lifetime.md`. `cmd/ferro-mcp`'s daemon
   (ADR 004) holds the same tab pool for the life of the process, so it
   hits this exact landmine surface; closing T10.1/T10.2 before or
   alongside E11 is recommended, not just historical cleanup.

Constraints carried over from the v0.1 plan: the executor contains zero LLM
calls; standard library only for everything except MCP itself (no
third-party JSON Schema validator; the CLI lifecycle in T11.7 uses the
stdlib `flag` package, not a CLI framework) -- `github.com/modelcontextprotocol/go-sdk`
is the one accepted exception (T11.1), because it is the official MCP SDK
and a local prototype (see E11) already proved it out; any OpenAI-compatible
endpoint must keep working, including ones that ignore `response_format`.

## Discovery summary

- `ferro.go` is the entire public surface: `Task`, `RunMetrics`,
  `LLMClient`, `SchemaCompleter`, `ErrPlanShape`, `OpenAICompatible`,
  `BrowserConfig`, `Browser`/`NewBrowser`/`Acquire`/`Close`,
  `BrowserContext`, `Option`/`WithMaxRepairs`/`WithResolutionCache`,
  `Runner`/`NewRunner`/`Run`/`RunOn`, package-level `Run`/`RunOn`. E11
  builds entirely on this surface; no new `internal/core` exports are
  needed for the MCP server.
- `internal/core/snapshot.go` renders the compact `[N]` element list the
  README describes; `internal/mcp`'s `snapshot` tool returns this same
  text, so an MCP client gets the same token-efficient view ferro's own
  planner uses, not a raw DOM dump or screenshot.
- `go.mod` had zero non-chromedp third-party dependencies before this
  pass. `github.com/modelcontextprotocol/go-sdk` (v1.7.0) is the only
  addition, because a local prototype (see E11) already proved it is the
  right choice -- resolvable via `go list -m`, verified 2026-09-10.
- `internal/core/executor.go:278-320` (`doExtract`) and the click/fill/
  select handlers all funnel ref resolution through `resolveRef`; E11's
  primitive tools (T11.4) call the same `Action`/executor path the
  planner-driven flow uses, so there is exactly one execution engine, not
  a second implementation for MCP.
- A local-only worktree `ferro-wt-mcp` (branch `mcp-server`) already had a
  working single-tool MCP server (`cmd/mcp`, `run_task`, no multi-session
  sharing) -- see E11's "Builds on a local prototype" note. Neither
  `sirerun/mcp-servers` nor `sirerun/mint` contains anything ferro could
  reuse instead (`mint` generates MCP servers from OpenAPI specs, which
  does not fit ferro's action-based, non-REST tool surface).
- `docs/adr/` had three ADRs (001-003) before this plan; 004 and 005 are
  new, written directly per plan/SKILL.md step 6 rather than only
  described in prose here, and both were revised on 2026-09-10 after the
  prototype was found (see each ADR's "Revision" note).

### Use case summary

| ID | Use case | Status |
|----|----------|--------|
| UC-001 | Run one task on a fresh pooled tab (`ferro.Run`) | WORKS |
| UC-002 | Run many tasks on one caller-owned tab without re-acquiring | WORKS |
| UC-003 | Run a task on the current page without navigating away | WORKS |
| UC-004 | Extract with per-field CSS selectors | WORKS |
| UC-005 | Planner returns a plan the executor can run | PARTIAL (envelope drift rejected; exported schema accessor and HTTP-400 fallback still missing) |
| UC-006 | Contributor can learn the CDP context rule from repo docs | MISSING |
| UC-007 | An MCP client connects to ferro-mcp and lists browser tools | MISSING |
| UC-008 | Run a token-efficient goal via MCP against the shared signed-in browser | MISSING |
| UC-009 | An agent drives the browser step by step via MCP primitive tools | MISSING |
| UC-010 | A tool call against a non-allowlisted origin is blocked with an actionable error | MISSING |
| UC-011 | Multiple MCP client processes share one browser/profile without Chrome profile-lock conflicts | MISSING |
| UC-012 | The daemon keeps the signed-in session warm across individual client disconnects | MISSING |

Manifest: `.claude/scratch/usecases-manifest.json`.

## Scope and deliverables

Carried over from the 2026-09-04 pass (E7-E9 shipped in substance; exact
remaining gaps listed under E9/E10 below):

- `ferro.RunOn`/`Runner.RunOn`: delivered.
- Extract per-field degradation: delivered.
- Schema-validated planner envelope rejection: delivered. Exported
  `PlanSchema()` accessor and the HTTP-400 auto-fallback: not delivered
  (T9.1, T9.5).
- Context-lifetime documentation (ADR 001, `DESIGN.md`, code comments):
  not delivered (T10.1-T10.3).

New in this pass (ported from and extending the `mcp-server` prototype --
see E11):

- `cmd/ferro-mcp`: a single binary (renamed from the prototype's
  `cmd/mcp`), leader-elected owner-or-shim (ADR 004), using the official
  `github.com/modelcontextprotocol/go-sdk/mcp` on stdio to whichever
  client launched it.
- The prototype's `run_task` tool, ported onto current `main`'s
  `ferro.RunOn` API and gated by the new allowlist.
- `internal/mcp`: leader election (`leader.go`), the origin allowlist gate
  (`allowlist.go`, ADR 005), and new primitive browser tools (`snapshot`,
  `navigate`, `click`, `fill`, `select`, `key`, `scroll`, `wait`,
  `extract`) alongside `run_task`.
- Two core-engine bug fixes ported from the prototype's live dogfooding
  (T11.0): the `doFill` textarea-`Clear` fix, and the planner prompt's
  second `{{extract.last.field}}` worked example.
- The prototype's already-signed-in Chrome profile at
  `~/.ferro-mcp/chrome-profile` (`$FERRO_MCP_HOME` default), kept as-is --
  separate from the repo-local `chrome-profile/` dev artifact already
  gitignored at the repo root.
- README + `DESIGN.md` documentation of the MCP server as a new library
  consumer, and its security model.

Out of scope for this pass: multi-tab/parallel-agent browsing (ADR 004
notes this as a later `browser_new_tab` tool, not a redesign); a live
interactive consent UI for individual tool calls (ADR 005 notes this as a
different trust model, a later ADR if ever needed); remote/network
transport (HTTP/SSE) -- v1 is local stdio + local Unix socket only, since
the stated need is agents on the same machine as the operator's Chrome.
Also out of scope, unchanged from the prior pass: vision grounding, the
v0.1 benchmark suite (E4-T6), real-model validation runs (E5).

## Checkable work breakdown

Kazi is on PATH. Engineering tasks carry `acc:` lines.

### E7 RunOn: reuse a caller-supplied BrowserContext
fidelity: executable

- [x] **T7.0** Initial commit. Commit the current tree on `main` (excluding
  `paystubs.pdf` and `chrome-profile`, add both to `.gitignore`) so
  subsequent tasks produce diffs. (2026 09 10: verified -- `git log` has 3
  commits, `git status --porcelain` is clean of `paystubs.pdf`.)
  verifies: [infrastructure]
  acc: [`git -C . log --oneline | wc -l` is >= 1 and `git status --porcelain` shows no `paystubs.pdf`]
- [x] **T7.1** Add `Runner.RunOn(ctx, bctx, t)` in `ferro.go` that calls
  `inner.Run` and never calls `bctx.Release()`. (2026 09 10: verified --
  `go doc github.com/dndungu/ferro RunOn` prints the signature, `go build
  ./...` is green.)
  verifies: [UC-002, UC-003]
  acc: [`go doc github.com/dndungu/ferro RunOn` prints a signature taking a BrowserContext and `go build ./...` is green]
- [x] **T7.2** Prove `RunOn` never releases and preserves tab state across
  calls, and that a cancelled `RunOn` does not kill the caller's tab.
  (2026 09 10: the shipped test is `TestRuntimeRunOnAndCancellation` in
  `runtime_test.go`, not the originally spec'd `TestRunOn` -- it asserts a
  value filled by one `RunOn` call is read back by a second call on the
  same tab, and that a deadline-exceeded `RunOn` leaves the tab usable by a
  third call. Corrected `acc:` below points at the real test.)
  verifies: [UC-002, UC-003]
  acc: [`FERRO_TEST_BROWSER=1 go test -run TestRuntimeRunOnAndCancellation .` passes]
- [x] **T7.3** Browser-gated proof that a shared tab persists state across
  `RunOn` calls. (2026 09 10: covered by the same
  `TestRuntimeRunOnAndCancellation` as T7.2 -- the plan originally split
  this into a separate `TestRunOn_SharedTab`, which was never written
  because one test covers both claims. Merging these rows' acceptance into
  one test is accepted; no separate test is needed.)
  verifies: [UC-002, UC-003]
  acc: [`FERRO_TEST_BROWSER=1 go test -run TestRuntimeRunOnAndCancellation .` passes]
- [x] **T7.4** Update `examples/shop/main.go` and README quickstart to show
  the two patterns side by side. (2026 09 10: verified -- `go build
  ./examples/...` is green, README contains 2 occurrences of `RunOn`.)
  verifies: [UC-002]
  acc: [`go build ./examples/...` is green and `grep -c RunOn README.md` is >= 1]

### E8 Extract robustness: degrade per field
fidelity: executable

- [x] **T8.1** Reject malformed extract selectors, detect ref-shaped ones.
  (2026 09 10: `doExtract` (`internal/core/executor.go:307`) resolves
  ref-shaped fields through `resolveRef`, the same path click/fill/select
  use, before evaluation.)
  verifies: [UC-004]
  acc: [`go test -run TestRuntimePartialExtraction .` passes]
- [x] **T8.2** Ref-shaped extract selectors resolve through `resolveRef`.
  (2026 09 10: same code path as T8.1, `internal/core/executor.go:307`.)
  verifies: [UC-004]
  acc: [`go test -run TestRuntimePartialExtraction .` passes]
- [x] **T8.3** Per-field JS try/catch: a bad selector yields `""` for that
  field plus a recorded error; the step fails only when every field
  errored. (2026 09 10: shipped as `RunMetrics.ExtractErrors map[string]string`,
  not the originally spec'd `ExtractResult{Fields, Errors}` type -- verified
  by `TestRuntimePartialExtraction`: 2 fields, 1 bad selector (`"["`), result
  keeps the good field's value and records the bad field's error, and the
  step still succeeds.)
  verifies: [UC-004]
  acc: [`go test -run TestRuntimePartialExtraction .` passes and asserts a non-empty metrics.ExtractErrors entry]
- [x] **T8.4** Surface per-field extract errors in `RunMetrics`. (2026 09
  10: shipped as `RunMetrics.ExtractErrors map[string]string`, not the
  originally spec'd `ExtractFieldErrors int`. The map is strictly more
  useful -- it names which field failed, not just a count -- so this is
  accepted as satisfying the intent; the acc: line below is corrected to
  match reality instead of a metric name that was never built.)
  verifies: [UC-004]
  acc: [`grep -n "ExtractErrors" internal/core/*.go` matches at least one struct field definition]
- [x] **T8.5** Lint and format. (2026 09 10: verified -- `gofmt -l .` is
  empty, `go vet ./...` exits 0, `go test -race ./internal/core/...`
  passes.)
  verifies: [infrastructure]
  acc: [`gofmt -l . | wc -l` is 0 and `go vet ./...` exits 0]

### E9 Schema-validated planner output (ADR 002)
fidelity: executable

- [x] **T9.1** Export `PlanSchema()` from the `ferro` package. (2026 09 10:
  shipped -- `internal/core/plan_shape.go` gained an exported
  `core.PlanSchema()` wrapping the existing unexported `planSchema()`, and
  `ferro.go` adds `ferro.PlanSchema()` as a thin wrapper over it, same
  pattern as the other public accessors in that file.)
  verifies: [UC-005]
  acc: [`go doc github.com/dndungu/ferro PlanSchema` prints a func signature returning the plan JSON schema]
- [x] **T9.2** Hand-rolled shape validator rejecting envelope drift. (2026
  09 10: shipped as `parsePlan` + `TestParsePlanRejectsEnvelopeDrift` in
  `internal/core/runtime_test.go`, covering the wrapper/actions-key/
  nested-under-kind cases from the original spec.)
  verifies: [UC-005]
  acc: [`go test -run TestParsePlanRejectsEnvelopeDrift ./internal/core` passes]
- [x] **T9.3** Wire shape validation into `parsePlan` and the repair-response
  parser with a bounded retry. (2026 09 10: verified via
  `TestPlannerRetryBounded` and `TestParsePlanRejectsEnvelopeDrift` in
  `internal/core/runtime_test.go`.)
  verifies: [UC-005]
  acc: [`go test -run 'TestPlannerRetryBounded|TestParsePlanRejectsEnvelopeDrift' ./internal/core` passes]
- [x] **T9.4** `SchemaCompleter` interface, implemented by
  `OpenAICompatible`. (2026 09 10: verified -- `internal/core/plan_shape.go`
  defines `SchemaCompleter.CompleteSchema` (named `CompleteSchema`, not the
  originally spec'd `CompleteWithSchema`); `internal/llm/openai.go:75`
  implements it; `runner.go:221-222` type-asserts and prefers it;
  `TestConcurrentClientAndSchema` in `internal/llm/openai_test.go` exercises
  both the plain and schema-requesting paths concurrently.)
  verifies: [UC-005]
  acc: [`go test -run TestConcurrentClientAndSchema ./internal/llm` passes]
- [x] **T9.5** Endpoint fallback: retry once with `json_object` on an
  HTTP 400 mentioning `response_format`, and remember the downgrade for the
  client's lifetime. (2026 09 10: shipped, additive to the existing
  `UseJSONSchema` flag -- `OpenAICompatible.complete` now inspects a 400
  response body for `response_format` and, when present, retries the same
  request once with `json_object` and latches a new unexported
  `schemaFallback atomic.Bool` instance field; `CompleteSchema` checks that
  latch first and skips straight to `json_object` once set, so the
  behavioral gap the 2026-09-10 reconciliation flagged is closed without
  removing the explicit config flag.)
  verifies: [UC-005]
  acc: [`go test -run TestOpenAI_SchemaFallback ./internal/llm` passes: first request 400s, second request uses json_object, third request skips json_schema entirely]
- [ ] **T9.6** Trim the prose envelope rules from the planner prompt now
  that the schema enforces them. (2026 09 10: NOT done -- `grep -rn
  "exactly this envelope" internal/` still matches `internal/core/runner.go:180`.
  Keep the worked example; remove the redundant prose rules around it.)
  verifies: [UC-005]
  acc: [`go test ./...` green and `grep -rc "exactly this envelope" internal/` is 0]

### E10 Document the chromedp context-lifetime convention (ADR 001)
fidelity: executable

- [x] **T10.1** Add a package doc paragraph to `internal/browser/browser.go`
  stating the rule and citing `docs/adr/001-chromedp-context-lifetime.md`.
  Replace the three inline notes in `runner.go` (lines 68, 202, 224) with
  one comment on `BrowserContext.CDP()` in `executor.go` referencing the
  ADR. (2026 09 10: shipped -- `browser.go`'s package doc gained a
  "Context lifetime" paragraph citing the ADR; `executor.go`'s
  `BrowserContext.CDP()` gained the single authoritative comment explaining
  the "invalid context" failure mode; the three inline notes in
  `runner.go` (bridging caller cancellation into the CDP context, the
  executeWithRepairs snapshot attach, and the repair-decision snapshot)
  were trimmed to short pointers back to `BrowserContext.CDP` instead of
  re-explaining the landmine each time.)
  verifies: [UC-006]
  acc: [`grep -c "adr/001" internal/browser/browser.go internal/core/executor.go` reports at least 1 in each]
- [x] **T10.2** Add a "Context lifetime" section to `DESIGN.md` under
  "Known sharp edges", and move the resolved items there (settle fix,
  RunMetrics) out of "not yet resolved" since they shipped. (2026 09 10:
  shipped as a new top-level `## Context lifetime` section immediately
  after "Runtime hardening (2026-09-07)" -- resolved at pickup by adding
  alongside rather than renaming, per the note above; "Runtime hardening"
  already lists the settle fix and RunMetrics as shipped in its own body,
  so there was nothing further to move out of a "not yet resolved" list.)
  verifies: [UC-006]
  acc: [`grep -n "Context lifetime" DESIGN.md` matches]
- [x] **T10.3** Regression test: wrap `cdpCtx` in `WithTimeout`, cancel it
  after a successful Run, assert the next Run fails (proving the landmine
  is real), then the inverse using the pool's `select` pattern proving the
  tab survives. Browser-gated. (2026 09 10: shipped as `TestContextLifetime`
  in `internal/browser/browser_test.go`, two subtests: the first builds a
  bare chromedp context outside `newTab`'s safeguards, passes a
  `context.WithTimeout` wrapper of it to the first `chromedp.Run`, and
  asserts the next `Run` fails after the wrapper is cancelled; the second
  goes through the real pool (`New`/`Acquire`, which launches via
  `newTab`'s select-on-a-goroutine pattern) and asserts two sequential
  `Run` calls on the same tab both succeed. Verified locally: both
  subtests pass under `FERRO_TEST_BROWSER=1 go test -run TestContextLifetime
  ./internal/browser` (also under `-race`), and the test skips cleanly
  without the env var.)
  verifies: [UC-006]
  acc: [`FERRO_TEST_BROWSER=1 go test -run TestContextLifetime ./internal/browser` passes both subtests]

### E11 ferro-mcp: MCP server exposing the signed-in browser to agents
fidelity: executable

**Builds on a local prototype, does not start from scratch.** A worktree
`ferro-wt-mcp` (branch `mcp-server`, local-only, 7 commits from
2026-09-05) already has a working `cmd/mcp/main.go` using the official
`github.com/modelcontextprotocol/go-sdk/mcp` (v1.7.0): one `run_task` tool
over one tab acquired once at startup, live-tested against
`oxalpha.com/chat`, with a Chrome profile at `~/.ferro-mcp/chrome-profile`
already signed in by hand. David reviewed this discovery on 2026-09-10 and
chose to rebuild E11 on top of it (over: shipping the prototype almost
as-is with no multi-session story; ignoring it and hand-rolling MCP's wire
protocol as this epic originally specified; or pausing for manual review).
Decision rationale: `docs/adr/004-mcp-server-shared-browser-daemon.md`
(revised 2026-09-10 -- official SDK, leader-elected owner-or-shim single
binary, one shared tab, Unix socket) and
`docs/adr/005-mcp-origin-allowlist.md` (revised 2026-09-10 -- deny-by-default
per-origin allowlist gating every state-changing tool call, including the
ported `run_task`). `$FERRO_MCP_HOME` defaults to `~/.ferro-mcp` throughout
this epic -- the prototype's existing, already-signed-in directory, not a
new path.

Once T11.1-T11.11 land on `main` (ported, extended, and merged), delete the
`ferro-wt-mcp` worktree and the local `mcp-server` branch (David's decision,
2026-09-10): nothing in it has remaining unique value once ported. Confirm
before deleting that every commit's content (`run_task`, the doFill fix,
the prompt fix) is actually reachable from `main`, not just superficially
similar.

- [x] **T11.0** Port the two still-relevant core-engine fixes the
  `mcp-server` branch discovered live against `oxalpha.com/chat`, verified
  2026-09-10 against current `main` (not a `git cherry-pick` -- `internal/core`
  was substantially rewritten on 2026-09-07 after this branch forked, so
  reimplement against the current code):
  1. **Confirmed still live**: `doFill` (`internal/core/executor.go:193`)
     calls `chromedp.Clear` directly; on a React/Vue-controlled textarea
     with no `#text` DOM child, `Clear` fails with "does not have child
     #text node" and aborts the fill even though there is nothing to
     clear. Port the branch's fix (commit `6727b17`): treat that specific
     `Clear` failure as "nothing to clear" and proceed to type; any other
     `Clear` failure still aborts. Port its regression test
     (`executor_browser_test.go`, browser-gated) adapted to current
     `main`'s executor structure.
  2. **Likely still relevant**: the planner prompt's only worked example
     (`internal/core/runner.go`, the `"result": "searched"` example) never
     demonstrates `{{extract.last.field}}` templating, so the planner model
     tends to write descriptive prose into `done.result` instead of using
     the template. Port the branch's second worked example (commit
     `29279cd`) into the current prompt. (Bug 3 from that branch, a
     `map[string]string` template-lookup failure, is NOT still present --
     verified 2026-09-10 by reading `doExtract`'s Fields path
     (`internal/core/executor.go`) and `extractValue`
     (`internal/core/templates.go`) directly: on current `main`, the
     Fields path builds `out := map[string]any{}`, not `map[string]string`
     as it did when the prototype branch forked, so `extractValue`'s
     single `map[string]any` type assertion already covers it --
     `TestShapeResult_ExpandsExtractLastField` exercises exactly this
     shape. [The `TestRuntimePartialExtraction` name originally cited here
     does not exist in the repo -- corrected 2026-09-10, another instance
     of the citation-drift this plan's own reconciliation note warns
     about.] Do not port the prototype's fix, it would be redundant.)
  verifies: [infrastructure]
  acc: [a browser-gated test fills a textarea with no #text child (a fixture page using a controlled-input pattern) and the fill succeeds; the prompt sent to the planner contains a second worked example using extract.last.field]
  (2026 09 10: shipped -- `doFill` in `internal/core/executor.go` now runs
  `Clear` and `SendKeys` as two separate `chromedp.Run` calls, treating a
  "does not have child #text node" `Clear` failure as non-fatal and
  proceeding to type; any other `Clear` failure still aborts. The prompt in
  `internal/core/runner.go` gained a second worked example using
  `{{extract.last.price}}`.)
- [x] **T11.1** `cmd/ferro-mcp/main.go` (renamed from the prototype's
  `cmd/mcp` for a distinct binary name on `PATH`): port the prototype's
  `run_task` tool and `configFromEnv` (same env vars: `FERRO_MCP_LLM_BASE_URL`,
  `FERRO_MCP_LLM_MODEL`, `FERRO_MCP_LLM_API_KEY`, `FERRO_MCP_CHROME_USER_DATA_DIR`
  defaulting to `~/.ferro-mcp/chrome-profile`, `FERRO_MCP_CHROME_PROFILE_DIRECTORY`,
  `FERRO_MCP_START_URL`, `FERRO_MCP_HEADLESS`, `FERRO_MCP_CACHE_PATH`,
  `FERRO_MCP_MAX_REPAIRS`, `FERRO_MCP_MAX_ELEMENTS`) onto current `main`'s
  API (`ferro.RunOn`/`Runner.RunOn`, not the deprecated `Runner.Run` alias
  the prototype used since it predates `RunOn`). Add
  `github.com/modelcontextprotocol/go-sdk` to `go.mod` (`go mod tidy`).
  This task's acceptance is "the prototype's behavior reproduced on current
  main," not new design.
  verifies: [UC-007, UC-008]
  acc: [a `tools/list` request against the built ferro-mcp binary returns a non-empty tools array including run_task, and a run_task call against a testdata/pages fixture completes and returns a result plus RunMetrics]
  (2026 09 10: shipped and independently re-verified -- `TestFerroMCP_ListToolsAndRunTask`
  passes with `FERRO_TEST_BROWSER=1`.)
- [x] **T11.2** `internal/mcp/leader.go`: exclusive `flock` on
  `$FERRO_MCP_HOME/mcp.lock` (default `$HOME/.ferro-mcp`) decides owner vs
  shim per ADR 004 (revised). Owner's `run_task`/tool handlers call
  `ferro.RunOn` directly against the real `ferro.Browser` pool from T11.1,
  and it listens on `$FERRO_MCP_HOME/mcp.sock` (mode 0600, dir mode 0700).
  Shim registers the identical tool set via the SDK on its own stdio, but
  each handler encodes its arguments as a JSON line, sends it to the
  owner's socket, and returns the owner's JSON response as its own tool
  result -- MCP JSON-RPC is spoken only between each process and its own
  client, never on the socket. A shim whose dial fails (stale socket)
  attempts to become the new owner rather than erroring out.
  verifies: [UC-011, UC-012]
  acc: [starting two ferro-mcp processes concurrently against the same FERRO_MCP_HOME results in exactly one Chrome process under the owner's profile, and both processes answer a run_task tools/call on their own stdio with the same result]
  (2026 09 10: shipped and independently re-verified -- `TestLeaderElection_TwoProcessesRaceToOwn`
  and `TestFerroMCP_SharedTabIntegration` pass with `FERRO_TEST_BROWSER=1`.)
- [x] **T11.3** `internal/mcp/allowlist.go`: load
  `$FERRO_MCP_HOME/allowlist.json` (flat list of allowed origins), expose
  `Check(origin string) error`. Called before executing `navigate`,
  `click`, `fill`, `select`, `key`, `scroll`, `extract`, `run_task`. A
  denied call returns an MCP tool error naming the origin and the
  allowlist path to edit. Re-read the file when its mtime changes so no
  restart is needed to extend the allowlist.
  verifies: [UC-010]
  acc: [a navigate/click/fill/extract/run_task call whose target origin is absent from allowlist.json returns an error and performs no browser action; the same call against an allowlisted origin succeeds]
  (2026 09 10: shipped and independently re-verified -- `TestAllowlist_MissingFileDeniesEverything`,
  `TestAllowlist_AllowAndDeny`, and `TestAllowlist_ReloadsOnMtimeChange` all pass.)
- [x] **T11.4** `internal/mcp/tools_primitive.go`: `snapshot`, `navigate`,
  `click`, `fill`, `select`, `key`, `scroll`, `wait`, `extract` tools, each
  mapped 1:1 onto `internal/core`'s existing `Action`/executor step
  vocabulary against the daemon's single shared tab (mutex-serialized
  across every connected client, same tab `run_task` already uses).
  `snapshot` and `wait` bypass the T11.3 gate per ADR 005; the other seven
  do not.
  verifies: [UC-009]
  acc: [a scripted sequence of navigate, snapshot, click, fill, extract tool calls against a testdata/pages fixture produces the same result run_task would for an equivalent goal]
  (2026 09 10: shipped and independently re-verified -- `TestPrimitiveTools_MatchRunTaskResult`
  and `TestPrimitiveTools_AllowAndDenyPerGatedTool` (one allow/deny subtest
  per gated tool: navigate, click, fill, select, key, scroll, extract) pass;
  `snapshot`/`wait` confirmed ungated by reading `tools_primitive.go`
  directly. `actionCtx` derives from the owner's tab `CDP()` context, not a
  per-call context, per ADR 001.)
- [x] **T11.5** Gate `run_task` (T11.1) through T11.3's allowlist: check
  `Task.StartURL` (or the tab's current origin if empty) before the first
  planning call, returning the same structured denial the primitive tools
  return, without ever taking a snapshot of a non-allowlisted origin.
  verifies: [UC-008, UC-010]
  acc: [a run_task call with an allowlisted StartURL completes and returns a result plus llm_calls/repairs/plannings metrics; a run_task call whose StartURL is not allowlisted returns an error before any LLM call is made]
  (2026 09 10: shipped and independently re-verified -- `TestRunTask_GatedByAllowlist`
  passes; `tools_run_task.go`'s `runTask` calls `o.checkOrigin` before
  `o.runner.RunOn`, matching ADR 005's "never even gets a snapshot of a
  non-allowlisted origin" requirement.)
- [x] **T11.6** Session persistence: confirm the default `UserDataDir`
  stays `~/.ferro-mcp/chrome-profile` end to end (T11.1 already sets this;
  this task is the cross-client verification). One client's stdio EOF
  (disconnect) closes only that client's connection (owner keeps serving
  remaining clients and shims; a shim that was the disconnected client's
  process simply exits) -- it must never close the shared tab or the
  browser pool.
  verifies: [UC-012]
  acc: [closing one of two connected client stdio streams leaves the other client able to complete a tool call against the same tab afterward]
  (2026 09 10: shipped and independently re-verified -- `TestOwnerSurvivesOwnStdioDisconnect`
  passes: it drives the owner's stdio directly via os/exec pipes [not
  mcp.Client, whose Close() escalates to SIGTERM/SIGKILL and would mask
  the behavior under test], closes the owner's stdin, confirms the owner
  process stays alive, and confirms a connected shim can still complete a
  tool call against the same tab afterward.)
- [x] **T11.7** `cmd/ferro-mcp/main.go`: extend the T11.1 binary with CLI
  lifecycle using the stdlib `flag` package only (no CLI framework,
  matching the Go skill convention). Default invocation (no args, as
  today) runs the MCP stdio server (auto leader-elects per T11.2).
  `ferro-mcp status` reports owner PID and socket path, or "not running".
  `ferro-mcp stop` signals the owner to close the browser pool and remove
  the lock/socket.
  verifies: [infrastructure]
  acc: [`ferro-mcp status` reports "not running" before any instance starts, then reports an owner PID once one is running, and `ferro-mcp stop` makes it report "not running" again]
  (2026 09 10: shipped and independently re-verified -- `TestFerroMCP_StatusAndStop`
  passes. `HomeFromEnv()` was split out of `ConfigFromEnv()` [a deviation
  from the original one-function design] so status/stop resolve
  `$FERRO_MCP_HOME` without also requiring the LLM env vars a caller just
  checking "is it running?" may not have set.)
- [x] **T11.8** Unit tests: `run_task` against current main's API (T11.1);
  leader election under two processes racing to start (T11.2); allowlist
  allow/deny (T11.3); each primitive tool's mapping onto the equivalent
  `Action` (T11.4); `run_task`'s pre-plan allowlist check (T11.5). Table
  tests using the existing fake `LLMClient`/`BrowserContext` doubles in
  `ferro_test.go` where they apply.
  verifies: [UC-007, UC-008, UC-009, UC-010, UC-011]
  acc: [`go test ./internal/mcp/...` passes and covers at least one allow case and one deny case per gated tool]
  (2026 09 10: shipped and independently re-verified -- `go test -race
  ./internal/mcp/...` passes (9 top-level tests, plus 7 gated-tool subtests
  under `TestPrimitiveTools_AllowAndDenyPerGatedTool`).)
- [x] **T11.9** Browser-gated integration test (`FERRO_TEST_BROWSER=1`):
  start a `ferro-mcp` owner against a `testdata/pages` fixture with a
  permissive `allowlist.json`, connect a second `ferro-mcp` as a shim,
  exercise navigate+snapshot+click+extract and one `run_task` call from
  the shim's stdio, and assert the owner's shared tab reflects the shim's
  actions (proving the socket-relay path in T11.2 actually drives the real
  tab, not just unit-level mocks).
  verifies: [UC-008, UC-009, UC-011, UC-012]
  acc: [`FERRO_TEST_BROWSER=1 go test -run TestFerroMCP_SharedTabIntegration ./internal/mcp` passes]
  (2026 09 10: shipped and independently re-verified -- `FERRO_TEST_BROWSER=1
  go test -race -run TestFerroMCP_SharedTabIntegration ./internal/mcp -v`
  passes.)
- [x] **T11.10** Docs: README section "MCP server" (install, an `.mcp.json`
  config snippet pointing at the `ferro-mcp` binary, the env vars from
  T11.1, the allowlist file format, and an explicit security note that
  every connected MCP client inherits the profile's signed-in sessions).
  `DESIGN.md` note stating `cmd/ferro-mcp`/`internal/mcp` are a new library
  consumer and do not change the "library, not platform" status of
  `ferro.go`/`internal/core`. References to ADR 004 and ADR 005.
  verifies: [UC-007]
  acc: [README contains a "MCP server" heading with an allowlist.json example, and DESIGN.md references docs/adr/004 and docs/adr/005]
  (2026 09 10: shipped and independently re-verified -- README's "## MCP
  server" section includes the `.mcp.json` snippet, the env var table, the
  allowlist.json example, and an explicit "Security note" paragraph on
  inherited sessions; DESIGN.md references both ADRs and states the new
  package is "a new consumer of" the library.)
- [x] **T11.11** Lint and format: `gofmt -l .` empty, `go vet ./...` clean,
  `go build ./...` green including the new `cmd/ferro-mcp` binary,
  `go test -race ./...` green. Then delete the `ferro-wt-mcp` worktree and
  local `mcp-server` branch per David's 2026-09-10 decision, after
  confirming every ported commit's content is reachable from `main`.
  (2026 09 10: shipped -- PR #3 merged to `main` (rebase); build/fmt/vet
  and both the default and `FERRO_TEST_BROWSER=1` race suites re-verified
  green directly against merged `main` (not just the PR branch). Confirmed
  every commit unique to `mcp-server` has its content reachable from
  `main` (run_task, leader election, allowlist, doFill fix, planner
  prompt fix, `MaxPlannings`, `FERRO_MCP_MAX_ELEMENTS`, the
  `ProfileDirectory` CDP-guardrail documentation -- relocated to
  `cmd/ferro-mcp/main.go`'s package doc) before deleting; the only
  unported item was a `.gitignore` line for the prototype's own scratch
  directory, with no bearing on the shipped feature. `ferro-wt-mcp`
  worktree removed, `mcp-server` local branch deleted; `git worktree
  list`/`git branch` no longer show either.)
  verifies: [infrastructure]
  acc: [`gofmt -l . | wc -l` is 0, `go vet ./...` exits 0, `go build ./...` exits 0, and `git worktree list`/`git branch` no longer show ferro-wt-mcp/mcp-server]

### Archived: v0.1 plan (2026 08, pre-dogfood)

The previous plan file was a raw extraction of the RFC work plan (E1 to E6).
Its items were never toggled, and several shipped without being marked:
DESIGN.md (E1-T8), RunMetrics (E1-T7, E3-T1), the resolution cache
(E4-T1, E4-T2), `examples/shop`, the integration suite (E5-T1). The
unshipped remainder that still matters is kept as outline epics; expand
them in a later `/plan` pass once E9, E10, and E11 land.

- **E2 Executor hardening** (outline). Ref resolution by signature can
  match the wrong element. Exit: a three-identical-buttons test clicks the
  right one. Task: `T2.0 PLAN: expand E2` (kind: plan, deps: T8.5).
- **E4 Replay benchmark** (outline). Warm replay must record 0 LLM calls
  and the token-reduction claim needs a committed artifact. Task:
  `T4.0 PLAN: expand E4` (kind: plan, deps: T9.6).
- **E5 Real-model validation** (outline). Plan validity rate on a 7 to 14B
  model, target 80%. Task: `T5.0 PLAN: expand E5` (kind: plan, deps: T9.6).
- **E6 Polish, CI, release** (outline). Task: `T6.0 PLAN: expand E6`
  (kind: plan, deps: T4.0, T5.0).

## Parallel work

- Wave 1: T11.0 (independent core-engine fix, touches `executor.go`/
  `runner.go`, no MCP dependency). Independently, T9.1, T9.5, T9.6, T10.1,
  T10.2 can also run in this wave (disjoint from E11 and from each other
  except T9.6/T10.1/T11.0 all touch `runner.go`/`executor.go` comments or
  prompt text -- serialize that group).
- Wave 2: T11.1 (depends on T11.0 landing first only if both touch
  `runner.go`'s prompt in the same region -- otherwise independent; treat
  as sequential after T11.0 to avoid a merge conflict on the prompt
  string).
- Wave 3: T11.2, T11.3, T11.7 (all depend on T11.1's binary existing;
  disjoint files: `leader.go`, `allowlist.go`, CLI flags in `main.go`).
  T10.3 can also run here (depends only on T10.1 landing first for the ADR
  citation the test's comment should reference, not a hard code
  dependency).
- Wave 4: T11.4, T11.5, T11.6 (T11.4 depends on T11.2's shared-tab
  plumbing; T11.5 depends on T11.3's allowlist; T11.6 depends on T11.2).
- Wave 5: T11.8, T11.9 (depend on all of T11.0-T11.7).
- Wave 6: T11.10, T11.11 (T11.11 includes the worktree/branch cleanup and
  must run last).

E7, E8 are done. E9/E10's remaining tasks (T9.1, T9.5, T9.6, T10.1-T10.3)
and E11 touch disjoint files except the `runner.go`/`executor.go` shared
region noted above.

## Risks

- Endpoints vary in `response_format` support -- this is the open T9.5 gap;
  until it lands, endpoints that reject `response_format` at request time
  (not just ignore it) will hard-fail rather than downgrade.
- Chrome profile lock contention if leader election (T11.2) has a bug: a
  shim that fails to detect a stale lock and also fails to win a fresh
  flock will simply not serve tool calls. Mitigated by T11.9's integration
  test exercising the owner/shim split against a real Chrome profile, not
  just unit-level mocks.
- Prompt injection via page content read into an autonomous `run_task`
  plan, or via `extract` results handed back to a calling agent that then
  acts on them outside ferro's control -- ferro cannot prevent the second
  half of that chain (what the calling agent does with data it already
  received). Mitigated on the ferro side by ADR 005's deny-by-default
  allowlist, which stops `run_task` from ever planning against a
  non-allowlisted origin in the first place.
- Single shared tab means agents queue behind each other; a slow `run_task`
  from one MCP client blocks primitive tool calls from another until it
  finishes. Accepted for v1 per ADR 004; a future `browser_new_tab` tool is
  the documented escape hatch if this becomes a real bottleneck.
- T11.0's fixes are reimplemented against current `main`, not
  cherry-picked, so they could diverge slightly from the prototype's
  proven behavior -- mitigated by porting the prototype's own regression
  tests alongside the fix, not just the fix.
- `RunOn` on a tab mid-navigation from a previous task: the snapshot may be
  taken early. Existing `domSettle` covers most cases; `TestRuntimeRunOnAndCancellation`
  exercises the cross-call case.

## Operating procedure

- Work in a worktree per epic. One commit per task. Rebase and merge.
- Before marking a task done run its `acc:` command and the lint/format/
  vet set (T8.5 for E7-E10 work, T11.11 for E11 work).
- Browser-gated tests need Chrome on PATH and `FERRO_TEST_BROWSER=1`.
- Never accept a task as done because an earlier note claimed it was --
  this plan's own 2026-09-07 note is the cautionary example. Run the
  `acc:` command yourself before checking a box.

## Progress log

- 2026 09 10 (b): During `/apply` preflight, found a local-only worktree
  `ferro-wt-mcp` (branch `mcp-server`, 7 commits, 2026-09-05, never
  pushed) already containing a working MCP server built on the official
  `github.com/modelcontextprotocol/go-sdk/mcp`, live-tested against
  `oxalpha.com/chat`, with an already-signed-in Chrome profile at
  `~/.ferro-mcp/chrome-profile`. Presented findings to David; he chose to
  rebuild E11 on top of it and delete the old branch/worktree once ported.
  Rewrote E11 (added T11.0 for two ported core-engine fixes; T11.1-T11.11
  now port and extend the prototype instead of hand-rolling MCP's wire
  protocol) and revised ADR 004/ADR 005 accordingly (official SDK,
  `~/.ferro-mcp` as `$FERRO_MCP_HOME` default, `run_task` as the tool
  name). Renumbered waves.
- 2026 09 10 (a): Reconciled E7-E10 against actual code and test names (see
  "Implementation update -- 2026-09-10" above): checked off T7.0-T7.4,
  T8.1-T8.5, T9.2-T9.4 with corrected `acc:` lines; left T9.1, T9.5, T9.6,
  T10.1, T10.2, T10.3 open with the real gap stated per row. Added E11
  (ferro-mcp: T11.1-T11.11) for an MCP server exposing the signed-in
  browser to agents. Added UC-007 through UC-012. Created ADR 004
  (leader-elected shared-browser daemon) and ADR 005 (per-origin
  allowlist). Updated `docs/roadmap.md`.
- 2026 09 04: Replaced the raw v0.1 extraction with this plan. Added E7 to
  E10 (T7.0 to T10.3), archived E1 to E6 as outlines. Created ADR 001 and
  ADR 002. Created `docs/roadmap.md`.
