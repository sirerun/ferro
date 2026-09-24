# Parallel execution runbook: cheap-model implementation lanes

Cross-track authority: [shared execution roadmap](../../execution-roadmap.md). Its shared-file reservations and total lane limit apply before dispatch in either track.

This runbook makes the parent [plan](../../plan-bulk-browser-execution.md) executable by Luna or cheaper Codex sessions. It does not launch sessions. The coordinator must complete the frontier gates before handing bounded work to implementation lanes. Where the parent plan uses broad B-rows, this directory supplies the executable decomposition; the gate and file-ownership rules here take precedence for dispatch.

## 1. Assignment matrix

| Packet | Tier | Prerequisite | Parent coverage | Parallel window |
|---|---|---|---|---|
| G00 | Frontier coordinator | none | B00 | serial baseline |
| G01 | Frontier architect | G00 | B01 | serial contract freeze |
| L01 | Luna/cheap Codex | G01 | B02 | wave A |
| L02 | Luna/cheap Codex | G01 | B03 | wave A |
| L03 | Luna/cheap Codex | G01 | B04 | wave A |
| L04 | Luna/cheap Codex | G01 | B05 | wave A |
| L05 | Luna/cheap Codex | G01 | B06 schema | wave A |
| L06 | Luna/cheap Codex | G01 | B06 replay | wave A |
| L07 | Luna/cheap Codex | G01 | B07 receipts | wave A |
| L08 | Luna/cheap Codex | G01 | B07 results | wave A |
| L09 | Luna/cheap Codex | G01 | B10 fixtures | wave A |
| L10 | Luna/cheap Codex | G01 | B11 documentation | wave A |
| G02 | Frontier integrator | L01–L08 reviewed | B08; remaining B02–B07 wiring | serial integration |
| G03 | Frontier reviewer | G02 + L09/L10 | B10 executor fixture qualification only | after integrated revision |
| G04 | Frontier Zatiti owner | G01; execution after G03 | B09 | discovery during wave A |
| G05 | Frontier release/pilot owner | G03 + G04 | B11/B12 | serial installed/live gates |

Start with three coding lanes at once, not all ten. Recommended groups: L01/L02/L03, then L04/L05/L07, then L06/L08/L09, with L10 whenever capacity is free. These are resource-limited groups, not additional logical dependencies. Honor the actual harness concurrency limit. Cheap sessions never orchestrate more sessions.

G01, G02 and G04 require architectural judgment and remain frontier work. Labeling them cheap-model tasks would merely transfer unresolved design into retries. After G01, all ten L packets have fixed inputs, exclusive files and concrete output checks.

## 2. G00: coordinator baseline checklist

1. Read repository instructions, parent plan, ajent.social, current branch/status, worktree list and any saved-work branch/archive revisions. Record the current revision; do not assume e6cc219 is still current.
2. Read existing files and installed manifest without displaying secrets. Produce `docs/evidence/bulk-v2/baseline.md` with source hashes, installed differences and reconciliation disposition.
3. Create an isolated integration worktree/branch. Copy or commit only this plan's documentation into its baseline if it is not yet committed; never include unrelated changes.
4. Assign a unique prefix such as `bulk-v2-<date>`. Record coordinator identity and scope in a dispatch ledger. Preserve the user's branches and running service.
5. Do not start L lanes until G01's contract commit and tests exist. The prior document is a design brief, not a substitute for compilable interfaces.

Gate: baseline report and integration revision recorded; no unresolved unknown installed patch is silently discarded. Installed reconciliation may remain pending until G05, with a named disposition.

## 3. G01: freeze compilable contracts before cheap dispatch

Owner: frontier architect. Output: `docs/contracts/bulk-v2.md`, `docs/contracts/bulk-v2.lock.json`, request/result/profile JSON fixtures, a concrete ADR, and real compiling types/helpers. Use existing package structure; no third-party dependency for this gate. Contract-only declarations are acceptable; production methods returning fake success or not-implemented stubs are not.

File placement to settle and implement in this gate:

- `internal/core/task_contract_v2.go`: limit, reservation, receipt metadata, validation and execution option types shared by core and provider consumers. It must not import MCP or provider implementation packages.
- `internal/mcp/task_contract_v2.go`: wire request/result types, enums, profile/store contracts and ownership identity. It may depend on core. Do not create a core→mcp cycle.
- `internal/llm/usage_contract_v2.go`: optional metadata completion interface using shared core metadata only if it does not produce a cycle; otherwise move shared metadata to the existing lowest common package through an explicitly reviewed design. Verify the actual import graph before freezing.
- `docs/contracts/bulk-v2/*.json`: valid/invalid request, success/failure result, model profile, receipt and usage fixtures.
- Contract tests in matching `_test.go` files proving field names, defaults, omissions, schema constraints, enum values and backward compatibility. Supply a runnable JSON-example checker with no paid/network dependency.

