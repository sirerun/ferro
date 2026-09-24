# Bounded low-cost browser execution under frontier-model supervision

Cross-track authority: [shared execution roadmap](execution-roadmap.md). Its shared-file reservations and total lane limit apply before dispatch in either track.

Status: prescriptive implementation plan; no implementation or live spending authorized by this document alone.
Requested by David, 2026-09-24. Baseline inspected: local Ferro `e6cc219`.

## Execution entry point for Luna / cheaper Codex sessions

Use [the parallel execution runbook](tasks/bulk-browser/RUNBOOK.md), not this architecture plan alone, to dispatch work. It defines six frontier-owned gates and ten bounded cheaper-model packets, exact file allowlists, required test names/commands, a paste-ready assignment prompt, contract locking, build limits, review order, and blocked-handoff rules.

**Do not fan out before G01:** a frontier session must first commit the real compiling interface contract and reviewed fixtures. L01–L10 then execute independently against the same immutable baseline. Final production wiring, cross-repository Zatiti choices, and release qualification remain frontier-owned. No sessions have been launched by writing this plan.

## 1. Outcome and completion definition

Make Codex or Claude Code able to delegate a bounded browser task to Ferro, have a configured inexpensive model plan/repair it, and receive a compact, validated result with attributable usage and failure evidence. Then integrate that executor with Zatiti so routine batch progress does not require a frontier-model turn per URL or click.

Completion means all of the following have real evidence:

1. A fresh Codex session and a fresh Claude Code session can invoke the installed MCP contract.
2. Ferro executes a read-only task using a named OpenRouter-compatible model profile, without an implicit frontier-model fallback.
3. Limits hold across initial planning, parsing retries, repair, extraction, and replanning; failures retain usage and uncertainty.
4. The caller receives schema-valid data or an explicit unsuccessful disposition. Invalid/partial data is never promoted to success.
5. Zatiti dispatches a 20-item sequential pilot without requiring Codex to individually advance each item; artifacts and terminal state survive coordinator interruption.
6. Pilot evidence reports accepted outputs, failures, total provider spend or unresolved spend, runtime, and supervisor interventions. Savings are measured, not assumed.
7. A matching extension/service package is installed and verified. Source-only tests do not qualify the installed pair.

The 20-item pilot is a qualification experiment, not permission for a campaign. Use an authorized public source or controlled fixture. No outreach, account modifications, purchases, or form submission in this milestone.

## 2. Ground truth and scope boundaries

Inspected source anchors:

- `internal/mcp/tools_run_task.go`: MCP args currently expose goal, start_url, max_plannings; result exposes result/metrics; execution errors discard the result envelope.
- `internal/core/runner.go`: Task already has Schema and ReplayKey; these are not exposed by current run_task.
- `internal/core/metrics.go`: EstimatedTokens uses approximately text length divided by four; it is not provider billing.
- `internal/llm/openai.go`: compatible completion client; current response decoding does not capture provider usage.
- `internal/mcp/chat.go`: side-panel execution constructs its own model runner and can install a read-only driver.
- `internal/mcp/config.go`: MCP runner uses service model configuration; do not assume side-panel settings automatically configure it.
- Existing execution has per-origin gates, tab leases, cancellation, repairs and replanning. Extend these mechanisms instead of bypassing them.

Earlier live evidence: MCP could read the paired Chrome tab and release its lease. Zatiti MCP initialization and artifact upload/readback passed. Those checks did not establish unattended batch dispatch or provider usage accuracy.

Ferro owns one bounded execution, browser ownership, model-call admission, output validation and execution evidence. Zatiti owns persistent queues, work assignment, retries, batch budgets, artifact retention, and terminal task verification. The supervisor owns task design, exception decisions and sampled quality review. Do not add a second durable scheduler, daemon, campaign database, or worker lifecycle system to Ferro.

One paired tab means one executing task. Parallel implementation lanes do not imply parallel access to the user's Chrome tab. Future parallel browser execution requires separate service homes, credentials, profiles and explicitly assigned tabs; a tab lease is not tenant isolation.

Inventory saved-work branches and archives if present; do not assume a branch named `STASH` exists. Preserve their contents and never bulk cherry-pick unidentified work. Installed files have diverged from repository code; package reconciliation is a release prerequisite.

