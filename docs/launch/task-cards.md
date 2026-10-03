# Executable task cards

Generated from tasks.json; change that file and run `python3 docs/launch/check_plan.py --write`.

All listed checks are required future verification, not claims that they already ran. Read agent-runbook.md before assigning work.

## Dependency waves

A wave is a dependency frontier, not permission to run every task simultaneously. Enforce path ownership and the build lease; use at most three coding lanes plus one coordinator across both tracks.

- Wave 0: D01
- Wave 1: D02
- Wave 2: D03
- Wave 3: D08, A11
- Wave 4: D04
- Wave 5: D05, D06, D07
- Wave 6: A01, A04, A10, A13, A16, F01, X01, W01
- Wave 7: A02, A05, A07, A14, A17, F02, I01
- Wave 8: A08, A12, A15, F03, F07, F09, I02
- Wave 9: A03, A19, F04, F11
- Wave 10: A06, F05, F12
- Wave 11: A09, F06, F13, X02
- Wave 12: F08, F14, X03, W02
- Wave 13: F10, F15, X04, I03
- Wave 14: A18, X05, X06
- Wave 15: X07, W03
- Wave 16: X08
- Wave 17: Q01
- Wave 18: Q02, Q03, Q06
- Wave 19: Q04, Q05, Q08
- Wave 20: Q07, R01
- Wave 21: R02
- Wave 22: R03
- Wave 23: R04

## D01 — Reconcile launch baseline and active work

- Repository: `ferro`; lane: `coordinator`.
- Dependencies: none.
- Owned paths: `docs/launch/evidence/D01.md`.
- Reviewer: coordinator; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Fetch main; record reviewed merge and dirty worktrees without changing them.
2. Inventory live agents/cwd and read relevant ajent.social; record conflicts and branch owners.
3. Establish local-mode baseline commands and capture actual results under build lease.

Acceptance:

- Exact base SHA, worktree list and regression commands recorded; no stale branch wholesale merge.

**Required verification:** git diff --check; local Go/Node suites on selected baseline; report Chrome skips explicitly

## D02 — Complete cross-machine semantic census

- Repository: `ferro`; lane: `coordinator`.
- Dependencies: D01.
- Owned paths: `docs/launch/evidence/D02.md`.
- Reviewer: coordinator; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Use restricted census to revisit mini and MacBook source paths; record current revisions and uncommitted differences.
2. Compare at least two independent source lifecycles per candidate or justify RFC path C; inspect actual negative-path tests.
3. Mark each boundary EXTRACT, REFERENCE_EXISTING, INVESTIGATE or REJECT; separate source lineage and third-party forks.

Acceptance:

- No component justified by filename or worktree count; source maturity and differences documented; password/session divergence retained.

**Required verification:** Reviewed comparison matrix and cited source locations in private evidence; no source runtime test claims without execution

## D03 — Clear provenance and public evidence boundary

- Repository: `ferro`; lane: `coordinator`.
- Dependencies: D02.
- Owned paths: `docs/launch/evidence/D03.md`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Inspect license/history/authorship for proposed copied code; flag restricted upstream license.
2. Create sanitized behavior contracts and attribution plan; forbid unapproved copying.
3. Review public fixtures, paths, URLs and notices before package work.

Acceptance:

- Every extraction has explicit original-code or cleared-copy path; restricted census never enters public commits.

**Required verification:** Provenance review plus secret/private-identifier scan of proposed public artifacts

## D08 — Qualify generated hosted application profile and neutral contract

- Repository: `ferro`; lane: `coordinator`.
- Dependencies: D03.
- Owned paths: `docs/launch/architecture.md`, `docs/launch/evidence/D08.md`.
- Reviewer: independent-security; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Identify the versioned, public, neutral contract required to consume the generated application profile; keep private project names and source out of public artifacts.
2. Qualify an AMOS-generated paid application and its authoritative provider/admission adapter, tenant isolation, restart/recovery, and operational ownership against the consumer journey.
3. Compare the qualified profile with preserved Griffon services and current Ferro behavior; record exact residual Ferro-owned browser/device responsibilities and migration constraints without inventing login or migration work.

Acceptance:

- Versioned public neutral contract is reviewable and provenance-safe; generated paid app/provider/tenant isolation/recovery evidence is recorded against exact revisions and limitations.
- Griffon preservation is explicit; no new hosted runtime adoption task is eligible until D08 is accepted and D04/D05 freeze the consumer contract.
- Browser action authority remains based on explicit pairing, exact-origin policy, per-task interaction consent and action confirmation; authentication or paid admission alone cannot grant it.

**Required verification:** Independent contract review plus cited generated-app/provider/tenant-isolation/recovery qualification evidence; no runtime result may be inferred from documentation alone

## D04 — Freeze hosted architecture and threat model

- Repository: `ferro`; lane: `coordinator`.
- Dependencies: D01, D02, D08.
- Owned paths: `docs/launch/architecture.md`, `docs/adr/011-hosted-ferro.md`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Adopt or explicitly revise the qualified generated application/provider profile with rationale; do not invent app-owned hosted login, billing or schema migration.
2. Model tenant/device/task authority, stale worker/extension fencing, provider boundaries and destructive-action confirmation.
3. Define local-mode preservation and rollback boundaries; reject unsupported browser actions.

Acceptance:

- No unresolved authority or replay semantics; coordinator approves the versioned application/provider adapter, tenant boundary, single device/task scope and deployment profile.

**Required verification:** Threat-model walkthrough using readiness Gate 3 scenarios

## D05 — Freeze executable contracts and dependency versions

- Repository: `ferro`; lane: `coordinator`.
- Dependencies: D03, D04.
- Owned paths: `api/hosted-v1.yaml`, `internal/hosted/contracts/`, `docs/launch/contracts/`, `docs/launch/evidence/D05.md`.
- Reviewer: security; initial size: 6-12 agent hours; status: planned.

Implementation:

1. Freeze the versioned neutral profile contract and only the consumer routes/operation schemas required for Ferro browser/device integration, with success/error examples and authority matrix.
2. Assign hosted identity, paid-admission, persistence and migration ownership to the qualified application/provider profile; specify only residual Ferro-owned state and compatibility adapters.
3. Pin reviewed dependencies only after D08 qualification; define bounded browser/device payloads and compatible protocol versions.
4. Freeze the maintenance CLI/workflow contract and IAM role separately; it has no public endpoint or arbitrary SQL capability.

Acceptance:

- Contracts validate; examples cover denied cases; no implementation agent must invent a shared interface; lockfile owner assigned.
- No app-owned hosted login, paid entitlement source or migration is specified unless D08 evidence proves a narrow residual requirement and D04 records its owner.

**Required verification:** OpenAPI/JSON Schema validation; contract fixture round trips; interface compile checks