The lock must contain: schema version; source base revision; path and SHA-256 of each frozen contract file; exact public/internal exported signatures consumed by each L packet; allowed import direction; test commands; definition of valid/invalid examples. The coordinator records CONTRACT_SHA separately in the dispatch ledger, avoiding a self-referential commit hash.

Freeze all of these, with explicit values rather than “choose appropriate”:

1. Exact JSON field names and optionality; represent missing numeric provider usage distinctly from zero.
2. Exact Go symbols/signatures for resolver, credential lookup, usage completion, admission/reservation/reconciliation, policy wrapper, schema preflight, replay identity, receipt store and result builder. Each L lane must be able to compile against them without sibling lane commits. Use narrow consuming interfaces and injected test dependencies.
3. Profile storage and revision format, atomic update semantics, credential reference behavior and legacy mapping. No paid default model selected by a cheap lane.
4. Stable typed error/status/retry mapping, including failed versus uncertain transmission and lost-return receipts. Freeze authenticated lookup by owner-scoped task_id when execution_id was never received; atomic same-key/same-digest admission, changed-input conflict, concurrent duplicate behavior, recovery retention/tombstones and expiry behavior are required.
5. Counters: count provider network attempts, all planning passes including initial pass, all repair/extraction attempts; define action units exactly. Recommended action unit: each admitted non-internal PageDriver operation; internal Settle does not count twice when part of another operation. Instrumentation must not recursively count its own origin-check snapshot.
6. Hard count/runtime limits versus estimated token limits. Use the parent pilot defaults; request policy may tighten defaults but never exceed service maxima. Hard-dollar mode remains unavailable unless enforceable upper bounds are supplied; the initial implementation must explicitly refuse unsupported hard-dollar requests.
7. Bounds: goal 16 KiB, schema 32 KiB/depth 16, provider body 2 MiB/error body 8 KiB, summary 2 KiB, inline result 16 KiB, receipt/artifact 4 MiB each, retrieval chunk at most 64 KiB. Specify aggregate storage cap and retention before L07 starts; initial recommendation 256 MiB with explicit operator cleanup, preserving active/unreconciled items. Fail closed at capacity rather than evicting those items.
8. Reuse the existing JSON Schema subset; freeze the exact allowed keywords by inspecting `internal/core/schema.go`. No remote refs, schema engine replacement or network resolution.
9. Origin canonicalization using existing helpers; cache partition canonical encoding and digest algorithm (SHA-256), including principal/account, policy/schema and compatibility identity.
10. Read-only operation table across every PageDriver method, lease/pairing capture/recheck boundary, trusted principal derivation and receipt access ownership. Never accept a caller's self-asserted owner ID as authorization. Separate stable authenticated receipt ownership from ephemeral lease/cancellation identity; prove a fresh authorized client can recover its receipt after restart.
11. Existing public LLMClient and legacy run_task remain source/wire compatible. The metadata path issues one HTTP request, not one request per interface.
12. Provider fixture mapping based on reviewed official documentation. Record provider-document date/links; no expensive live call needed to freeze deterministic parsing behavior.

Acceptance: contract packages compile and contract tests pass; all ten packets reference actual defined signatures; a dependency/import check finds no cycle; each worker's scope can be implemented without editing another worker's files. Gate failure blocks dispatch. A frontier reviewer reviews this lock before implementation assignments.

## 4. Session launch and branch protocol

The coordinator creates worktrees from the same accepted contract baseline and supplies absolute paths. Do not have cheap sessions guess paths or select branches. Example command structure (placeholders must be replaced by the coordinator before execution):

```text
git worktree add -b <unique-prefix>-l01 <new-empty-worktree-path> <BASE_SHA>
```

BASE_SHA must contain CONTRACT_SHA. Use one branch/worktree per packet, not one shared checkout. Never use reset --hard, clean, forced removal or automatic conflict resolution. Never modify the user's installed application or live tab during implementation tests.

The coordinator, not workers, is the sole writer of `docs/evidence/bulk-v2/dispatch.json`. Record for each packet: assignee/session, worktree, branch, base_sha, contract_sha, start time, status, commit_sha, tests, blocker and next action. Use states: queued, running, blocked, ready-for-review, accepted. Accepted code is not installed/live qualification.

