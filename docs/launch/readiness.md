# Launch qualification gates

Every row requires an evidence file naming commit/image/extension hash, environment, actual command or manual script, observed result, reviewer and remaining limitation. Proposed targets and fixture passes are not production claims. A release candidate is immutable; material fixes invalidate affected evidence and require rerun.

## Gate 0 — ready to parallelize

D01-D07 done: known baseline, source comparison on both machines, provenance cleared for intended public code, threat model reviewed, frozen OpenAPI/SQL/Go interfaces, fixture UI contract, approved deployment/commercial values. All tasks have ownership/dependencies. No source family or worktree counted twice.

## Gate 1 — first real hosted task

Two separate users on two actual browser profiles authenticate, link separate devices, select their own website tab, supply their own test provider key and complete a read-only task through HTTPS. No local Go service is running. User A cannot list/read/cancel/reply to B's task or pair B's browser even with valid A credentials. Old browser generation and old credential fail. Navigation produces a fresh page snapshot. Stop prevents new commands. Verify selected DOM only goes to intended backend/provider and secrets do not enter content scripts.

## Gate 2 — paid private beta

Use Stripe test mode for checkout, webhook duplication/reordering, active subscription, cancel-at-period-end, payment failure, re-subscribe, portal and account deletion. Test a small authorized live checkout/cancel/refund only when the launch owner has approved actual money movement; a test-mode pass is not a live-paid pass. Checkout success redirect alone grants nothing. Projection outage/staleness, late/unattributed events and provider retry behavior have documented outcomes. New unpaid tasks fail at API admission, not only in UI. Billing management/export stay available after access expires.

Validate device revoke, logout-all, key replace/delete, account disable and account delete across both replicas. Account deletion stops renewals or reports durable pending state; it never claims complete prematurely. Restore replays tombstones before accepting traffic. Ferro actually consumes every claimed AMSL package/workflow/component at an immutable version.

## Gate 3 — failure and security qualification

Required adversarial cases: missing/wrong/expired/revoked credentials; account disabled; IDOR for every resource route; malformed/oversized JSON; page prompt injection; origin redirect; stale DOM target; content-script spoofed link/confirmation; SSRF endpoint/model/redirect attempts; log/trace secrets; CSRF/session fixation/code replay/PKCE mismatch; webhook signature/body/account/mode errors; checkout race across processes; SQL deadlock/rollback; credential storage/KMS failure; role/environment mismatch.

Fault cases: kill API/worker between command persist/send/reply; drop each HTTP response; duplicate poll/reply/task requests; close/reopen Chrome; suspend/restart extension worker; network offline and reconnect; navigate/switch tab mid-task; browser update; server rolling release; revoke during poll/model request; task deadline/cancel race; DB outage/failover; delayed/out-of-order Stripe delivery; KMS unavailable; OpenRouter 401/429/5xx/slow streaming/truncated response. Expected result is explicit safe stop/uncertainty and no automatic duplicate browser mutation.

Independent reviewer checks cloud isolation, auth/linking, payment state, encrypted keys and deployment trust. No unresolved critical/high findings; unresolved medium issues affecting authority, data loss, payment correctness or duplicate effects block launch. Lesser issues need named owner, deadline and explicit acceptance. Run dependency/secret/container scans and license/SBOM checks; findings are reviewed, not merely scanned.

## Gate 4 — operational and product qualification