## D06 — Freeze website and extension interaction states

- Repository: `ferro`; lane: `coordinator`.
- Dependencies: D04.
- Owned paths: `docs/launch/ui-contract.md`, `testdata/hosted-ui/`.
- Reviewer: coordinator; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Enumerate logged-out/linking/online/offline/unpaid/busy/uncertain/revoked/deleted states.
2. Define screen copy, selectors, focus/live announcements, fixture API responses and exact confirmation UI.
3. Preserve glass bubbles and frame-color behavior; define account-separated local history.

Acceptance:

- Every error has recovery action; UI fixtures match D05 before integration; no silent tab switch or credential copy onboarding.

**Required verification:** Review static UI fixtures against architecture and accessibility checklist

## D07 — Record launch-owner decisions and external setup

- Repository: `ferro`; lane: `coordinator`.
- Dependencies: D04.
- Owned paths: `docs/launch/deployment-inputs.example.json`, `docs/launch/evidence/D07.md`.
- Reviewer: maintainer; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Prepare concrete deployment-profile cost/resource assumptions and required secret-reference names after D08.
2. Record the qualified application/provider profile's customer identity, paid-access, support and retention ownership; separately record Ferro browser/device and deployment responsibilities.
3. Separate prepared artifacts from authorized actual mutations; use fake values in committed examples.
4. Obtain or prepare an authorized draft store item for its stable ID early; public publication waits for R02.

Acceptance:

- No secret values committed; all public commercial claims and deployment inputs have an accountable owner; no implicit activation of other product staging.

**Required verification:** Owner-reviewed decision table and non-secret config schema validation

## A01 — Implement PostgreSQL servicecred Store

- Repository: `go`; lane: `amsl-identity`.
- Dependencies: D05.
- Owned paths: `servicecred/pgstore/`, `docs/servicecred-postgres.md`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Reuse existing servicecred public API; add parameterized SQL Store adapter and caller-applied schema.
2. Implement atomic create, exact-bound revoke, authoritative lookup and sanitized list with context/timeouts.
3. Keep boltstore behavior/API and avoid cached authentication.

Acceptance:

- Two processes see committed revocation; no overwrite or secret leak; DB failures deny.

**Required verification:** go test -race ./servicecred/... using disposable PostgreSQL contract suite

## A02 — Verify credential store parity and publishable evidence

- Repository: `go`; lane: `amsl-identity`.
- Dependencies: A01.
- Owned paths: `servicecred/contracttest/`, `servicecred/pgstore/contract_test.go`, `docs/evidence/servicecred-postgres.md`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Exercise all existing denied grant/expiry/scope/redaction cases against both supported stores.
2. Add DB outage, revoke/create race, account/resource mismatch and fresh-process checks.
3. Produce candidate implementation commit and review bundle without claiming stable status.

Acceptance:

- Every Store guarantee has a real persistence test; SQL driver/dependency rationale and provenance included.

**Required verification:** go test -race ./servicecred/...; redaction serialization tests; package review

## A03 — Dogfood credentials in hosted Ferro

- Repository: `ferro`; lane: `hosted-identity`.
- Dependencies: A02, F02, F03.
- Owned paths: `internal/hosted/auth/device_credentials.go`, `internal/hosted/auth/device_credentials_test.go`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Pin reviewed published AMSL commit without replace/GOWORK override.
2. Wire trusted account/device ceilings and request-time verification into hosted middleware.
3. Exercise two credentials/users and restart revocation through real HTTP handlers.

Acceptance:

- Foreign device/session/task denied; disabled account overrides token; local shared mode never authorizes hosted endpoints.

**Required verification:** go test ./internal/hosted/auth -run TestDeviceCredential -count=1 with PostgreSQL

## A04 — Freeze checkout capability contract

- Repository: `capabilities`; lane: `amsl-billing`.
- Dependencies: D05.
- Owned paths: `capabilities/billing.customer-checkout.json`, `docs/contracts/customer-checkout.md`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Write exact common lifecycle from D02, SDK alternatives and non-goals.
2. Specify durable attempt/lease/payload hash and provider-retention recovery semantics.
3. Record restricted evidence honestly; obtain API/security review before implementation.

Acceptance:

- No permanent exactly-once claim; caller retains prices/redirects/access; unknown outcome has explicit recover/needs-review state.

**Required verification:** Catalog schema validation and deterministic generation; adversarial contract review

## A05 — Implement customer and checkout coordination

- Repository: `go`; lane: `amsl-billing`.
- Dependencies: A04.
- Owned paths: `billing/checkout/`, `docs/customer-checkout.md`.
- Reviewer: security; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Implement frozen attempt lifecycle and PostgreSQL storage using maintained Stripe SDK adapter.
2. Claim provider work transactionally, call provider outside DB transaction and fence commit.
3. Add recovery and bounded retry/backoff without creating a new attempt after ambiguous timeout.

Acceptance:

- Same attempt stable; changed payload conflicts; concurrent processes do not silently create independent customer operations; expired idempotency outcomes require reconciliation.

**Required verification:** go test -race ./billing/checkout/... with real PostgreSQL and deterministic lost-response provider fixtures

## A06 — Dogfood checkout and portal routes

- Repository: `ferro`; lane: `hosted-billing`.
- Dependencies: A05, F03, F02, F04.
- Owned paths: `internal/hosted/billing/checkout.go`, `internal/hosted/billing/checkout_test.go`, `internal/hosted/billing/portal.go`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Consume immutable AMSL pin; derive customer/price/return URLs from trusted server/account state.
2. Wire real web checkout/portal/status APIs and already-subscribed recovery.
3. Verify test-mode session create and redirect without granting entitlement.

Acceptance:

- Cross-account/price/return spoofing rejected; duplicate clicks reuse one active attempt; portal belongs to signed-in customer.

**Required verification:** go test ./internal/hosted/billing -run TestCheckout; Stripe test-mode flow recorded; exercise actual durable web sessions and CSRF middleware, including unauthenticated, missing/invalid CSRF and cross-account denial

## A07 — Freeze subscription reconciliation contract

- Repository: `capabilities`; lane: `amsl-billing`.
- Dependencies: A04.
- Owned paths: `capabilities/billing.subscription-reconciliation.json`, `docs/contracts/subscription-reconciliation.md`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Specify verified inbox identity, customer mapping, lease epoch, current provider projection and transaction boundaries.
2. Specify duplicate/out-of-order/unmatched event recovery and sweeps; avoid timestamp-only ordering.
3. Keep entitlement/grace/tier predicates outside package.

Acceptance:

- Unknown customer events remain retryable; stale workers cannot overwrite current projection; provider mode/account is part of event binding.

