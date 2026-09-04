# Work plan: ferro post-dogfood hardening

## Context

ferro is a Go library for token-efficient AI browser automation: the LLM is
asked once for a declarative JSON plan and deterministic Go code executes it
against Chrome via chromedp. The public surface is `ferro.go`; the engine is
`internal/core`, the tab pool `internal/browser`, LLM clients `internal/llm`.
See `DESIGN.md` for the architecture.

A dogfood session on 2026 09 04 (long-lived browser session, many small
tasks, a real model) surfaced four items this plan addresses. They are
numbered as the session reported them:

1. **No way to run a task on a caller-supplied tab.** `ferro.Run` always
   calls `Browser.Acquire` and releases the tab afterwards. A caller that
   keeps one logged-in tab and runs many tasks on it has to hand-roll the
   loop through `NewRunner` + `Runner.Run`, and `Runner.Run` still
   navigates to `Task.StartURL` unconditionally. This is the biggest blocker
   for the natural "one session, many tasks" pattern.
2. **Extract with a malformed field selector aborts the whole step.** A
   selector like `"[2]"` (a ref, not CSS) makes `document.querySelector`
   throw; `doExtract` returns an error for the entire extract. Observed
   failure rate 4/5 in a synthetic batch. Extract is a common action, so
   one bad field should degrade, not abort.