## 3. Execution ownership and parallelism

Owners below are logical lane names, not already-running agents or assignments to named people. The implementing coordinator assigns a person/session before changing a row to in-progress. Every lane uses an isolated worktree and a task contract copied from its row. No worker dispatch is requested by this document.

| ID | Accountable lane | Deliverable | Dependencies | Main owned surface |
|---|---|---|---|---|
| B00 | integrator | Baseline/package inventory and integration branch | none | inventory documentation |
| B01 | contract-owner | Versioned task/result/profile/budget contracts and ADR | B00 | new contract definitions and schemas |
| B02 | profiles | Shared profile resolver and private configuration | B01 | new profile implementation; config/chat changes coordinated |
| B03 | provider-usage | Provider usage and per-call receipts | B01 | internal/llm |
| B04 | budgets | Execution-wide admission and limit controller | B01 | new core budget implementation |
| B05 | browser-policy | Enforced task policy and session binding | B01 | driver policy wrapper and tests |
| B06 | validation-cache | Output validation and safe replay | B01 | new core validation/cache integration |
| B07 | result-artifacts | Result envelopes, evidence storage and retrieval | B01 | new MCP result/artifact implementation |
| B08 | MCP-integrator | Versioned tool wiring into the real runner | B02–B07 | tools_run_task, owner, runner, public exports |
| B09 | Zatiti-integration | Durable sequential dispatch and report contract | B01 discovery; execution after G03 executor fixtures | Zatiti-owned adapter/worker integration |
| B10 | qualification | Executor fixture suite and staged evidence matrix | B01 fixture authoring; G03 after B08 + L09/L10; later rows at G04/G05 | integration tests, fixture data, evidence |
| B11 | release-owner | Matched package, migration and pilot release | B00, G03 executor fixtures, G04 Zatiti qualification | packaging/docs; existing extension preserved |
| B12 | supervisor | Authorized 20-item live pilot and verdict | B09–B11 | pilot report and acceptance record |

B-rows describe coverage; G/L IDs in the runbook are the executable assignments. The following waves are a summary, not a second dispatch graph:

Parallel waves:

- Wave 0: B00, then B01. B10 may inventory test infrastructure without freezing new interfaces.
- Wave 1: B02, B03, B04, B05, B06, B07, B09 contract work, and B10 fixture work proceed independently after G01 freezes the v2 contract.
- Wave 2: B08 composes reviewed dependencies. G03 qualifies the executor using L09 fixtures and L10 documentation. Then B09/G04 verifies the real Zatiti adapter and restart recovery. B11 may prepare packaging after G03; installation waits for G04.
- Wave 3: B11 installs the matching build; B12 conducts authorized live qualification.

Critical path: G00 → G01 → L01–L08 integration (G02) plus L09/L10 → executor fixtures (G03) → Zatiti qualification (G04) → package and pilot (G05). G04 discovery can overlap implementation after G01.

Shared-file rule: B08 alone edits `internal/mcp/tools_run_task.go`, owner dispatch and the final runner wiring during parallel execution. Other lanes provide implementations and focused tests in separate files. B01 owns shared contract types; subsequent contract changes require its review, consumer updates and an explicit version decision. Never resolve collisions by restoring another lane's files.

Handoff per row: commit SHA, contract revision, owned paths, tests and outcomes, known limitations, example request/result, migration effects, and next dependency. A passing unit test with a fake provider is not live model qualification.

## 4. Contract to freeze in B01

Introduce `run_task_v2`; preserve the existing `run_task` argument/result contract during migration. Document legacy limitations. Do not silently make existing mutating tasks read-only or change existing error semantics. Share implementation internally once behavior is explicit.

Required request semantics:

| Field | Contract |
|---|---|
| schema | Literal version identifier, e.g. ferro.task/v1 |
| task_id | Caller correlation ID; never a claim of exactly-once browser execution |
| goal | Nonempty bounded text; reject oversized input before any model call |
| start_url | Optional explicit URL; omission binds to the currently paired tab |
| model_profile | Name resolved from service-owned configuration; no arbitrary key/base URL in task arguments |
| policy | Explicit read_only for pilot; exact allowed origins narrowed against service allowlist |
| output_schema | Bounded supported JSON Schema; validate before execution and after result production |
| limits | Positive bounded values for runtime, action count, model calls, per-call output tokens, cumulative tokens; optional USD reserve ceiling |
| replay_key | Optional caller label; server derives scoped cache identity, never trusts label alone |
| evidence | Compact response by default; bounded detailed artifacts retrievable separately |