**Required verification:** Catalog validation and failure-state design review

## A08 — Implement verified inbox and reconciliation

- Repository: `go`; lane: `amsl-billing`.
- Dependencies: A07.
- Owned paths: `billing/subscription/`, `docs/subscription-reconciliation.md`.
- Reviewer: security; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Implement durable accept/claim/reconcile/project/sweep API and official Stripe adapter.
2. Verify raw payload using SDK in ingress adapter; atomic inbox commit before acknowledgment.
3. Add bounded retry, dead-letter metadata, lease fencing and missing-webhook repair.

Acceptance:

- Crash/replay/reorder/race tests converge to current provider state; wrong account/mode/signature cannot affect projection.

**Required verification:** go test -race ./billing/subscription/... with PostgreSQL and provider fixtures

## A09 — Dogfood subscription projection in Ferro

- Repository: `ferro`; lane: `hosted-billing`.
- Dependencies: A08, A06.
- Owned paths: `internal/hosted/billing/webhook.go`, `internal/hosted/billing/reconcile.go`, `internal/hosted/billing/reconcile_test.go`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Wire signed webhook route, durable reconciliation worker and scheduled sweep.
2. Consume immutable module pin and map customer identity only from trusted binding.
3. Expose billing status/freshness for website and task admission.

Acceptance:

- Real Stripe test-mode duplicate/reordered/cancel/payment-failure flows update correct account; redirect is never payment evidence.

**Required verification:** go test ./internal/hosted/billing -run TestReconcile; provider test-mode evidence

## A10 — Document existing-library identity adoption

- Repository: `capabilities`; lane: `amsl-identity`.
- Dependencies: D05.
- Owned paths: `capabilities/identity.sessions.json`, `capabilities/identity.password-hashing.json`, `docs/recipes/browser-sessions.md`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Record why source JWT/passkey/password flows are not one reusable contract.
2. Reference OIDC verification and SCS/PostgreSQL integration, including expiry/revoke/CSRF tests Ferro must run.
3. Keep password hashing reference-only for this passwordless launch.

Acceptance:

- No new universal auth/user package or invented crypto; catalog distinguishes library reference from implemented AMSL behavior.

**Required verification:** Catalog validation and recipe review against F03/F04 test requirements

## A11 — Implement reusable credential-free Go validation

- Repository: `workflows`; lane: `delivery`.
- Dependencies: D03.
- Owned paths: `.github/workflows/go-validation.yml`, `tests/go-validation/`, `docs/go-validation.md`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Use workflow_call with bounded typed inputs; pin action/tool SHAs and minimal permissions.
2. Run fmt/vet/test/race without arbitrary shell hooks, secret inheritance or privileged fork execution.
3. Preserve callers' trigger/concurrency ownership and stable aggregate result contract.

Acceptance:

- Malformed module path/timeouts rejected; a failing test makes caller fail; no elevated token or long-lived secret.

**Required verification:** actionlint plus workflow fixture/static checks; actual caller runs are A12

## A12 — Dogfood Go validation in two real modules

- Repository: `ferro+go`; lane: `delivery`.
- Dependencies: A11, A02, F01.
- Owned paths: `ferro:.github/workflows/ci.yml`, `go:.github/workflows/ci.yml`, `docs/launch/evidence/A12.md`.
- Reviewer: coordinator; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Pin immutable reusable workflow in Ferro and AMSL Go; preserve product-specific Chrome/Postgres/security checks.
2. Record real successful runs in both repos and an intentionally failing test on a disposable branch.
3. Verify required-check names and merge_group behavior, then remove deliberate failure.
4. Coordinate the two caller lockfiles/check-name edits explicitly; preserve each repository own non-Go gates.

Acceptance:

- Two real module invocations demonstrated; baseline gates not weakened; no synthetic-only reuse claim.

**Required verification:** GitHub Actions run URLs and exact caller/callee SHAs; check-required-status verification

## A13 — Implement AWS deployment identity component

- Repository: `pulumi`; lane: `infrastructure`.
- Dependencies: D05, D07.
- Owned paths: `components/aws/deployment-identity/`, `docs/aws-deployment-identity.md`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Use existing or explicitly new GitHub OIDC provider; generate exact audience/subject trust from frozen contract.
2. Separate artifact/deploy roles and bounded resource policies; limit PassRole.
3. Return safe identity outputs and import instructions.

Acceptance:

- Wrong repo/ref/environment denied by trust shape; no blanket admin or duplicate shared provider creation.

**Required verification:** Pulumi unit/mocks + JSON policy assertions

## A14 — Add deployment-identity enforcement tests

- Repository: `pulumi`; lane: `infrastructure`.
- Dependencies: A13.
- Owned paths: `policy/aws-deployment-identity/`, `tests/aws-deployment-identity/`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Test raw-role bypasses, wildcard trust, wrong audience, unsafe role delegation and environment mismatch.
2. Define policy attachment and audit outputs separately from construction.
3. Prepare disposable live federation fixture, no auto-apply.

Acceptance:

- Policy rejects unsafe raw and component resources; exact allowed contexts pass; live evidence still explicitly pending.

**Required verification:** Pulumi policy tests and synthetic trust fixtures

## A15 — Dogfood deployment identity in Ferro CI

- Repository: `ferro`; lane: `delivery`.
- Dependencies: A14, I01.
- Owned paths: `infra/deployment_identity.go`, `.github/workflows/deploy.yml`, `docs/launch/evidence/A15.md`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Consume immutable Pulumi component and wire environment-specific GitHub deploy role.
2. Verify authorized qualification federation and denied wrong-context federation.
3. Record actual branch/environment protection and least-privilege resource access.

Acceptance:

- No stored cloud access key; untrusted PR never receives deployment credentials; pipeline role cannot edit unrelated resources.

**Required verification:** Disposable AWS federation checks and CI run evidence

## A16 — Implement private AWS PostgreSQL component

- Repository: `pulumi`; lane: `infrastructure`.
- Dependencies: D05, D07.
- Owned paths: `components/aws/private-database/`, `docs/aws-private-database.md`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Implement approved private network/SG/encryption/secret/backup/deletion inputs with explicit cost and availability settings.
2. Export TLS/secret-reference/network outputs; support existing-resource import.
3. Keep runtime DDL permission separate from migration role.

Acceptance:

- No public DB, unrestricted ingress or secret output; backup/deletion choices visible; parameter/version pin is explicit.

**Required verification:** Pulumi mock/resource-property tests

## A17 — Add database policy and failure tests

- Repository: `pulumi`; lane: `infrastructure`.
- Dependencies: A16.
- Owned paths: `policy/aws-private-database/`, `tests/aws-private-database/`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Reject public/unencrypted/unbacked-up/raw bypass resources and plaintext outputs.
2. Test allowed explicit availability/deletion profiles and import behavior.
3. Prepare restore fixture that consumes exported outputs.

