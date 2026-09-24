# Zatiti browser integration and user flow

Status: proposed companion plan, 2026-09-24. Documentation requested by the
user; implementation, dispatch, release and live account actions are not
authorized by this document. Baseline: Ferro `3851637` (including caller-key recovery and corrected bulk gate order). No integration is
claimed complete. Logical owners below require an explicit session assignment.

## Authority and relationship to existing work

[Execution roadmap](execution-roadmap.md) controls shared ownership and
sequencing. [Bulk execution design](plan-bulk-browser-execution.md) and its
[runbook](tasks/bulk-browser/RUNBOOK.md) already plan bounded `run_task_v2`,
receipts, usage accounting and Zatiti dispatch. This document adds the
interactive user flow and requirements for governed direct actions; it does
not replace that work or delay its read-only pilot pending mutating workflows.

Before implementation, bulk.G01 must record a compatibility decision covering
these requirements and the hosted launch contract. Reuse one receipt store,
identity model and policy vocabulary where semantics match. Do not introduce
a second scheduler or independently freeze conflicting schemas. Proposed wire
names below are descriptive, not accepted API names.

## Outcome and first scope

A user connects a browser, binds it to a worker, assigns a task, follows its
progress, resolves prerequisites or reviews, and receives evidence-backed
results. Zatiti owns policy, durable task state, reviews, budgets, artifacts
and verification. Ferro owns browser execution, tab ownership, action
preconditions and truthful execution observations.

The direct-tool track first qualifies a sequential, fixture-based read/extract
workflow. The bounded autonomous track proceeds independently: bulk.G03
qualifies Ferro fixtures, G04 qualifies Zatiti dispatch/recovery, and G05
qualifies matching installation and the authorized pilot. Add reviewed mutations only after durable action reconciliation
and target preconditions pass. No screenshots, multi-tab execution, account
isolation or complete browser network containment are promised by this scope.

## User flow and acceptance

| Stage | User-facing behavior | Required implementation/evidence |
|---|---|---|
| Connect | Create a named browser connection; pair the extension tab; sign in in the browser if needed | Capability/version validation, opaque browser binding, clear connection state, no cookies/tokens in chat or exports |
| Authorize | Choose workers and permitted sites; inspect and apply a configuration plan | Zatiti bindings and effective policy; Ferro policy can narrow but cannot expand authority |
| Assign | Request an outcome and output format | Durable task/attempt and pinned tool contracts; no manual supervisor turn required to advance each browser step |
| Execute | Show reading, extracting, waiting for browser, and blocked states | One tab owner; bounded acquisition/renewal; evidence-linked progress without exposing secrets |
| Review | Preview the concrete destination, action and submitted data when policy requires approval | Zatiti review binds the exact action digest; Ferro checks expected pairing/origin/target immediately before dispatch |
| Resume | Reconnect or sign in, then resume after revalidation | Fresh observation and authority checks; changed target invalidates old approval; no blind mutation replay |
| Stop | Report cancellation requested and whether a dispatched action remains uncertain | Cancel only the owning run; retain uncertainty and receipts independently of transport lifetime |
| Finish | Return artifact, sources and verification status | Browser action completion is separate from independently verified task completion |

Expose connection/task administration through Zatiti's shared CLI/MCP registry.
The desktop, when used, consumes those same operations. UI labels must not
collapse browser offline, tab busy, login required, origin denied and unknown
outcome into a generic failure. Review every transition using synthetic data.

## Ferro contract additions to reconcile with bulk.G01

1. **Discoverable compatibility.** Report supported contract versions, backend,
   direct-action receipts, session binding and precondition capabilities. Reject
   unsupported required features; preserve legacy tools during migration.