Default pilot ceilings: 90 seconds total, 20 dispatched browser actions, 3 total model requests, 1 repair, 2 planning passes including initial plan, 2,048 completion tokens per call, 12,000 estimated input tokens per call, and 24,000 estimated input-plus-maximum-output tokens cumulatively. These are starting policy values, not a prediction of task difficulty or cost. Preserve existing legacy max_plannings meaning; use an unambiguous v2 field and define whether the initial plan counts. Treat estimated token admission separately from provider-reported usage.

Use integer micro-USD for monetary fields and explicit currency. No floats for accumulated money. Omitted limit inherits the configured bounded default; zero, negative, overflow, and above-policy values are rejected, not interpreted as unlimited. Service policy may tighten caller limits, never widen them. Return effective limits in the result.

Proposed result fields:

- schema/version, task_id, execution_id, terminal status, started_at, ended_at;
- model profile identifier and resolved model/provider identity where actually reported;
- result and validation state; partial_result separated from accepted result;
- bounded summary and source references with retrieval timestamps;
- metrics: all request attempts, repairs, planning passes, browser actions, latency;
- usage: reported input/output/reasoning/cache fields when supplied, estimates separately, currency, known billed amount, reserved amount, unresolved amount/state;
- error: stable category, stage, retry classification, bounded redacted detail;
- side_effect_state: none_observed, confirmed, or unknown, with evidence references;
- artifacts: opaque references plus byte size, media type and digest; no unrestricted filesystem paths.

Terminal statuses: succeeded, failed, blocked, cancelled, budget_exhausted, outcome_uncertain. Detailed reasons include invalid_input, profile_missing, disconnected, pairing_changed, tab_busy, origin_denied, login_required, output_invalid, provider_error, deadline_exceeded, and capability_unsupported. B01 freezes the exact mapping. A task may have unknown provider spend even when its browser result succeeded; cost state is independent of outcome.

Validly admitted execution failures return the structured envelope. Protocol/authentication/malformed-request failures remain MCP errors with a stable safe diagnostic. Do not encode ordinary browser failure as successful output. Cancellation must preserve a retrievable receipt when the transport cannot return its final response.

## 5. B00 — baseline and package reconciliation

1. Record main, saved-work and worktree revisions and clean/dirty state without changing them.
2. Inventory installed executable hash, extension manifest/version, file hashes and launch configuration. Inspect private settings only for required field presence; never print keys.
3. Compare installed side-panel and connection patches against intended release source. Identify each unique patch's owner and commit; port deliberately, preserving license/assets.
4. Produce a manifest linking source revision to service and extension hashes. An extension version number alone is insufficient.
5. Identify existing user state and backup format. Backups containing keys remain private.

Acceptance: every installed/source difference has a disposition; source changes can be built without copying unknown working-directory files. No live service replacement yet.

## 6. B02 — model profiles

Implement one resolver used by v2 MCP and side-panel chat. A profile includes endpoint, exact model ID, credential reference, request limits, timeout policy, provider-routing constraints, and optional verified pricing metadata with timestamp/source. Task callers select names, not credentials or arbitrary endpoints.

- Store secrets in the existing private service configuration mechanism; do not place them in MCP arguments, results, prompts or committed examples.
- Preserve legacy environment configuration as an explicit compatibility profile. Migrate side-panel settings by creating an equivalent profile without overwriting existing profiles.
- Configuration reads/updates must be atomic and validated; a running task binds a profile revision so mid-task edits do not alter its model or broaden permissions.
- No automatic paid frontier fallback. Missing/unsupported model configuration fails before a browser action or provider call.
- Add a capability view exposing profile names, supported constraints and defaults, with no secret values.
- Keep OpenAI-compatible providers working; provider-specific routing fields stay in an explicit optional provider extension.
- Verify current OpenRouter request/response and pricing documentation at implementation time. Do not encode an unverified model name or assumed current price in production defaults.