Acceptance:

- Mocks and enforcement tests are labeled separately; production enforcement not inferred from policy-file existence.

**Required verification:** Pulumi policy suite + resource output secret checks

## A19 — Wire database component into Ferro infrastructure

- Repository: `ferro`; lane: `infrastructure`.
- Dependencies: A17, I02, F02.
- Owned paths: `infra/database.go`.
- Reviewer: security; initial size: 2-4 agent hours; status: planned.

Implementation:

1. Consume the immutable database component and wire its outputs into Ferro connection, TLS, secret and migration-role configuration.
2. Verify the composition with Pulumi mocks/policies and a reviewed preview; hand the outputs to I03 for deployment.

Acceptance:

- Runtime and migration roles remain distinct; connection configuration consumes actual component outputs rather than duplicated constants.
- This task completes wiring only; AMSL adoption and restore evidence remain incomplete until A18.

**Required verification:** Pulumi composition mock/policy checks and reviewed resource preview; no live adoption claim

## A18 — Dogfood database through Ferro deployment and restore

- Repository: `ferro`; lane: `infrastructure`.
- Dependencies: A19, I03, F13.
- Owned paths: `docs/launch/evidence/A18.md`.
- Reviewer: security; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Verify the deployed Ferro consumes the immutable database component outputs for connection/TLS/secret config; use the I03 deployment path, not ad hoc provisioning.
2. Test denied public connection, app least privilege and migration role separation.
3. Restore a disposable copy and prove deletion/revocation tombstones survive before traffic.

Acceptance:

- Real consumer TLS and restore meet readiness targets; raw app credentials never printed; infrastructure policy attachment verified.

**Required verification:** Disposable cloud DB checks and timed restore evidence

## F01 — Scaffold hosted composition without changing local mode

- Repository: `ferro`; lane: `hosted-core`.
- Dependencies: D05.
- Owned paths: `cmd/ferro-cloud/`, `internal/hosted/server/`, `internal/hosted/config/`.
- Reviewer: coordinator; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Create hosted command, validated config, route composition seams and graceful shutdown.
2. Add liveness/readiness and injectable stores/provider transports.
3. Leave local Owner/Unix election/token behavior local.

Acceptance:

- Cloud command never launches Chrome or uses local shared bearer; missing required production config fails startup.

**Required verification:** go test ./internal/hosted/config ./internal/hosted/server ./cmd/ferro-cloud; local regression suite

## F02 — Implement Ferro-owned browser/task persistence

- Repository: `ferro`; lane: `hosted-core`.
- Dependencies: F01.
- Owned paths: `internal/hosted/store/`, `migrations/hosted/`, `tests/postgres/`.
- Reviewer: security; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Implement only D05-assigned Ferro-owned browser/task/device state and persistence boundaries.
2. Consume tenant identity and provider/admission context from the qualified profile; do not duplicate profile-owned account, login, billing or migration state.
3. Provide disposable fixtures and recovery/compatibility evidence for Ferro-owned state.

Acceptance:

- Tenant-scoped task/device guarantees are tested; no global unscoped child lookup; no duplicate profile-owned tenant state or application migration.

**Required verification:** Tenant-scoped task/device persistence tests with the qualified store contract and recovery fixtures; no application-schema migration claim

## F03 — Integrate qualified application authentication context

- Repository: `ferro`; lane: `hosted-identity`.
- Dependencies: F02, A10.
- Owned paths: `internal/hosted/auth/oidc.go`, `internal/hosted/auth/principal.go`, `internal/hosted/auth/oidc_test.go`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Consume the D08/D05 versioned authentication context from the generated application profile through its supported adapter.
2. Keep customer identity creation, login callbacks, sessions and account lifecycle in the authoritative application profile; do not add a Ferro-owned login or credential store.
3. Bind each browser task to the authenticated tenant/device context and reject missing, stale or cross-tenant context.

Acceptance:

- Only the qualified profile can establish customer identity; stale, missing and cross-tenant contexts are denied without granting browser action authority.

**Required verification:** Consumer adapter contract checks plus actual qualified-profile context journey recorded later at Q01

## F04 — Consume qualified application session lifecycle

- Repository: `ferro`; lane: `hosted-identity`.
- Dependencies: F03.
- Owned paths: `internal/hosted/auth/sessions.go`, `internal/hosted/auth/csrf.go`, `internal/hosted/auth/sessions_test.go`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Integrate the qualified profile's session and request-integrity boundary through the versioned contract; do not create Ferro-owned web sessions.
2. Honor logout, revoke, expiry, disabled/deleted tenant and recent-auth results from the authoritative profile.
3. Keep web/account operations distinct from device/browser-operation authority.

Acceptance:

- Profile session revocation/expiry and request-integrity denials propagate to Ferro; no web credential by itself authorizes browser mutation.

**Required verification:** Consumer adapter checks for profile session expiry/revocation and request-integrity outcomes; no app-owned session-store claim

## F05 — Implement extension device linking and revocation

- Repository: `ferro`; lane: `hosted-identity`.
- Dependencies: F04, A03.
- Owned paths: `internal/hosted/devices/link.go`, `internal/hosted/devices/registry.go`, `internal/hosted/devices/link_test.go`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Implement frozen first-party code/PKCE exchange, exact extension redirect allowlist and atomic consumption.
2. Issue scoped device credential once; bind Chrome session nonce and account/device metadata.
3. Wire device revoke/list and lost-exchange relink behavior.

Acceptance:

- Wrong verifier/extension/account, reused/expired code fail; link response never logs secret; old device cannot reply after revoke.

**Required verification:** go test ./internal/hosted/devices -run TestLink; two-process consume/revoke cases

## F06 — Implement fenced pairing and command transport

- Repository: `ferro`; lane: `hosted-browser`.
- Dependencies: F02, F05.
- Owned paths: `internal/hosted/browser/`, `internal/hosted/commands/`.
- Reviewer: security; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Implement explicit pair generation and account/device checks on next/reply; one active poll with bounded heartbeat.
2. Persist dispatch before send; validate command/task/worker epoch/origin/deadline on reply.
3. Handle duplicate/late/conflicting replies and response-loss uncertainty without redelivery.

Acceptance:

- No foreign/stale command delivery; pair change cannot interrupt dispatched action unnoticed; two replicas share authoritative state.

**Required verification:** go test -race ./internal/hosted/browser/... ./internal/hosted/commands/... against PostgreSQL fault fixtures

## F07 — Implement durable task admission and worker lifecycle