2. **Durable caller identity and lookup.** Accept a scoped caller action key and
   canonical request digest before dispatch. Same key/same request returns the
   recorded state; changed input conflicts. Lookup must work after the initial
   response is lost, even if the caller never received Ferro's execution ID.
   Authentication ownership must survive a fresh transport session without
   allowing another principal to read or adopt the receipt. Correlation IDs
   alone confer no authority. Caller-key recovery and atomic same-key admission
   are mandatory for the bounded read-only pilot too, as recorded in bulk.B07
   and L07: a lost response can leave both execution and model spend unresolved.
   Direct-action records extend that execution-level contract; they do not
   replace it or authorize changing legacy tools. Retention/tombstones must
   prevent an old admitted key from silently becoming a new dispatch.
3. **Write-ahead action receipts.** Persist intent before browser dispatch and
   record dispatch state, completion/error, timestamps, request digest, opaque
   browser/session generation and evidence references. Crash after dispatch
   produces uncertainty unless authoritative evidence resolves it. Never infer
   nonexecution from an absent response or cancellation. Fail closed if intent
   cannot be stored. No exactly-once browser effect claim.
4. **Stable session binding.** Surface an opaque instance/pairing generation and
   lease identity; bind actions to them. Restart, re-pairing and lease transfer
   invalidate old handles. Keep durable receipt ownership distinct from the
   transient session allowed to operate or cancel a tab. Reuse existing leases
   and internal generation checks rather than creating another lock mechanism.
5. **Action preconditions.** Bind the request to expected origin, generation,
   snapshot/target identity and relevant expected form state. Check as close to
   input dispatch as the backend permits; stale state causes explicit rejection.
   Document residual page-script races; this is not an atomic transaction with
   the website. An integer element reference alone is insufficient for a
   delayed approval. If effects cannot be bounded, reject the governed action.
6. **Structured evidence and recovery.** Separate browser command completion
   from application outcome. Return bounded, redacted observations and opaque
   artifact references, retaining partial evidence on failure. Receipt retrieval
   is observational and never repeats an action. Site-specific read verification
   may resolve a business outcome; inconclusive evidence retains uncertainty.
7. **Autonomous execution.** Reuse bulk.B03–B08 for usage, action traces, bounded
   policy and receipts. A read-only delegated allowance may authorize bounded
   steps without a review round trip per action. Mutating `run_task` additionally
   needs per-action admission or an explicitly enforceable approved plan, with
   repairs/replans invalidating any approval they change. Freeze that extension
   separately; the read-only pilot need not wait for it.

The origin allowlist controls automation/disclosure, not every redirect,
subresource or background request. Tab leases do not isolate browser accounts.
Connection setup must explain shared authenticated authority. Separate profiles
or services are required where account separation is an actual requirement;
full egress confinement requires a separately qualified boundary.

## Delivery packets and ownership

All rows are planned, unassigned. These are coverage packets for the owning
coordinator to incorporate into existing gates, not permission to fan out.

| Packet | Logical owner | Depends on | Deliverable and completion gate |
|---|---|---|---|
| ZB01 | Ferro contract owner + Zatiti integration owner | bulk.G00; before affected bulk.G01 freeze | Current source inventory; compatibility ADR mapping bulk/hosted/direct contracts; accepted schemas and fixtures; identify exact Zatiti owned paths |
| ZB02 | Ferro receipt owner | ZB01; reuse bulk.B07 | Durable direct-action receipt and authenticated lookup; crash, duplicate and fresh-session retrieval tests |
| ZB03 | Ferro browser-policy owner | ZB01; coordinate bulk.B05 | Opaque session binding and dispatch preconditions; lease/re-pair/stale-target regression tests |
| ZB04 | Zatiti integration owner | ZB01; runtime requires ZB02/ZB03 | Named connection, adapter and capability checks; effect admission/record/reconciliation; scoped artifact publication; restart recovery |
| ZB05 | Zatiti UX/API owner | ZB04 contract | Connect/apply/task/review/resume/stop/result flow using shared registry; CLI/MCP behavioral parity and client-visible state tests |
| ZB06 | Independent qualification owner | ZB02–ZB05 | Real MCP fixture qualification and matching installed package report; limitations and evidence recorded |
| ZB07 | Joint contract owners | ZB06; bulk bounded execution gates | Separate proposal and acceptance for governed autonomous mutations; do not enable by default |