Tests: legacy profile migration, missing credentials, invalid endpoints/models, redaction, concurrent config update, stale profile revision, profile selection across chat/MCP, no secret in errors. Live provider smoke requires explicit spending authorization and a bounded allowance.

## 7. B03 — provider usage and request receipts

Keep the public completion interface compatible where possible. Introduce a metadata-capable adapter or optional interface for usage rather than breaking every existing LLMClient implementation. The contract owner settles that seam before other lanes code against it.

- Capture actual request ID, resolved model/provider if returned, reported token fields, finish reason and cost only when reported with defined semantics.
- Record every attempted request, including malformed completion retries, failed HTTP responses and timeout after send.
- Distinguish pre-send rejection (no request sent), confirmed provider response, and uncertain billing after transport loss.
- Bound response body and error body size; redact authorization headers and echoed secrets.
- Missing usage is unknown, not zero. Never label len(text)/4 as billed tokens.
- Aggregate receipts across all planner/repair/extraction calls. Avoid double counting cached-token subsets or provider fields whose totals already include reasoning.
- Retry policy is explicit; count each network attempt before sending. Do not hide automatic SDK/HTTP retries outside the budget controller.

Tests with controlled HTTP fixtures: normal usage, absent/partial usage, streaming not supported/refused if applicable, malformed JSON, truncated output, 401, 429 with Retry-After, 5xx, timeout before/after response headers, response too large, unknown model, alternate provider fields, cancellation. Confirm no fabricated billing on any path.

## 8. B04 — budget and execution controller

All model calls pass through one per-execution admission object, including repair and extraction. All browser actions pass through an action counter with the same execution deadline. Counters are concurrency-safe even though the first release executes sequentially.

Before a provider request: reserve one request, bound prompt bytes/context, reserve conservative input and maximum completion tokens, and reserve estimated worst-case spend if enforceable pricing is configured. After completion: reconcile actual reported usage; retain an uncertainty reservation if the provider may have processed an unanswered request. Stop when any relevant limit is exhausted.

A hard dollar ceiling is available only when an enforceable upper bound exists for every possible routed provider/model charge. Unknown/stale pricing, unbounded routing, unknown tokenization or unbounded fee semantics must reject hard-dollar mode, not advertise a guarantee. Support clearly labeled estimated-budget mode plus hard request/output/runtime limits. Upstream account controls may offer another limit but do not assume they are exact or instantaneous.

- Price updates cannot lower an in-flight reservation retroactively without evidence.
- Deadline includes provider wait, backoff, browser execution and repair; every goroutine has cancellation/exit ownership.
- A cancellation stops future admissions; it cannot reverse a request or browser action already sent.
- Retry sleeps respect remaining deadline and reservations; no busy loops.
- Overflow-safe integer arithmetic and atomic reservation/reconciliation.
- Zatiti grants a bounded child allowance and retains reservations until receipt reconciliation. Ferro does not invent an installation-wide batch budget database.

Tests: limit boundary, last allowed call, concurrent reservation, repair attempts consuming limits, retry consuming limits, estimate versus actual reconciliation, unknown usage, arithmetic overflow, timeout, cancellation and no post-cancel request dispatch.

## 9. B05 — browser policy and ownership

Promote the existing read-only driver mechanism into a reusable policy wrapper used by v2 tasks. Enforce policy below the model plan so repaired plans and cache hits cannot bypass it.

Pilot policy allows snapshot, extraction, scrolling, waiting and navigation to exact approved origins. It rejects click, fill, select, keyboard input, file operations and arbitrary evaluation. Read-only describes browser primitives, not a guarantee that all GET requests are side-effect-free; use appropriate sources and do not claim otherwise.

- Effective origins are the intersection of task scope and service allowlist; no wildcard broadening.
- Recheck redirects, navigation destinations, current origin and policy revocation at existing execution boundaries.
- Bind execution to lease owner, tab identity and pairing generation. Pairing changes stop execution; do not silently continue in another tab.
- Blocked/login/captcha signals produce a bounded terminal result; no evasion or endless polling.
- Treat page instructions as untrusted data, not authority to change the goal, profile, destination or policy.
- Keep credential/profile/API settings inaccessible to page content and model-directed browser commands.