- Repository: `ferro`; lane: `hosted-tasks`.
- Dependencies: F02.
- Owned paths: `internal/hosted/tasks/`, `internal/hosted/events/`.
- Reviewer: security; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Implement frozen task transitions, idempotent create/fingerprint, worker lease/epoch and bounded event log.
2. Add durable cancel/deadlines and actor-bound status/events.
3. Make worker loss before dispatch recoverable and after dispatch uncertain.

Acceptance:

- Concurrent duplicate creates produce one task; conflicting intents 409; stale worker cannot append/dispatch; cancel idempotent and cross-user denied.

**Required verification:** go test -race ./internal/hosted/tasks/... ./internal/hosted/events/... using two-process crash fixtures

## F08 — Adapt existing executor to hosted task and policy

- Repository: `ferro`; lane: `hosted-browser`.
- Dependencies: F06, F07.
- Owned paths: `internal/hosted/execution/`, `internal/hosted/driver/`.
- Reviewer: security; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Implement PageDriver over durable command bridge and per-task isolated runner/snapshot context.
2. Preserve fresh target signatures, read-only gate, origin checks, bounded planner passes and uncertainty.
3. Wire human action confirmation; unsupported/ambiguous sensitive actions pause or refuse.

Acceptance:

- Never share model/page/snapshot state across accounts; malicious page/model output cannot widen authority; local executor regression remains green.

**Required verification:** go test ./internal/hosted/execution/... ./internal/hosted/driver/...; actual Chrome fixture command flow Q01

## F09 — Implement encrypted OpenRouter key lifecycle

- Repository: `ferro`; lane: `hosted-model`.
- Dependencies: F02.
- Owned paths: `internal/hosted/modelkeys/`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Use maintained envelope/KMS implementation and account/provider/version context.
2. Implement key presence, test, replace/delete and cache invalidation without returning raw key.
3. Bound use to configured OpenRouter endpoint and role-scoped KMS access.

Acceptance:

- Ciphertext swap across accounts fails; KMS/DB failure denies; logs/errors/exports contain no key; deleted/replaced key never reused.

**Required verification:** go test ./internal/hosted/modelkeys/... including KMS-adapter contract and redaction fixtures

## F10 — Wire bounded model client and provider errors

- Repository: `ferro`; lane: `hosted-model`.
- Dependencies: F09, F08.
- Owned paths: `internal/hosted/model/`, `internal/hosted/execution/model.go`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Resolve fixed OpenRouter base/model allowlist and decrypt only for authorized task.
2. Set timeout/token/cost ceilings, cancel propagation and redirect restrictions; reuse existing planner schema.
3. Map provider 401/429/5xx/timeouts to safe UI codes; redact page/model diagnostics.

Acceptance:

- No arbitrary outbound URL or key forwarded to another host; no idle model calls or automatic new task retry.

**Required verification:** go test ./internal/hosted/model/... with redirect/SSRF/rate/timeout/cancel fixtures

## F11 — Honor authoritative paid admission at task boundary

- Repository: `ferro`; lane: `hosted-billing`.
- Dependencies: F07.
- Owned paths: `internal/hosted/entitlements/`, `internal/hosted/tasks/admission_billing.go`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Consume the generated application's versioned paid-admission result; do not reconstruct subscription policy from checkout redirects or AMSL package state.
2. Bind admission to tenant, device, task limits and expiry; keep browser origin and action grants in Ferro's separate policy checks.
3. Define behavior for expired/revoked admission and already-admitted work from the qualified contract.

Acceptance:

- Missing, stale, denied or cross-tenant paid admission fails closed; paid status never grants origin access or browser-action authority.

**Required verification:** Admission adapter cases for qualified allow/deny/stale/tenant outcomes; provider lifecycle evidence remains owned by the profile qualification

## F12 — Add account quotas and abuse backpressure

- Repository: `ferro`; lane: `hosted-tasks`.
- Dependencies: F11.
- Owned paths: `internal/hosted/limits/`, `internal/hosted/tasks/admission_limits.go`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Implement transactionally reserved one-active-task/account and frozen per-account/global rate limits.
2. Bound queues/payloads/task steps and release reservations on terminal states/crash.
3. Return stable 429/Retry-After and enforce anonymous link/auth rate controls.

Acceptance:

- Two replicas cannot exceed account concurrency through races; disconnected/canceled tasks release reservations; memory/DB growth bounded.

**Required verification:** go test -race ./internal/hosted/limits/... with shared-DB parallel admission

## F13 — Integrate application-owned export and deletion lifecycle

- Repository: `ferro`; lane: `hosted-account`.
- Dependencies: F04, F05, F09, F12.
- Owned paths: `internal/hosted/account/`, `internal/hosted/retention/`.
- Reviewer: security; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Use the qualified application's export/deletion lifecycle and recent-auth result; Ferro may export only its own browser/task data under the contract.
2. Propagate account disable, device revoke, task stop and provider deletion-pending outcomes without implementing a second billing cancellation ledger.
3. Apply deletion/revocation outcomes to Ferro-owned transient data and recovery state as D05 specifies.

Acceptance:

- Cross-tenant export is denied; disabled/deleted tenants cannot run or relink; provider deletion-pending state remains visible; recovery cannot resurrect revoked access.

**Required verification:** go test ./internal/hosted/account/... ./internal/hosted/retention/... with provider/DB failure injection

## F14 — Instrument redacted health and operational events

- Repository: `ferro`; lane: `observability`.
- Dependencies: F01, F07, A09.
- Owned paths: `internal/hosted/telemetry/`, `docs/operations/metrics.md`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Add structured allowlisted logs, request/task IDs and metrics for task state, reply uncertainty, reconcile backlog and auth failures.
2. Redact page text, prompt, key, URL query, headers and account email.
3. Bound labels/cardinality and document health checks and secret-free diagnostics.

Acceptance:

- Canary secrets and page content never appear in logs/traces/errors; metrics do not create unbounded account labels.

**Required verification:** go test ./internal/hosted/telemetry/... with log/trace canary scan

## F15 — Implement audited operator controls and kill switches

- Repository: `ferro`; lane: `hosted-operations`.
- Dependencies: F13, F14, F12.
- Owned paths: `cmd/ferro-cloud-admin/`, `internal/hosted/admin/`, `.github/workflows/operate.yml`.
- Reviewer: security; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Implement narrowly scoped account-disable/device-revoke and global stop-admission/mutation controls using existing lifecycle services; no public admin HTTP endpoint.
2. Run commands through a protected maintenance workflow/task role with verified operator/deployment identity and append-only redacted audit event; no unaudited direct database edits.
3. Require explicit account/task targets and dry-run preview; exclude transcript export, provider-key retrieval, arbitrary SQL or impersonation.

Acceptance:

- Unprivileged runtime cannot invoke maintenance controls; targeted account revoke cancels its tasks and leaves others running.
- Every maintenance mutation records actor/action/resource/result; kill switches stop new dispatch without deleting customer data.

**Required verification:** go test ./internal/hosted/admin/... ./cmd/ferro-cloud-admin; protected workflow denied-path and qualification kill-switch drill

## X01 — Separate local and hosted extension transports

- Repository: `ferro`; lane: `extension`.
- Dependencies: D05, D06.
- Owned paths: `extension/transport-local.js`, `extension/transport-hosted.js`, `extension/transport.test.cjs`.
- Reviewer: standard; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Define explicit mode/endpoint config with production HTTPS fixed allowlist and dev config separate.
2. Implement hosted poll/reply/status transport with auth, timeouts, version header and backoff.
3. Preserve local mode compatibility via adapter fixtures.

Acceptance:

- No arbitrary hosted base or credential fallback; local tests pass; one bounded poll/no idle task calls.

**Required verification:** node --test extension/transport.test.cjs

## X02 — Implement extension sign-in and credential storage

- Repository: `ferro`; lane: `extension`.
- Dependencies: X01, F05.
- Owned paths: `extension/auth.js`, `extension/auth.test.cjs`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Use identity web flow with stable callback, random state and PKCE; exchange only approved endpoint.
2. Store device secret in trusted extension storage, isolate from page messages and clear on logout/revoke/expiry.
3. Expose linking/offline/relink UI states without copying tokens.

Acceptance:

- Wrong state/redirect/code denied; browser restart restores only allowed state; secrets never appear in content-script messages.

**Required verification:** node --test extension/auth.test.cjs; real Chrome sign-in journey Q01

## X03 — Implement explicit hosted pairing lifecycle

- Repository: `ferro`; lane: `extension`.
- Dependencies: X02, F06.
- Owned paths: `extension/pairing.js`, `extension/pairing.test.cjs`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Bind selected tab+session nonce+server generation; distinguish active Chrome tab from paired tab.
2. Handle navigation/close/discard/update/offline and refuse switch until stop/uncertainty resolved.
3. Show paired site consistently across side-panel tab navigation.

Acceptance:

- Switching tabs never silently changes authority; same numeric tab after restart cannot adopt old command/snapshot.

**Required verification:** node --test extension/pairing.test.cjs; browser navigation/generation fixtures

## X04 — Implement typed dispatch validation and dedup journal

- Repository: `ferro`; lane: `extension`.
- Dependencies: X03, F08.
- Owned paths: `extension/hosted-dispatch.js`, `extension/hosted-dispatch.test.cjs`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Validate packaged operation schema, origin/generation/deadline/target/confirmation before content dispatch.
2. Journal command in session storage before action; reject duplicate/conflicting command and stale worker epoch.
3. Package all executable code; no eval, remote script or arbitrary CDP pass-through.

Acceptance:

- Duplicate or lost replies never reexecute a mutation; worker restart stops with uncertainty; forged content messages cannot execute.

**Required verification:** node --test extension/hosted-dispatch.test.cjs; actual Chrome crash/replay fixtures Q02

## X05 — Minimize permissions and page data capture

- Repository: `ferro`; lane: `extension`.
- Dependencies: X04.
- Owned paths: `extension/permissions.js`, `extension/permissions.test.cjs`, `extension/adapter.js`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Implement optional explicit-origin grants and injection on approved paired pages; deny off-origin redirects before DOM capture.
2. Exclude password/OTP/payment fields and minimize snapshot content.
3. Validate frame/popup/file/canvas limitations and accurate error text.

Acceptance:

- Revoked host permission stops read/action; no background all-tabs scrape; local and hosted snapshot parity preserved.

**Required verification:** node --test extension/permissions.test.cjs; browser permission/restricted-page tests

## X06 — Wire trusted confirmation and cancellation UI

- Repository: `ferro`; lane: `extension-ui`.
- Dependencies: X04, D06.
- Owned paths: `extension/task-controls.js`, `extension/task-controls.test.cjs`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Render bounded task start consent and action-specific confirmation digest state.
2. Send confirmations only from trusted UI gestures; never from content/model messages.
3. Show cancel requested vs canceled vs uncertain; require inspect before retry.

Acceptance:

- Page cannot approve itself; old target/generation/expired confirmation rejected; double clicks not duplicate actions.

**Required verification:** node --test extension/task-controls.test.cjs; real side-panel consent/cancel checks

## X07 — Wire hosted side-panel states and account history

- Repository: `ferro`; lane: `extension-ui`.
- Dependencies: X05, X06, F10, F11.
- Owned paths: `extension/sidepanel.js`, `extension/sidepanel.html`, `extension/sidepanel.css`, `extension/history.js`, `extension/background.js`.
- Reviewer: coordinator; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Integrate auth/pair/model/paid/online states and server event cursors.
2. Retain floating glass design, keyboard/focus/reduced motion and bounded local archive partitioned by account.
3. Handle refresh/restart without resubmitting a task; show site/model/permission disclosure.
4. As sole extension composition owner, wire the packaged auth/transport/pairing/dispatch/control modules into background.js and the existing local flow; modules that are never called are unfinished.

Acceptance:

- No previous-account transcript leak; every error/revoke/payment/offline path has accurate actionable state.
- Untrusted model/page/history rendering cannot execute HTML/JS or load remote tracking images; URLs are scheme-validated.

**Required verification:** Hosted UI fixture tests; local side-panel regression and accessibility checks

## X08 — Build store-ready deterministic extension artifact

- Repository: `ferro`; lane: `extension`.
- Dependencies: X07, W03.
- Owned paths: `extension/manifest.json`, `scripts/package-extension.py`, `docs/store/`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Set stable publisher ID/version and minimal justified permissions; bundle all dependencies locally.
2. Generate clean ZIP from allowlisted files with hash and source SHA; exclude tests/dev URLs/secrets.
3. Prepare real screenshots/reviewer instructions and protocol compatibility table.

Acceptance:

- Artifact installs fresh/upgrades correctly; no remote executable content; permissions/privacy match actual code.

**Required verification:** ZIP content/hash validation; unpacked install and upgrade tests; store-reviewed install at R02

## W01 — Implement public website and onboarding shell

- Repository: `ferro`; lane: `web`.
- Dependencies: D05, D06.
- Owned paths: `web/templates/landing.html`, `web/templates/onboarding.html`, `web/static/onboarding.js`, `web/embed.go`, `internal/hosted/web/render.go`.
- Reviewer: standard; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Build product landing/onboarding routes from frozen UI states and actual supported features.
2. Guide sign-in -> subscription -> install -> connect -> key -> first task without requiring local Go.
3. Use fixture API client until real routes integrated; no hardcoded claims or live prices.

Acceptance:

- Keyboard/mobile/narrow viewport usable; no invented deployment/model/store status.

**Required verification:** HTML/JS checks and deterministic UI fixture test

## W02 — Implement account and subscription management UI

- Repository: `ferro`; lane: `web`.
- Dependencies: W01, F04, A06, A09, F13.
- Owned paths: `web/templates/account.html`, `web/static/account.js`, `web/templates/billing.html`, `internal/hosted/web/routes.go`.
- Reviewer: coordinator; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Wire actual account/device revoke/model presence/export/delete and Stripe checkout/portal status.
2. Show deletion pending, past-due/expired/cancel-at-period-end and provider error recovery.
3. Never render raw provider key or trust querystring payment success.
4. Register page/API interaction routes through the coordinator-owned server root and embed templates/assets from the actual release binary.

Acceptance:

- Actual endpoint journey works; unpaid customers can manage/cancel/export; UI and server policy agree.

**Required verification:** Website integration tests against real API and provider fixtures

## W03 — Write approved privacy, terms and support content

- Repository: `ferro`; lane: `web`.
- Dependencies: W02, D07, X05.
- Owned paths: `web/templates/privacy.html`, `web/templates/terms.html`, `web/templates/support.html`, `docs/store/disclosures.md`.
- Reviewer: maintainer; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Describe selected page/prompt flow, provider/key handling, consent, retention, deletion and unsupported automation.
2. Obtain owner review for price/tax/refund/support statements; prepare store questionnaire mappings.
3. Publish accurate incident/support contact and FAQ without private infrastructure details.

Acceptance:

- No mismatch among website, store and runtime; support links usable; legal/commercial owner signs evidence.

**Required verification:** Content review plus link/HTML checks; no claim of legal approval by coding agent

## I01 — Create infrastructure project and artifact pipeline

- Repository: `ferro`; lane: `infrastructure`.
- Dependencies: F01, D07.
- Owned paths: `infra/Pulumi.yaml`, `infra/go.mod`, `infra/main.go`, `Dockerfile`, `.github/workflows/artifact.yml`.
- Reviewer: security; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Define isolated qualification/production stack inputs and imports; reuse existing Actions before new AMSL wrapper.
2. Build non-root read-only image from reviewed source with SBOM, scans, signing and digest outputs.
3. Preserve least privilege and no secrets in image/build context.

Acceptance:

- Verified immutable digest tied to source; failed scan/test cannot promote; local/customer files excluded.

**Required verification:** Container smoke test, scan/signature verification, Pulumi config validation

## I02 — Compose network, HTTPS, compute and secrets

- Repository: `ferro`; lane: `infrastructure`.
- Dependencies: I01, A13, A16.
- Owned paths: `infra/network.go`, `infra/compute.go`, `infra/secrets.go`, `infra/dns.go`, `infra/observability.go`.
- Reviewer: security; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Compose component outputs with ECS/ALB/ECR/KMS/Secrets Manager, explicit outbound access and private DB.
2. Define min/max/pools/draining/health, scoped runtime/migration roles, DNS/TLS and alarm destinations.
3. Produce resource/cost/import diff; no shared product staging or unrelated resource mutation.

Acceptance:

- Runtime cannot admin/migrate/read unrelated secrets; app connects through consumed component outputs; no public DB.

**Required verification:** Pulumi mock/policy checks, reviewed live preview and priced resource list

## I03 — Implement migration, deploy and rollback execution

- Repository: `ferro`; lane: `delivery`.
- Dependencies: A15, A19, I02, F14.
- Owned paths: `scripts/deploy-cloud.sh`, `scripts/migrate-cloud.sh`, `scripts/verify-release.sh`, `docs/operations/release.md`.
- Reviewer: security; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Run one migration job under separate role, then deploy verified digest with environment concurrency lock.
2. Check rollout completion, public health and protocol compatibility; stop on failed gates.
3. Implement previous-image rollback and test schema backward compatibility.

Acceptance:

- Pulumi apply or image push alone never marks success; same release operator/lock spans deployment; rollback drills recorded.

**Required verification:** Disposable qualification deploy/fail/rollback and old-image schema compatibility

## Q01 — Verify two-user hosted end-to-end journey

- Repository: `ferro`; lane: `qualification`.
- Dependencies: F10, F12, X07, W02, I03, X08.
- Owned paths: `tests/hosted/e2e/`, `docs/launch/evidence/Q01.md`.
- Reviewer: independent-security; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Use two controlled Chrome profiles/users and real HTTPS/Google/provider qualification credentials.
2. Complete independent read-only and approved interaction tasks with no local Go; navigate, revoke and cancel.
3. Verify resource isolation across all routes and no key/page leakage in telemetry.

Acceptance:

- Readiness Gate 1 passed with real extension/API/DB/provider evidence; fixture-only results labeled separately.

**Required verification:** FERRO_TEST_HOSTED=1 test harness; manual qualification scripts and sanitized results

## Q02 — Verify restart, replay and failure semantics

- Repository: `ferro`; lane: `qualification`.
- Dependencies: Q01.
- Owned paths: `tests/hosted/faults/`, `docs/launch/evidence/Q02.md`.
- Reviewer: independent-security; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Inject response loss/crashes at dispatch/reply/commit and extension worker/browser restart.
2. Test duplicate create/reply, stale lease/generation, revoke/cancel/deadline races and offline recovery.
3. Assert no automatic reexecution of delivered mutations and no cross-user result delivery.

Acceptance:

- Readiness Gate 3 fault cases pass; uncertainty surfaced consistently; no hidden retry queue.

**Required verification:** Deterministic two-replica+Chrome fault suite and executed failure matrix

## Q03 — Verify full paid lifecycle and reconciliation

- Repository: `ferro`; lane: `qualification`.
- Dependencies: Q01, A09, W02.
- Owned paths: `tests/hosted/billing/`, `docs/launch/evidence/Q03.md`.
- Reviewer: independent-security; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Run Stripe test-mode checkout, duplicate/out-of-order/missing events, renewal failure, cancel, portal and deletion.
2. Verify two-instance checkout race and unknown result recovery; actual task admission follows committed projection.
3. Prepare separately approved bounded live payment/cancel/refund test for release owner.

Acceptance:

- Gate 2 test-mode evidence complete; live-payment evidence not claimed until observed; active/canceled/past_due behavior matches public policy.

**Required verification:** Stripe CLI/test clocks where supported; API/UI task-admission integration suite

## Q04 — Verify retention, restore and key rotation

- Repository: `ferro`; lane: `qualification`.
- Dependencies: Q02, F13, A18.
- Owned paths: `tests/hosted/recovery/`, `docs/operations/recovery.md`, `docs/launch/evidence/Q04.md`.
- Reviewer: security; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Restore isolated backup with KMS references and reapply deletion/revocation tombstones before traffic.
2. Measure RPO/RTO and verify TLS/runtime permission boundaries.
3. Rotate app/provider encryption secrets and reconcile canceled accounts without resurrection.