Zatiti implementation must be assigned in its own repository under its
specification-generation and package ownership rules. This Ferro plan cannot
authorize cross-package edits there. The integration owner must create the
Zatiti plan entries before starting ZB04/ZB05. Serialize shared-root changes
and reserve paths under the existing execution roadmap before dispatch.

## Qualification matrix

These are two independent acceptance tracks. ZB02/ZB03/ZB06 direct-action
contracts and mutation cases are not prerequisites for bulk.G03/G04. The
bounded read-only track uses its existing G01/L07 execution-level receipts
and policy contract. Direct-action qualification uses the ZB contract once
accepted; share tested infrastructure without conflating those schemas.

### Read-only and recovery qualification

- Successful fixture flow: connect → bind/apply → assign → acquire → observe →
  extract → publish artifact → independently verify → release.
- Crash windows: before dispatch intent, after durable intent, after browser
  dispatch, after browser completion but before response, and after response
  before Zatiti report. Recovery must not duplicate execution or silently
  incur a second model charge. For direct mutation tests it must not repeat
  the submission.
- Same action key with same/different input; concurrent duplicate submissions;
  fresh-session authorized lookup; cross-principal lookup denied; revoked
  credentials cannot retrieve or dispatch beyond the documented policy.
- Expired lease, wrong owner, browser restart, re-pairing, stale snapshot,
  disconnection and cancellation while queued versus after dispatch.
- Origin-policy tightening, login challenge and blocked redirect observation;
  document that page-network containment has not been established.
- Receipt disk full/corruption, artifact publication failure, bounded reads,
  secret redaction and retention of unresolved obligations.
- Legacy direct tools and `run_task` remain compatible. Bounded v2 runs preserve
  model usage and unknown spend on every failure path through existing bulk tests.

### Additional governed-mutation qualification

- Review an exact fixture submission, change its target while approval is
  pending, prove no input is dispatched, renew review, and verify the actual
  business outcome separately from the browser command receipt.
- Exercise direct-action duplicate admission and every dispatch crash window;
  retain unknown outcomes and never replay a possibly completed submission.
- Require a fixture-backed backend capability matrix for expected origin,
  pairing, snapshot/target and form-state preconditions. Reject unsupported
  actions and document residual races; current source names are not evidence.

Use controlled pages and disposable browser profiles first. Record tested
service/extension/Zatiti revisions, test commands, expected/observed behavior
and skipped checks. A live workflow needs separate task authorization and is
not established by unit tests, this plan, or a coordination reply.

## Handoff questions for Ferro's coordinator

1. Which receipt/session requirements are already assigned or implemented after
   the baseline? Identify owning packets and immutable revisions before reuse.
2. Can the action-key lookup and direct receipts share bulk.B07's implementation
   while preserving legacy MCP behavior? Record schema/lifecycle differences.
3. Which preconditions can each backend actually enforce, and which remain
   advisory or unsupported? Return a capability matrix and failure semantics.
4. Propose the first immutable contract baseline for Zatiti and reserve any
   overlapping files. Accepted rulings belong in the contract ADR and assigned
   work in the existing runbook; replies in ajent.social alone do not assign work.


## Coordination record — 2026-09-24

Ferro's coordinator reviewed the initial companion and requested mandatory
caller-key recovery for the read-only pilot plus separate mutation gates.
Those corrections are incorporated here against main `3851637`. No bulk.G01
contract lock or receipt/policy implementation was reported accepted. L07's
future task_receipts_v2 files remain owned by its eventual assigned lane;
G02 owns shared composition. Reuse the planned store only after G01 freezes
execution-level versus action-level semantics. No second receipt store is
assigned by this plan. Zatiti's owning integration session and exact lifecycle
schemas remain open inputs to ZB01/bulk.G04; this documentation session does
not claim that implementation lane.