Tests: malicious page asks to submit/send, repair proposes click, cached plan proposes mutation, redirect exits allowlist, allowlist revoked mid-run, lease collision, pairing changes, disconnect during action, wrong client cancellation, denied action sends zero commands. Exercise both extension and CDP backends.

## 10. B06 — validation and replay

Expose the core Schema and ReplayKey through the v2 contract, with bounded schema validation and scoped replay.

- Validate schema syntax/size/complexity before spending. Reject remote schema references and unsupported keywords rather than fetching arbitrary URLs.
- Validate final output mechanically; mark missing evidence and partial extraction separately. Presence/schema validity is not factual truth.
- Return source URL and retrieval time as execution provenance where available. Do not invent original posting dates or contacts.
- Prefer deterministic selector extraction for stable layouts. Test actual zero-model warm replay through the production path before claiming it.
- Scope cache keys by task/schema version, policy, origin, profile/account partition, model compatibility and layout evidence. Never reuse another user's authenticated content or cached answer.
- Cache plans, not fresh facts; retrieve current page data on every task.
- Revalidate locators/preconditions on a replay. Stale or unsafe plans invalidate the cache and either replan within remaining allowance or fail.
- Cache loading uses bounded trusted formats; no execution of cache-supplied arbitrary code. Atomic writes; cache failure is observable and does not fabricate success.

Tests: schema mismatch, truncation, missing field, unsupported schema, oversized output, stale page, same replay label across origins/accounts/schemas/policies, policy tightening, warm deterministic replay, cold miss, cache corruption. Report cache hits separately from accepted outputs.

## 11. B07 — compact results and evidence

Provide compact inline responses so routine page snapshots do not return to the expensive supervisor. Default summary cap: 2 KiB; inline result cap: 16 KiB. Larger results return a bounded preview and artifact reference; never silently truncate schema-valid data and still mark the truncated result valid.

Use existing storage facilities where available; otherwise a minimal private append/write execution receipt store is acceptable, but no scheduler/retry daemon. Zatiti remains the durable business record.

- Store bounded detailed action traces, provider receipts and final outputs. Record metadata by default, not whole authenticated pages or secrets.
- Opaque artifact identifiers, digest, media type, size; authenticated retrieval with ownership checks and bounded ranges. Prevent path traversal and cross-session unauthorized reads.
- Explicit retention/size limit and operator-triggered cleanup; active/unreconciled receipts cannot disappear silently. No background lifecycle service added for cleanup.
- Retrieval supports interrupted-call reconciliation using execution_id. A caller task_id is correlation only. Do not label retry as exactly once.
- If deduplication is supported, persist a request digest and reject task_id reuse with changed input. After restart, an in-flight record becomes outcome_uncertain unless authoritative completion exists; never replay it automatically.
- Preserve metrics and partial evidence on provider errors, cancellation, browser failure and validation failure.

Tests: failed task retains spend, broken MCP connection preserves receipt, restart with in-flight record, duplicate request mismatch, bounded retrieval, access denial, redaction, oversized artifact, disk full, interrupted atomic write. Disk failure must not yield a success receipt with nonexistent artifacts.

## 12. B08 — MCP integration and compatibility

Wire B02–B07 into the production Owner/Runner path. Add v2 tool registration and schema descriptions; preserve direct tools and legacy run_task behavior. Every task follows this order:

1. Validate request and resolve immutable profile/policy/limits.
2. Check browser connectivity, ownership and origin without exposing unauthorized content.
3. Establish execution identity and receipt; acquire existing lease ownership mechanism.
4. Admit model requests and browser steps through the shared controls.
5. Validate output, finalize receipts, emit compact result.
6. Release lease and resources on all exits; preserve recoverable evidence even when return transport is gone.

Avoid holding global owner locks during provider calls if that blocks status/cancellation. Preserve existing cancellation identity, transport authentication, service credential behavior and owner/shim semantics.

Tests: real stdio and remote MCP serialization, old clients unchanged, two clients contending, status/cancel during long task, malformed v2 input, service restart, profile revision change, errors with structured result, no network call when validation/policy rejects. Remote exposure retains existing access restrictions; do not open a public listener.