## 5. Paste-ready cheap-session prompt

Replace every angle-bracket placeholder. Do not dispatch an unfilled prompt.

```text
Implement packet <L-ID> only.
Repo worktree: <ABSOLUTE_WORKTREE>
Branch: <BRANCH>
BASE_SHA: <SHA>
CONTRACT_SHA: <SHA>
Read docs/tasks/bulk-browser/RUNBOOK.md, docs/tasks/bulk-browser/<L-ID>.md,
and docs/contracts/bulk-v2.md plus its lock first.
Confirm the branch and contract file hashes match before editing.
Use the actual frozen signatures. Do not design new interfaces or choose dependencies.
Edit only the packet's allowlisted paths. Use local fixtures and test doubles in tests.
No paid API calls, real browser/account actions, config changes, dependency updates,
merges, pushes, deployments, or additional sessions.
If you need a change outside your allowlist or the contract is insufficient,
return a blocked handoff identifying the symbol, expected behavior and proposed
smallest contract correction. Do not work around it with a stub.
Implement the ordered steps, run the packet checks and full owning-package tests (L10 runs documentation checks only),
then git diff --check. Follow the shared build lease rules.
Commit only your files on this branch. Return the handoff template from RUNBOOK.
Stop after this packet. Do not advance another lane or claim live qualification.
```

## 6. Worker preflight, test budget and handoff

Before edits: verify `git branch --show-current`, `git status --short`, `git rev-parse HEAD`, CONTRACT_SHA ancestry and frozen-file hashes. If unexpected authored changes exist, stop and identify paths; do not restore them. Read only packet inputs first; expand exploration only for a named missing dependency.

For a source defect: add the regression test, run it against the unchanged behavior and capture the expected failure before fixing. For a wholly new API, verify behavioral tests fail without the implementation rather than accepting a compile-only failure as the behavior proof. Do not weaken existing tests, add skip flags, or change success assertions to match incorrect output.

Run the named test prefix, then the entire owning package. If a baseline package test fails before your change, record it separately and ask the integrator to classify it; do not silently label the whole package passed. No repeated broad tests without a new change or identified concern.

Before any multi-package heavy build/test: check uptime, hold if one-minute load exceeds 10, load the claim skill, acquire the shared build lease and verify WON (exit 0 alone is not proof). Release the exact winning claim after completion, including failures. Only G03 runs the full broad race suite; concurrency lanes run targeted race tests as permitted by the lease rules. At most two heavy build lanes per project, with the exclusive lease taking precedence.

Handoff format (maximum one page; detailed test logs may be artifacts):

```text
Packet / branch / commit:
Base SHA / contract SHA:
Status: ready-for-review | blocked
Files changed:
Implemented behavior:
Tests: exact command, exit code, behavioral result
Regression evidence: old behavior failure / new behavior pass, where applicable
Known limitations / unverified claims:
Blocked symbol or out-of-scope edit needed:
Next owner and exact action:
```

No invented results. If time/context runs short, commit a clearly labeled incomplete change only if it compiles without fake production behavior, and report incomplete; do not report ready-for-review. Preserve any useful work without changing other lanes.

## 7. Coordinator review and G02 integration

Review every returned diff against its allowlist and frozen contract before accepting. Reject invented APIs, hardcoded success, swallowed errors, missing uncertainty, tests that only mirror implementation, and any claim of live success from fixtures.

Integrate accepted commits in deterministic order: L02, L03, L05, L06, L01, L04, L07, L08 for G02. Integrate reviewed L09 and L10 after G02 and before G03; their fixture/documentation work may proceed earlier. Their new implementations should compile independently against the contract; an order-dependent lane is a contract defect to resolve, not permission to add a stub. Only the coordinator cherry-picks onto the isolated integration branch. Resolve conflicts manually after determining ownership; never pick ours/theirs blindly.

G02 exclusively owns runner.go, executor.go, repair.go, ferro.go, existing cache/schema changes if needed, tools_run_task.go, owner.go, server.go, config.go, chat.go and CLI registration. It must:

1. Preserve legacy behavior and add run_task_v2 registration using frozen wire types.
2. Wire immutable profiles and metadata client; attach the admission wrapper so ALL planner, parse-retry, repair and extraction calls go through it.
3. Establish one execution deadline and action counter; wrap the actual PageDriver with policy and ownership checks without recursively counting guard snapshots.
4. Pass schema/replay state through the core Task; preflight before paid work and validate before success.
5. Persist receipt before dispatch, finalize on all paths, return compact result, and release lease/resources. Preserve unknown effects and unknown usage on transport loss.
6. Unify side-panel profile resolution without changing the user's existing settings format silently; apply the frozen migration.
7. Wire authenticated receipt retrieval, bounded artifact reads and explicit cleanup. Direct primitives and legacy tools retain existing gates.
8. Add integration tests for failures between these components; no isolated component pass substitutes for a wired path.

Any frozen contract change requires a new lock revision, updated consumers and a rerun of affected tests. Rebase/restart only affected lanes with a supplied new baseline; never make running cheap lanes chase a moving branch.

## 8. G03 executor fixture qualification gate

This gate excludes Zatiti terminal verification (G04) and installed/live evidence (G05). B10 in the parent collects all three stages; its later evidence rows cannot block this gate.

Own integration tests in new `integration/bulk_v2_*` files and existing harness adjustments reviewed for backward compatibility. Use L09 fixtures. Execute these observable cases through actual MCP, not direct helper invocation:

1. Valid task: selected fake provider receives one planning request; output validates; receipt agrees with result.
2. Invalid schema or denied origin: zero provider requests and zero forbidden browser commands.
3. Malformed plan→repair/retry: total network attempts hit the exact request ceiling; no hidden extra request.
4. Mutation proposed by initial, repaired and replayed plans: forbidden driver method receives zero calls.
5. Disconnect/pairing change/cancel: bounded terminal/uncertain result; lease released; receipt accessible only to owner.
6. Provider timeout after request: unresolved cost retained; no automatic retry of uncertain browser effects.
7. Large valid output: full artifact retrievable with matching digest; inline preview not mislabeled as complete data.
8. Restart: unfinished execution reconciles as uncertain; no spontaneous replay. Drop the initial response before execution_id is returned; a fresh authorized client must recover by its persisted task_id. Concurrent duplicate requests dispatch once, changed input conflicts, and another principal cannot retrieve the record.
9. Warm replay: current facts extracted, partition maintained, model-call count genuinely zero when the validated production path permits it.
10. Legacy regression: prior direct tools, run_task, chat and owner/shim tests still pass.

Run formatting, vet/lint as required, package suite, the single authorized broad race suite, extension tests and real-Chrome fixture path using current repo commands. Publish `docs/evidence/bulk-v2/fixture-verdict.md` with exact tested revision and failures. Fixture pass permits packaging, not real account or spend claims.

## 9. G04 Zatiti and G05 release/pilot

G04 is intentionally not a cheap discovery task. Verify the current Zatiti API and in-flight adapter work, choose its existing dispatch extension seam, then issue separate bounded owning-repository packets with actual paths, API schemas, version handling and restart tests. Do not hand Luna a generic instruction to “integrate Zatiti.” No generic scheduler is added in Ferro.

Before declaring G04 complete demonstrate: queued item→claimed attempt→Ferro task→artifact upload→report→terminal verification; then repeat with coordinator stopped between dispatch/report and prove no duplicate action. Persist allowances and unresolved spend. One tab executes sequentially. The missing adapter is a blocker to unattended qualification, not a reason to simulate it in Codex chat.

G05 ports only inventoried packaging differences, builds a matching package, backs up private settings, drains active work, installs within user authorization and checks fresh Codex/Claude sessions. Run the parent 20-item pilot only with the authorized source/profile/spend allowance. Follow its measurable pass/fail rubric. No cheap worker selects prices, paid models, external sources, release scope or authorization on its own.

## 10. Ready-to-dispatch checklist

- [ ] G00 inventory and integration baseline recorded.
- [ ] G01 actual contract files, signatures, fixtures and checker exist; reviewed and tests pass.
- [ ] Coordinator has assigned an owner and unique branch/worktree for each dispatched packet.
- [ ] Prompt placeholders replaced; base contains contract; hashes match.
- [ ] Packet allowlists do not overlap; shared production edits reserved for G02.
- [ ] Required test commands target real tests; no fake pass, broad skip or production stub.
- [ ] Build capacity checked; lease procedure understood.
- [ ] No paid calls/live browser tasks are implicit in implementation assignments.

At plan-writing time these gates are not completed. The tasks are ready for contract preparation, not immediate blind fan-out. Once G01 is accepted, L01–L10 need implementation skill rather than fresh architecture decisions.