Acceptance:

- Recovery targets measured; no deleted/revoked account regains access; restore resources removed via IaC.

**Required verification:** Timed restore and key-rotation drills with sanitized evidence

## Q05 — Measure load, idle resource use and soak

- Repository: `ferro`; lane: `qualification`.
- Dependencies: Q02, F14.
- Owned paths: `tests/hosted/load/`, `docs/launch/evidence/Q05.md`.
- Reviewer: coordinator; initial size: 4-8 agent hours; status: planned.

Implementation:

1. Run declared 100-device/25-task load, bounded global saturation and one-replica loss.
2. Measure idle extension vs baseline for 30min; run 24h bounded soak.
3. Record API/cancel latency, DB pools/queue bounds, uncertainty, cost and actual alerts.

Acceptance:

- Targets met or formally revised before release; no leak/unbounded growth; no invented performance comparison.

**Required verification:** Versioned load/soak scripts; actual metrics/cost results; alert-delivery evidence

## Q06 — Verify supported browsers and accessibility

- Repository: `ferro`; lane: `qualification`.
- Dependencies: X08, Q01.
- Owned paths: `tests/hosted/browser-matrix/`, `docs/launch/evidence/Q06.md`.
- Reviewer: coordinator; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Test fresh/upgrade/store-ID callbacks on macOS and Windows Chrome plus minimum fixture version.
2. Exercise keyboard, focus, screen reader status, contrast, reduced motion, zoom and narrow side panel.
3. Check restricted pages, closed/discarded tab, multiple windows, browser restart and unsupported capability messages.

Acceptance:

- Supported matrix documented honestly; no runtime authority switches with active tab; local mode still usable.

**Required verification:** Browser matrix and accessibility checklist with versioned screenshots/results

## Q07 — Complete independent security and supply-chain review

- Repository: `ferro`; lane: `qualification`.
- Dependencies: Q02, Q03, Q04, Q06, F15.
- Owned paths: `docs/launch/evidence/Q07.md`, `docs/security/hosted-threat-model.md`.
- Reviewer: independent-security; initial size: 6-12 agent hours; status: planned.

Implementation:

1. Independently review auth/linking/IDOR/CSRF/XSS/SSRF/prompt injection/payment/keys/fencing/IAM with denied-path tests.
2. Review package provenance, dependency/container scans, SBOM and public AMSL evidence.
3. Fix blocking findings and rerun affected consumer suites; record explicit accepted low risk only.

Acceptance:

- No open critical/high or medium affecting authority/data/payments/duplicate effects; exact candidate hashes reviewed.

**Required verification:** Independent review report, exploit/regression tests and scan triage

## Q08 — Close AMSL adoption evidence and catalog

- Repository: `capabilities`; lane: `coordinator`.
- Dependencies: A03, A06, A09, A10, A12, A15, A18, Q01, Q03.
- Owned paths: `capabilities/`, `docs/evidence/`, `catalog.json`.
- Reviewer: maintainer; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Update each intended capability with actual implementation/version/tests and consumer verification limits.
2. Record extraction/adoption effort, removed duplication, regressions and second-consumer evidence.
3. Run schema/catalog checks and seek explicit maintainer lifecycle decision; no automatic stable promotion.

Acceptance:

- Every claimed component has real Ferro consumption; references are not mislabeled packages; public records sanitized.

**Required verification:** python3 tools/catalog.py validation/generation using repository-documented commands; evidence review

## R01 — Complete support, policy and release rehearsal

- Repository: `ferro`; lane: `release`.
- Dependencies: Q03, Q04, Q05, Q06, W03, F15.
- Owned paths: `docs/operations/support.md`, `docs/operations/incident.md`, `docs/launch/evidence/R01.md`.
- Reviewer: maintainer; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Rehearse account/device revoke, billing correction, uncertain action, outage and deletion support.
2. Verify alert recipient delivery, scoped admin access, audit records and stop-admission/mutation switches.
3. Get owner signoff on commercial/retention/support content and prepared live qualification.

Acceptance:

- Operator can execute runbooks; no broad transcript/key access; critical alerts actually arrive.

**Required verification:** Observed tabletop/safe-failure drill and owner signoff

## R02 — Submit and qualify public Chrome Web Store build

- Repository: `ferro`; lane: `release`.
- Dependencies: X08, Q06, W03, Q07.
- Owned paths: `docs/store/submission.md`, `docs/launch/evidence/R02.md`.
- Reviewer: maintainer; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Prepare exact ZIP/hash, listing assets, privacy/permission explanations and reviewer account/steps.
2. Submit through authorized publisher; track requested changes without mutating approved build silently.
3. Verify published store installation, sign-in callback and upgrade compatibility.

Acceptance:

- Submission != approval; public availability and exact artifact verified; runtime code policy and disclosures match behavior.

**Required verification:** Store submission/approval IDs and actual published-install smoke test

## R03 — Deploy immutable candidate and verify public paid journey

- Repository: `ferro`; lane: `release`.
- Dependencies: Q07, Q08, R01, R02.
- Owned paths: `docs/launch/evidence/R03.md`.
- Reviewer: maintainer; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Deploy reviewed digest/config through authorized protected pipeline; no DNS/secret console drift.
2. Run full public new-customer and bounded authorized live-payment lifecycle through published extension.
3. Verify rollback/kill switches/metrics/support and retain exact artifact manifest.

Acceptance:

- All applicable readiness gates have observed evidence; real charge/cancel semantics verified when authorized; no unresolved launch blockers.
- Deployed image and public extension match the artifacts independently reviewed in Q07; a material change restarts affected review/store gates.
- Production Google consent accepts intended public users; test-only OAuth allowlists and development extension IDs are absent.

**Required verification:** Public-domain smoke and paid-journey evidence; exact source/image/extension hashes

## R04 — Record public-launch readiness decision and handoff

- Repository: `ferro`; lane: `coordinator`.
- Dependencies: R03.
- Owned paths: `docs/launch/evidence/R04.md`, `docs/roadmap.md`, `README.md`.
- Reviewer: maintainer; initial size: 2-6 agent hours; status: planned.

Implementation:

1. Audit every task and gate, resolved findings, adoption evidence and external approvals.
2. Write current installation/account/billing/support docs and explicit supported scope.
3. Hand operations owner the tested runbooks and schedule post-launch review.

Acceptance:

- No planned/skipped critical work presented as complete; public status and AMSL maturity distinguished.

**Required verification:** Final evidence audit, link checks, plan graph validation and release-owner decision