3. **Planner envelope drift (bug #4).** The model sometimes returns
   `{"plan": {...}}`, `{"actions": [...]}`, or steps nested under the kind
   name. `parsePlan` accepts it, `Validate` says "plan has no steps", and
   the real cause is hidden. See `docs/adr/002-schema-validated-planner-output.md`.
4. **chromedp context lifetime is an undocumented landmine (bugs #1, #7).**
   Wrapping the tab context before the first `chromedp.Run` kills the tab
   when the wrapper is cancelled. Fixed in `newTab` with a comment, but the
   rule is not written down where the next person will find it. See
   `docs/adr/001-chromedp-context-lifetime.md`.

Constraints carried over from the v0.1 plan: the executor contains zero LLM
calls; standard library only (no third-party JSON Schema validator);
any OpenAI-compatible endpoint must keep working, including ones that
ignore `response_format`.

Repo state: no commits yet on `main`, no origin remote. The first task
creates the initial commit so later work lands as reviewable diffs.

## Discovery summary

- `ferro.go:129` `Run` acquires and releases; `Runner.Run` at
  `internal/core/runner.go:53` navigates to `StartURL` when set. No path
  reuses a caller's `BrowserContext` without also owning its release.
- `internal/core/executor.go:278` `doExtract` iterates `a.Fields`, one
  `chromedp.Evaluate` per field, and returns on the first error.
- `internal/core/plan.go:99` `validateAction` checks `Fields` is non-empty
  but never validates the selector strings.
- `internal/core/runner.go:117` prompt describes the envelope in prose;
  `internal/llm/openai.go` supports `response_format` type `json_object`
  only.
- `internal/browser/browser.go:100` carries the context-lifetime comment;
  `runner.go:68,202,224` carry three copies of the "invalid context" note.
- `docs/adr/` did not exist before this plan.

### Use case summary

| ID | Use case | Status |
|----|----------|--------|
| UC-001 | Run one task on a fresh pooled tab (`ferro.Run`) | WORKS |
| UC-002 | Run many tasks on one caller-owned tab without re-acquiring | MISSING |
| UC-003 | Run a task on the current page without navigating away | MISSING |
| UC-004 | Extract with per-field CSS selectors | BROKEN (one bad selector aborts) |
| UC-005 | Planner returns a plan the executor can run | BROKEN (envelope drift is silent) |
| UC-006 | Contributor can learn the CDP context rule from repo docs | MISSING |

Manifest: `.claude/scratch/usecases-manifest.json`.

## Scope and deliverables

- `ferro.RunOn(ctx, bctx, client, task, opts...)` and a matching
  `Runner.RunOn`; `Task.StartURL` is honored only when set, and a new
  `Task.NoNavigate` is unnecessary because empty `StartURL` already skips
  navigation. The delivered change is: `RunOn` never calls `Release`.
- Extract degrades per field: a bad selector yields an empty string plus a
  recorded per-field error; the step succeeds if at least one field
  resolved. A ref-shaped selector (`[N]`) is repaired to the ref's
  resolved selector before evaluation.
- Planner and repair responses validated against a fixed schema with a
  typed error; structured output requested from endpoints that support it.
- Context-lifetime convention documented in ADR 001, `DESIGN.md`, and a
  package comment; duplicate inline notes collapsed to one reference.

Out of scope: vision grounding, the v0.1 benchmark suite (E4-T6), real-model
validation runs (E5). Those remain in the archived v0.1 plan below.

## Checkable work breakdown

Kazi is on PATH. Engineering tasks carry `acc:` lines.

### E7 RunOn: reuse a caller-supplied BrowserContext
fidelity: executable

- [ ] **T7.0** Initial commit. Commit the current tree on `main` (excluding
  `paystubs.pdf` and `chrome-profile`, add both to `.gitignore`) so
  subsequent tasks produce diffs.
  verifies: [infrastructure]
  acc: [`git -C . log --oneline | wc -l` is >= 1 and `git status --porcelain` shows no `paystubs.pdf`]
- [ ] **T7.1** Add `Runner.RunOn(ctx, bctx, t)` in `ferro.go` that calls
  `inner.Run` and never calls `bctx.Release()`. Rename nothing; keep
  `Runner.Run` as an alias documented as "same as RunOn" for one release,
  since it already does not release. Add package-level `ferro.RunOn(ctx,
  bctx, client, t, opts...)`. Document in godoc that the caller owns the
  tab's lifetime and that an empty `StartURL` runs against the current
  page.
  verifies: [UC-002, UC-003]
  acc: [`go doc github.com/dndungu/ferro RunOn` prints a signature taking a BrowserContext and `go build ./...` is green]
- [ ] **T7.2** Unit test with a fake `BrowserContext` counting `Release`
  and `Navigate` calls: `RunOn` with empty `StartURL` makes 0 Navigate and
  0 Release calls; with `StartURL` set makes 1 Navigate and 0 Release;
  `ferro.Run` makes exactly 1 Release. Uses the existing fake LLM in
  `ferro_test.go`.
  verifies: [UC-002, UC-003]
  acc: [`go test -run 'TestRunOn' .` passes and asserts Release count 0 for RunOn]
- [ ] **T7.3** Browser-gated test (`FERRO_TEST_BROWSER=1`): acquire one tab,
  run three tasks via `RunOn` against `testdata/pages` fixtures, assert the
  tab's `document.title` persists between tasks and that a fourth task with
  empty `StartURL` sees the previous task's page.
  verifies: [UC-002, UC-003]
  acc: [`FERRO_TEST_BROWSER=1 go test -run TestRunOn_SharedTab .` passes]
- [ ] **T7.4** Update `examples/shop/main.go` and README quickstart to show
  the two patterns side by side: `ferro.Run` for one-shot, `Acquire` +
  `RunOn` loop for a session. Run `gofmt -l` and `go vet ./...`.
  verifies: [UC-002]
  acc: [`go build ./examples/...` is green and README contains a `RunOn` code block]

### E8 Extract robustness: degrade per field
fidelity: executable

- [ ] **T8.1** In `validateAction` for `KindExtract`, reject empty selector
  strings and detect ref-shaped selectors (`^\[\d+\]$`); leave them in place
  but flag nothing at validation time, since refs are legal input to the
  repair in T8.2. Add a table test for empty, ref-shaped, and normal
  selectors.
  verifies: [UC-004]
  acc: [`go test -run TestValidate_ExtractFields ./internal/core` passes]
- [ ] **T8.2** In `doExtract`, before evaluating a field, if the selector is
  ref-shaped resolve it through `resolveRef` (same path click and fill use)
  and substitute the resolved CSS. Record the outcome via `recordOutcome`
  so the cache learns it.
  verifies: [UC-004]
  acc: [`go test -run TestExtract_RefSelector ./internal/core` passes with a fake snapshot mapping [2] to a real selector]
- [ ] **T8.3** Evaluate each field inside a JS try/catch that returns
  `{ok, value, error}`. On a per-field failure store `""` for the field and
  append the field name and JS error to a new `Action`-independent result
  type `ExtractResult{Fields map[string]string; Errors map[string]string}`.
  The step fails only when every field errored. Keep the return value of
  `doExtract` a `map[string]string` for template compatibility and expose
  errors through `extracted["last_errors"]`.
  verifies: [UC-004]
  acc: [`go test -run TestExtract_PartialFailure ./internal/core` passes: 3 fields, 1 bad selector, result has 2 values and 1 error, step succeeds]
- [ ] **T8.4** Surface per-field extract errors in `RunMetrics` as
  `ExtractFieldErrors int` and in debug logging. Regression batch: a
  browser-gated test that runs the synthetic 5-case batch from the dogfood
  session and asserts 5/5 succeed.
  verifies: [UC-004]
  acc: [`FERRO_TEST_BROWSER=1 go test -run TestExtract_Batch .` reports 5 passing subtests]
- [ ] **T8.5** Lint and format: `gofmt -l .` empty, `go vet ./...` clean,
  `go test -race ./...` green.
  verifies: [infrastructure]
  acc: [`gofmt -l . | wc -l` is 0 and `go vet ./...` exits 0]

### E9 Schema-validated planner output (ADR 002)
fidelity: executable

- [ ] **T9.1** Define `planSchema` as a Go constant string (JSON Schema
  draft 2020-12, `additionalProperties: false`, `steps` required, each step
  a flat object with `kind` enum and the per-kind fields) in a new
  `internal/core/schema.go`. Export a `PlanSchema()` accessor from `ferro`.
  verifies: [UC-005]
  acc: [`go test -run TestPlanSchema_IsValidJSON ./internal/core` passes]
- [ ] **T9.2** Write a hand-rolled validator `validatePlanShape(raw
  []byte) error` for exactly this schema (top-level object, only `steps`
  and optional `reasoning`, `steps` is a non-empty array of objects whose
  keys are in the allowed set and whose `kind` is in the enum). Returns
  `*ErrPlanShape{Path, Reason}`. No third-party dependency.
  verifies: [UC-005]
  acc: [`go test -run TestValidatePlanShape ./internal/core` passes with table cases: nested-under-kind, plan-wrapper, actions-key, valid]
- [ ] **T9.3** Wire it into `parsePlan` and the repair-response parser:
  shape validation runs before `json.Unmarshal`. On `ErrPlanShape`, the
  runner retries planning once with the reason appended to the user
  message, increments `RunMetrics.ShapeRetries`, and fails with the typed
  error if the retry also fails.
  verifies: [UC-005]
  acc: [`go test -run TestPlan_EnvelopeDrift ./internal/core` passes: fake LLM returns `{"plan":{...}}` then a valid plan; run succeeds with ShapeRetries == 1]
- [ ] **T9.4** Add `SchemaCompleter` interface in `internal/core`
  (`CompleteWithSchema(ctx, system, user string, schema []byte) (string,
  error)`). `OpenAICompatible` implements it by sending
  `response_format: {"type":"json_schema","json_schema":{"name":"plan",
  "strict":true,"schema":...}}`. The runner type-asserts and prefers it;
  plain `Complete` remains the fallback.
  verifies: [UC-005]
  acc: [`go test -run TestOpenAI_SchemaRequest ./internal/llm` passes: httptest server sees response_format.type == "json_schema"]
- [ ] **T9.5** Endpoint fallback: if the endpoint returns HTTP 400 mentioning
  `response_format`, `OpenAICompatible` retries once with `json_object` and
  remembers the downgrade for the client's lifetime.
  verifies: [UC-005]
  acc: [`go test -run TestOpenAI_SchemaFallback ./internal/llm` passes: first 400, second request has type json_object, third request skips json_schema]
- [ ] **T9.6** Trim the prose envelope rules from the planner prompt that
  the schema now enforces; keep the worked example. Re-run the fake-LLM
  suite and the `-tags=integration` suite if `FERRO_MODEL_URL` is set.
  verifies: [UC-005]
  acc: [`go test ./...` green and the prompt no longer contains the phrase "exactly this envelope"]

### E10 Document the chromedp context-lifetime convention (ADR 001)
fidelity: executable

- [ ] **T10.1** Add a package doc paragraph to `internal/browser/browser.go`
  stating the rule and citing `docs/adr/001-chromedp-context-lifetime.md`.
  Replace the three inline notes in `runner.go` (lines 68, 202, 224) with
  one comment on `BrowserContext.CDP()` in `executor.go` referencing the
  ADR.
  verifies: [UC-006]
  acc: [`grep -c "adr/001" internal/browser/browser.go internal/core/executor.go` reports at least 1 in each]
- [ ] **T10.2** Add a "Context lifetime" section to `DESIGN.md` under
  "Known sharp edges", and move the resolved items there (settle fix,
  RunMetrics) out of "not yet resolved" since they shipped.
  verifies: [UC-006]
  acc: [`grep -n "Context lifetime" DESIGN.md` matches]
- [ ] **T10.3** Regression test: a test that wraps `cdpCtx` in
  `WithTimeout`, cancels it after a successful Run, and asserts the next
  Run fails, proving the landmine is real; then the inverse using the
  pool's `select` pattern proving the tab survives. Browser-gated.
  verifies: [UC-006]
  acc: [`FERRO_TEST_BROWSER=1 go test -run TestContextLifetime ./internal/browser` passes both subtests]

### Archived: v0.1 plan (2026 08, pre-dogfood)

The previous plan file was a raw extraction of the RFC work plan (E1 to E6).
Its items were never toggled, and several shipped without being marked:
DESIGN.md (E1-T8), RunMetrics (E1-T7, E3-T1), the resolution cache
(E4-T1, E4-T2), `examples/shop`, the integration suite (E5-T1). The
unshipped remainder that still matters is kept as outline epics; expand
them in a later `/plan` pass once E7 to E10 land.

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

- Wave 1: T7.0.
- Wave 2 (independent after T7.0): T7.1, T8.1, T9.1, T10.1.
- Wave 3: T7.2, T8.2, T9.2, T10.2.
- Wave 4: T7.3, T8.3, T9.3, T9.4, T10.3.
- Wave 5: T7.4, T8.4, T9.5.
- Wave 6: T8.5, T9.6.

E7, E8, E9, E10 touch disjoint files except `ferro.go` (T7.1, T9.1
accessor) and `runner.go` (T9.3, T10.1). Serialize those pairs.

## Risks

- Endpoints vary in `response_format` support. Mitigated by T9.5 fallback
  and the local validator in T9.2, which runs regardless.
- Per-field extract degradation could mask bad plans. Mitigated by the
  `ExtractFieldErrors` metric (T8.4) and all-fields-failed still erroring.
- `RunOn` on a tab mid-navigation from a previous task: the snapshot may be
  taken early. Existing `domSettle` covers most cases; T7.3 asserts it.

## Operating procedure

- Work in a worktree per epic. One commit per task. Rebase and merge.
- Before marking a task done run its `acc:` command and the E8.5 lint set.
- Browser-gated tests need Chrome on PATH and `FERRO_TEST_BROWSER=1`.

## Progress log

- 2026 09 04: Replaced the raw v0.1 extraction with this plan. Added E7 to
  E10 (T7.0 to T10.3), archived E1 to E6 as outlines. Created ADR 001 and
  ADR 002. Created `docs/roadmap.md`.