- Deploy two replicas from a verified digest and prove session/task authority does not depend on load-balancer stickiness.
- Run a controlled 100-device / 25-task load profile for 60 minutes. Measure API p95, dispatch/cancel latency, DB connections, bounded queues and errors; meet architecture targets or explicitly revise before release.
- Run a 24h beta soak with bounded synthetic activity. Zero cross-account deliveries, duplicate mutating executions, unbounded queue growth or secret-bearing logs. Review uncertain outcomes separately from ordinary failures.
- Restore a production-shaped disposable database and encrypted keys in <=4h with <=15min data-loss target; prove restored revoked/deleted accounts do not regain access. Test old image rollback against the migrated schema. Drop restore resources through IaC after review.
- Verify alert delivery by inducing safe failures: public HTTPS health, API/DB/KMS failure, reconcile backlog, error/latency, task uncertainty spike, quota/cost and failed backup. A dashboard existing is insufficient.
- Test latest supported Chrome on macOS and Windows, minimum supported version in fixture CI, fresh install, upgrade from local version, browser restart and uninstall. Linux is documented best-effort unless qualification evidence is added.
- Accessibility: keyboard-only onboarding/chat/settings, focus restoration, live status without excessive announcements, contrast for glass bubbles, reduced motion, zoom/narrow side panel, error recovery.
- Measure idle CPU/memory/network over 30 minutes and active task overhead against an empty-profile baseline. No model calls while idle; no screenshot loop or extra browser instance. Publish only measured resource claims.
- Verify support contact and runbooks: disconnect/relink, wrong key, provider rate limit, failed payment, lost command result, outage, data export/deletion, abuse and vulnerability report. Restrict support/admin actions; no casual transcript/key access or user impersonation.

## Gate 5 — Chrome Web Store and public release

Create a stable publisher-owned extension ID early so auth callback configuration can be qualified. Bundle all executable code; server returns a constrained operation protocol interpreted by packaged code, never JS/WASM/modules to execute. Inspect any debugger use and justify each permission. Replace unconditional all-sites injection with explicit user-granted per-origin access where feasible and qualify real navigation behavior; do not simply remove host permissions and hope execution works.

Prepare listing copy, actual screenshots, accurate single purpose, permission explanations, privacy/data-use disclosures and a reviewer test account/steps. Document page/prompt transfer to Ferro and OpenRouter, BYOK custody, retention, user control and unsupported websites/frames/files. No evasion of login/CAPTCHA, misleading universal automation claims, remote executable updates or hidden data collection. Review public policy/terms/tax/refund copy with the accountable owner before publishing.

Public install must work from the approved store item, including identity callback; unpacked installation is only beta evidence. Store submission, review, approval and public availability are separate statuses. Keep the prior working extension/backend protocol compatible during rollout; version handshake rejects unsupported clients with upgrade instructions. Server kill switches can stop admission/mutation and invalidate bad versions without deleting customer data. Do not expose arbitrary remote code as a kill-switch mechanism.

Run the full new-customer journey from the public domain using a published extension: sign in -> subscribe -> install -> connect -> key -> task -> stop -> history -> portal/cancel -> export -> delete. Verify valid TLS/DNS, legal/support links, charge amounts/currency, receipts and provider permissions. Release owner signs the evidence table and monitors launch with a tested rollback path.

## Evidence levels and launch decision

| Level | Establishes | Does not establish |
|---|---|---|
| Static/mock/package tests | Tested code/schema behavior | Real browser/provider/cloud behavior |
| Real Postgres + controlled Chrome | Protocol and isolation in fixtures | Real-site/model reliability |
| Provider test-mode flow | Actual provider integration | Successful live customer charge |
| Disposable cloud deploy + restore | Tested operational behavior | Every production policy is attached |
| Live bounded qualification | Observed public journey | Unlimited website compatibility |
| Public store availability | Approved distribution artifact | Runtime security or product reliability |

Release is blocked until all applicable levels are represented and there is no unstated bypass. Keep a per-capability AMSL adoption record separately from product launch status.

Official references checked during planning: [Chrome user data](https://developer.chrome.com/docs/webstore/program-policies/user-data-faq), [Manifest V3 requirements](https://developer.chrome.com/docs/webstore/program-policies/mv3-requirements), [remote hosted code](https://developer.chrome.com/docs/extensions/develop/migrate/remote-hosted-code), [Chrome identity](https://developer.chrome.com/docs/extensions/reference/api/identity), [Stripe webhooks](https://docs.stripe.com/webhooks). Recheck these before store submission; this plan does not assert approval or interpret legal obligations for the owner.
