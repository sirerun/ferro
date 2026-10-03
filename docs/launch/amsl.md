# AMSL extraction and Ferro adoption requirements

Authority: [RFC revision 3](https://github.com/ajent-social/capabilities/blob/main/docs/rfc/0001-amsl-bootstrap.md). Repository instructions and user authorization govern changes; this plan does not silently publish restricted evidence or approve security-sensitive merges. Every implementation task includes a separately reviewable consumer task.

This AMSL component-adoption track is independent of hosted customer-runtime adoption. A credential, checkout, reconciliation, workflow or infrastructure component—even when consumed by Ferro—does not qualify the generated paid application/provider profile, tenant isolation, recovery, or the neutral runtime contract. Hosted runtime eligibility remains gated by D08 and subsequent D04/D05 approval. The older references below to Ferro-local account policy are candidate component boundaries, not authority to create app-owned hosted login or paid admission.

## Evidence gate

For each proposed component create a comparison row for intent, inputs, outputs, owner/resource binding, state lifecycle, concurrency, partial failure, storage, revocation, operational requirements and differences. Read code and tests from two independent sources or prove RFC path C (a mature source plus Ferro as immediate second real consumer). Merely finding matching filenames is DISCOVERED, not CANDIDATE.

Record source root/revision/working-tree status privately and an independently reviewable behavioral contract publicly. Worktrees and a predecessor/successor lineage count once. Third-party copied/forked code does not automatically count as an independent implementation. Mature means its relevant production path and failure semantics are understood; a large repository or passing unrelated tests is not maturity evidence. D02 must narrow or reject candidates lacking this evidence.

Before copying any source, D03 checks license, authorship/history, copyright notices and publication rights. The MacBook upstream billing source has non-Apache license conditions: no copy or superficial rewrite into AMSL. The default for billing is an original implementation from a sanitized reviewed contract using the official SDK. No private paths, repo names, hashes, incidents, customer data or internal topology in public artifacts. Restricted evidence is explicitly maintainer-reported and not publicly reproducible. Do not invent consumer names to make it appear public.

Use the RFC reuse order. A REFERENCE_EXISTING record with a real Ferro integration is a successful reuse outcome. Do not add a wrapper that merely renames an SDK. New SQL adapters are justified against an existing capability contract; a new capability is not required for each adapter.

## Components

### A: identity.service-credentials — adopt, add PostgreSQL adapter

Evidence: S01 existing AMSL lifecycle; S02 existing Ferro integration branch. Check upstream main/PR state before editing; source checkouts can be stale. Preserve `servicecred.Store` methods Create/Lookup/Revoke/List and existing issue-once Secret redaction, explicit owner/resource/scopes, mandatory expiry and durable request-time revocation. Do not assume the local credential adapter is already merged into the hosted baseline.

Proposed owner: `ajent-social/go`, `servicecred/pgstore/`. Implement against pgx/v5 using an existing pool or transaction seam, parameterized SQL and fixed schema. Create is atomic/no overwrite; Lookup reads authoritative committed state; Revoke atomically requires exact owner+resource; List exposes metadata only. Database failures never authenticate. No bbolt mounted over network storage. Schema/version handling must work with caller migrations and two app replicas. Expose neither token nor digest over HTTP. Add contract tests shared with boltstore where possible without changing semantics.

Denied cases: wrong owner/resource/scope; expiry; disabled account (Ferro gate); revocation before and after process restart; concurrent create/revoke; storage error; another credential's session/lease/cancel access; sanitized logging/JSON. Revocation does not undo already-performed browser effects.

Ferro adoption: hosted auth/device routes use an immutable module pin and the actual SQL adapter. Two users/devices exercise cross-account denial and fresh-process revocation against PostgreSQL. Existing local shared-token mode retains its tests; hosted mode never falls back to it. Registry admission remains CANDIDATE until review/executed consumer evidence supports EXPERIMENTAL; no STABLE promotion.

### B: billing.customer-checkout — durable coordination, not Stripe reimplementation

Evidence: S03 plus independent behavior observed in S09; S04/S12 are the same source lineage as S03. S09 is comparison-only. D02 must validate the common contract and source maturity before implementation. Reuse official Stripe Go SDK for requests and signature primitives. Do not port custom provider HTTP clients.

Proposed package: `ajent-social/go/billing/checkout/` with a small provider-specific Stripe adapter; no imaginary multi-provider API. Frozen operations: EnsureCustomer(account, attempt), StartCheckout(account, approved price, approved returns, attempt), RecoverAttempt(account, attempt). Concrete Go types, errors and Store transaction/lease methods are D05 artifacts. Ferro owns authentication, price selection, admission, public text and eligibility.

Persist account->customer binding, operation kind, logical attempt ID, request hash, provider key, provider references, status, lease epoch, created/expiry times. Only one active customer-create operation per account. Only one active checkout per account. DB transaction claims work; SDK request happens outside transaction; only current epoch commits result. Replay an identical attempt within the provider window; changed arguments conflict. A lost response leaves an unknown result that is recovered, not a new unguarded create. Beyond the idempotency window, resolve an existing provider reference/searchable attempt or enter needs-review; do not promise permanent exactly-once provider effects. Unknown/refused recovery is explicit and observable.

Tests: two independent processes racing same/different attempt IDs, crash before/after provider response/local commit, provider timeout, duplicate click, changed payload, expired/consumed checkout, unknown outcome beyond retention, database rollback, wrong account, malicious redirect/price, already subscribed and test/live mismatch. Real Stripe test-mode consumer: web login -> checkout -> return -> webhook -> visible subscription. Redirect alone must not grant access.

### C: billing.subscription-reconciliation — durable verified inbox and current-state projection

Proposed package: `ajent-social/go/billing/subscription/`. Evidence S03/S09, subject to D02 comparison; do not adopt raw event timestamp ordering or product tier fallbacks. Own bounded verified-event ingress persistence, unique(provider account,livemode,event ID), per-resource reconciliation leases, retry/dead-letter metadata and atomic projection updates. SDK verification precedes admission. Ferro owns status->entitlement mapping and prices.

Operations to freeze: AcceptVerifiedEvent, ClaimReconciliation, ReconcileCustomer, GetProjection, ScheduleSweep; exact return types/errors/Store CAS methods defined before coding. Keep Stripe-specific status/reference types explicit. Verified payload admission is not authority to attribute an arbitrary account from metadata: cross-check existing customer binding and controlled checkout attempt. Unmatched events remain retryable. Duplicate delivery is harmless; same event ID with conflicting content alarms. Retrieve authoritative subscription state; prevent stale worker commit with lease epochs. Order by controlled reconciliation, not arbitrary timestamp or lexicographic event ID. Address cancellation/deletion and multiple subscriptions deterministically.

Tests: duplicates/out-of-order/equal-timestamp events, transaction crash, concurrent instances, late unknown customer, missed webhook, provider 429/5xx, stale lease, test/live/account mismatch, cancel-at-period-end, incomplete/past_due/unpaid, deleted customer and projection recovery. Ferro adoption tests prove paid-access decisions at actual task admission, billing UI and reconciler. Record backlog and freshness thresholds.

### D: identity.sessions / identity.password-hashing — reference existing

Evidence S05 opaque/passkey, S10 JWT access/refresh, S11 password-reset/role claims, S04/S12 broader OAuth. They have incompatible lifecycle and policy. Do not package them as a universal user/auth service or port weak custom token validation. Use maintained OIDC verification + SCS/PostgreSQL for Ferro's chosen Google sign-in flow. Password hashing stays an external reference because launch has no local passwords. If later comparison identifies an actual residual shared behavior, write a new narrow contract before extracting it.

Deliver a tested integration recipe and catalog alternative rationale, not new cryptography, JWT/session engine, password-reset UI or cloud AMSL identity service. Account linking/disable/delete and extension link authorization stay product-owned.

### E: delivery.go-validation — first new low-risk extraction

Evidence S07 and S08 independent workflows. `ajent-social/workflows/.github/workflows/go-validation.yml` uses workflow_call; inputs only validated module directory/toolchain/race toggle/timeout/runner profile. No arbitrary shell hooks or secret inheritance. Read-only permissions; pinned full-SHA actions and tool versions; bounded jobs; GOWORK=off. Caller owns triggers (including merge_group), concurrency and product-specific browser/security gates.

Adopt in Ferro and the AMSL Go repository on actual commits. Verify a real successful invocation and intentionally failing test propagation; preserve stable required-check names through an aggregate job. Record both run URLs/SHAs. Static YAML lint and synthetic caller fixtures do not count as adoption. Broaden to container/release jobs only when the measured residual duplication justifies it.

### F: infrastructure.deployment-identity — AWS profile with enforcement

Evidence S06/S07 independent trust/role implementations; their permissions differ and must be compared. Implement in `ajent-social/pulumi/components/aws/deployment-identity/` using Pulumi Go unless contract review finds a compelling existing supported profile. Inputs: existing OIDC provider ARN or explicit create request, exact repository, protected environment or exact ref subject, resource ARNs, named bounded permission profile. Outputs: role/provider identity and redacted trust metadata. No account-wide deploy role default; do not recreate an account-shared OIDC provider blindly.

Separate ECR artifact writer, qualification deployer and production deployer; deployment role cannot issue arbitrary PassRole. Test wrong audience/repository/ref/environment and raw-resource policy bypass. Repo-level branch/environment protection is a separate verification, not an IAM assumption. Actual federation from an authorized qualification workflow and rejected wrong-context test are required. Public component must contain no Ferro account/domain values.

### G: infrastructure.private-database — AWS PostgreSQL profile

Evidence S05/S06 independent infrastructure sources, subject to contract comparison. Own private RDS topology, SG ingress, encryption, secret references, TLS-compatible outputs, deletion protection, explicit backup/PITR and import/migration behavior. Inputs expose availability, retention, instance class/storage caps and network selection; do not hide cost choices. No PostgreSQL credentials in outputs/logs/userdata. Do not invent a cloud-neutral DB abstraction.

Policy tests reject public accessibility, unrestricted ingress, no encryption/backup, unsafe deletion overrides, plaintext secret outputs and app DDL authority. Pulumi mocks prove generated properties only. Ferro must consume the exported outputs in the running service, then demonstrate TLS, least-privilege connection, denied public access and restore into an isolated environment. Policy attachment/drift enforcement is evidenced separately from merely committing a policy file.

## Required completion bundle per component

Contract + source comparison + alternatives + provenance; minimal implementation; public synthetic/adversarial tests; downstream immutable pin; executed Ferro consumer flow; actual test commands/run IDs and limitations; migration/rollback/revocation behavior; maintainer security/API review; catalog validation/regeneration; lifecycle recommendation with evidence (no automatic promotion).

Record integration time, app-local code replaced/avoided, regressions, independent consumers and incompatibilities. Delete copied old logic only after successful parity/consumer checks; retain safe rollback. Do not mass-migrate portfolio projects. A second consumer's production adoption requires its own bounded assignment; repository access is not an invitation to change live systems.

## Sources for implementation agents

- [AMSL RFC](https://github.com/ajent-social/capabilities/blob/main/docs/rfc/0001-amsl-bootstrap.md).
- [Stripe idempotency](https://docs.stripe.com/api/idempotent_requests): retry semantics have a retention boundary; this is not a durable application ledger.
- [Stripe webhooks](https://docs.stripe.com/webhooks): handle verified events and delivery irregularities through durable application processing.
- [SCS](https://github.com/alexedwards/scs): existing Go session management and PostgreSQL storage options.
- [GitHub OIDC with AWS](https://docs.github.com/en/actions/how-tos/secure-your-work/security-harden-deployments/oidc-in-aws): issuer/audience/subject and environment trust must match deployment context.

Checked during planning; resolve and review exact SDK/action/provider versions at D05 and pin them. Do not copy `latest` examples into release workflows.