## 13. B09 — Zatiti integration (separate repository ownership)

Before coding, inspect current Zatiti main and in-flight MCP adapter work; the previously inspected build was `859500a`, not a permanent assumption. Reuse an existing qualified adapter if available. Record the chosen extension seam and capabilities from current schemas. Do not duplicate a dispatcher while another lane owns it.

Ferro deliverables: versioned tool contract, examples, capabilities, bounded execution, receipt retrieval and fake-free conformance fixtures. Zatiti deliverables belong in its own plan/task contracts and require its ownership/review rules.

For each item, the durable workflow must:

1. Validate source/policy/schema and allocate a per-item allowance from remaining batch allowance.
2. Persist task and correlation IDs before dispatch; obtain the cooperative attempt/lease using actual APIs.
3. Invoke run_task_v2 using a configured cheap profile; respect one paired-tab execution slot.
4. Keep the Zatiti attempt alive through its existing lifecycle mechanism where required; do not maintain it with Codex chat polling.
5. Collect receipt/result, validate, upload artifacts at the exact task scope and report actual IDs/digests.
6. Read verification and terminal state; report rejection truthfully. Artifact presence alone does not prove task correctness.
7. Classify retry: transient known-nonexecution may retry within allowance; login/policy/input errors stop; uncertain effects require reconciliation, never blind replay.
8. Persist compact batch summary and exception queue. Codex wakes only at the chosen review boundary or an actual escalation.

Restart test: stop/restart the coordinator between dispatch and report; reconcile the execution receipt and finish reporting without duplicate browser actions. No autonomous outreach or unrestricted agent permission grant.

If current Zatiti cannot provide this durable path, report the exact missing operation with a named Zatiti task and dependency. A manual 20-call Codex loop is useful debugging but does not satisfy unattended-batch acceptance.

## 14. B10 — independent qualification and benchmark

Use controlled local web fixtures first; real account pages are not deterministic test fixtures. Include stable pages, changed layouts, redirects, login/blocked pages, injected instructions, malformed model output, schema mismatch, disconnect, slow responses and uncertain action completion. Test doubles only in tests; no synthetic production success.

Required evidence matrix, collected in stages: G03 covers executor fixtures and legacy compatibility; G04 covers Zatiti integration/restart; G05 covers installed packaging and the authorized pilot. G03 must not wait on G04 or G05 evidence. B10 is the umbrella for evidence, not a separate all-at-once prerequisite.

| Area | Must demonstrate |
|---|---|
| Model routing | Selected cheap profile used; no hidden fallback |
| Limits | All planner/repair/extraction/retry calls and actions counted |
| Policy | Denied action never reaches driver on either backend |
| Usage | Reported/estimated/unknown separated, including failures |
| Result | Valid data or explicit unsuccessful disposition |
| Recovery | Lost transport/restart does not duplicate uncertain work |
| Isolation | Lease, pairing, artifact ownership and cache boundaries hold |
| Compatibility | Legacy direct tools/chat/owner-shim behavior remains |
| Integration (G04) | Zatiti terminal verification and artifacts match actual result |
| Packaging (G05) | Installed service and extension hashes match tested release |

Benchmarks compare on the same fixed fixtures and output criteria: direct frontier supervision, cheap-model tasks, and warm deterministic replay. Record all task attempts, not only successes. Live baseline runs require their own spending authorization. Report total spend divided by accepted outputs, acceptance rate, p50/p95 latency, model requests, repairs, unknown-cost count and supervisor interventions. Record frontier interaction/token data only when observable; do not manufacture a conversion to subscription dollars or exact quota savings.

Run focused tests per lane. At integration gate run project-required formatting, vet/lint, Go tests, concurrency race suite, Node extension tests and real-Chrome fixture suite. Consult current repository commands before running. Heavy builds follow machine load/build-lease rules; only the assigned integration lane runs the broad race suite. No need to run builds for this documentation-only plan.

## 15. B11 — package and migration

Build the service and extension from the same qualified revision, including deliberately reconciled side-panel patches. Manifest lists commit, hashes, configuration schema version, supported tool/bridge versions and test evidence.

- Preserve private model settings, pairing token, allowlist and user state. Use a reversible private backup before migration.
- Validate profile migration offline; refuse incompatible configuration with a useful error rather than resetting it.
- Drain active task/lease before replacing the service. User extension reload/re-pair may still be required; document it.
- Smoke-test with a harmless fixture in the installed package through both fresh clients.
- Rollback restores the prior matching executable/extension/configuration format without deleting receipts or replaying tasks. Never restore a broad backup over newer unrelated settings.
- Publish runbook describing cheap profile, budgets, result retrieval, cost uncertainty, policy limits, and Zatiti ownership.

Gate: fixture-based installed-package path passes; no browser pairing or live-provider success inferred from build success.

## 16. B12 — live pilot and go/no-go

Before sending a paid request, agree on source, exact model/profile, total spend allowance and review boundary. Fetch current official provider documentation/pricing, record date and model identifiers, and confirm account controls. Do not assume the user's existing subscription pays OpenRouter charges.

Use 20 bounded read-only tasks with a frozen extraction rubric and schema. Review every pilot result against source evidence; a larger rollout can sample only after measured accuracy supports it. Keep blocked/failed/unknown outcomes in the denominator.

Predeclared initial release gate:

- zero observed policy violations, secret leakage, cross-task data leakage, or duplicate uncertain actions;
- all 20 items have a terminal or explicitly unresolved record and accounted allowance;
- at least 18/20 outputs meet the factual rubric without manual repair;
- no uncontrolled retries or budget admissions beyond declared limits;
- all provider spend is reconciled, or the pilot remains financially unqualified with reserved uncertainty;
- at most two exception interventions after initial dispatch, excluding scheduled quality review;
- cost per accepted result and supervisor work are reported against an available comparable baseline; no savings claim without a baseline.

Failure triggers the responsible lane's regression task, not automatic scale-up or automatic model escalation. A cheaper model with poor acceptance can cost more overall. Report recommendation as go, revise, or stop with measured reasons.

## 17. Risks, deferrals and explicit triggers

| ID | Owner | Deferred capability / risk | Trigger and measurable next action |
|---|---|---|---|
| D01 | release-owner | Parallel browser workers | After B12 passes and measured queue wait dominates runtime: design two isolated sessions and prove cross-session denial before rollout |
| D02 | supervisor | Automatic model escalation | After failed-task categories justify it: explicit allowed models and additional allowance; benchmark incremental acceptance before enablement |
| D03 | browser-policy | Mutating browser jobs | New user-authorized use case: define action-specific authority, confirmation, idempotency/reconciliation tests before enabling |
| D04 | provider-usage | Exact-dollar mode unavailable | When enforceable provider pricing/token bounds are verified: add pricing conformance and pre-admission bound tests; keep estimated mode labeled meanwhile |
| D05 | Zatiti-integration | Missing durable adapter operation | During B09 discovery: create owning-repo task naming missing API and restart acceptance test; blocks unattended qualification |
| D06 | validation-cache | Site changes break replay | Any observed stale replay: invalidate that cache partition, reproduce fixture, fix locator/precondition test before re-enable |
| D07 | profiles | Model/provider changes | Profile revision or pricing/model change: rerun model conformance + fixture benchmark; no silent replacement |
| D08 | release-owner | Installed/source divergence recurs | Every package build: hash manifest comparison; fail packaging when unaccounted patches remain |

These rows carry owners/dependencies/triggers; they are not implemented capabilities. No unbounded “later” work outside this table.

## 18. Review and status discipline

Each B-row starts not-started. Evidence changes status to in-progress, reviewable, fixture-verified, installed-verified, or live-qualified; do not collapse those states into “done.” B01 records architectural choices and rejected alternatives in an ADR: reuse core execution rather than another agent loop; queue in Zatiti rather than Ferro; named private profiles rather than caller-supplied credentials; versioned MCP tool rather than silent compatibility break.

The plan itself authorizes no merge, deployment, paid calls, outreach, worker launch or new infrastructure. Implementation work follows the user's actual task authorization and repository merge authority. The next executable step is B00, followed by contract review B01; B02–B07 can then be assigned concurrently.
