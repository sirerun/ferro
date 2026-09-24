# Execution instructions for bounded coding agents

Cross-track authority: [shared execution roadmap](../execution-roadmap.md). Its shared-file reservations and total lane limit apply before dispatch in either track.

## Coordinator responsibilities

Own architecture decisions, source/provenance comparison, threat model, API/SQL/Go-interface freeze, workspace assignment, compatibility review, integration and release gate evidence. Security/payment/lease/provenance decisions require a reviewer able to reason about cross-layer failures; do not delegate those decisions to an agent simply because it can write the code.

Use small PRs: one task or one inseparable component+consumer fixture. Freeze implementation contracts in D05; every task handoff gives a parent SHA and the contract revision. Agents can add code only within owned paths. Composition roots, dependency lockfiles, SQL numbering, API specs and public catalog generation are coordinator-owned unless explicitly assigned. Do not let two workers update `go.mod`, the same manifest, `background.js`, router registration or the same catalog output concurrently. The manifest includes serialization dependencies for these hotspots; actual path overlaps still require a scheduler lock.

Parallelism policy: choose at most three concurrent coding lanes from AMSL runtime, hosted backend, extension/web UI and delivery/infra, plus one coordinator. This is a shared limit across hosted and bulk work, reduced when the harness has fewer slots. Assign ready cards by dependency rather than filling every slot. Use separate worktrees and branches. Build dependencies in sibling worktrees only for temporary development; real consumer evidence uses immutable published commits and no `replace`, workspace override or untracked source.

Before claiming completion inspect the actual diff, test assertions and denied paths. Review implementation and consumer separately, then review their wiring together. A green test that sets internal state by hand may miss the public route that should set it. Prefer actual handlers/database/extension protocol for boundary tests. No fabricated test results, CI links or promotion claims.

## Worker prompt template

> Implement TASK_ID only at BASE_SHA in ASSIGNED_WORKTREE. Read docs/launch/architecture.md, amsl.md and your task card. Contract revision is CONTRACT_SHA. Edit only the owned paths; ask the coordinator to handle composition/lockfiles or conflicting changes. Follow the listed implementation steps in order. Use the specified existing SDK/package; do not invent alternatives or widen scope. Add the listed acceptance tests at the real boundary. Keep local mode passing. No live deployment, billing charge, broad portfolio migration, credential printing or public posting. If a contract is missing/contradictory, report its exact gap and continue only independent work. Return changed paths, tests actually run, output summary, risks, dependency pins and consumer wiring evidence. Do not mark the task done from compilation alone.

Each card specifies dependencies, output paths, steps, pass/fail conditions, test command and reviewer class. Read only the relevant source comparison/contract slice; do not give a low-reasoning worker the entire portfolio and ask it to “figure out auth”. A task exceeding one focused working day should be split by coordinator before assignment. Task estimates are initial sizing bands; record actual duration.

## Definition of ready

- Every dependency is integrated and verified, or a frozen mock contract is explicitly supplied for a UI-only task.
- File ownership and branch base are unambiguous; no unowned migrations/composition edits.
- Inputs/types/errors/status transitions and failure behavior exist in a committed artifact.
- Tests specify observable behavior and forbidden behavior; required fixtures/services are available.
- Provenance/SDK choice settled; no credentials needed in the prompt.
- The reviewer and downstream consumer task are named.

## Definition of done

- Only intended files changed; formatting and relevant tests pass; failures are investigated.
- Public APIs wired into real routes/UI/infra outputs. No hidden stub/fake remains in production path.
- Cross-user/auth/idempotency/failure cases applicable to the task are tested.
- Test results attached to the exact commit, including skipped checks and environment limits.
- Consumer task is complete before the component lane is called adopted.
- Docs/config/migration/rollback updated; secret scan and provenance review complete for public AMSL changes.
- Reviewer signs the task record. Status promotions, merges and deployments follow current user authorization and repository governance.

## Resource and test discipline

Read applicable AGENTS.md and ajent.social at session start and lane boundaries. Discover other agents via process table/cwd, not remembered paths; append-only gossip is coordination, not an assignment database. Do not send external messages without authorization. Ajent MCP conflicts are recorded as unavailable, not an empty inbox; do not block implementation on the knowledge service.

Before a multi-package go build/test/vet/lint or JS build: check `uptime`; hold above load 10; acquire the shared mini heavy-build lease using the installed claim script and exact purpose. Release the exact claim SHA in a shell trap on success/failure/interruption. One multi-package race lane; at most two heavy lanes/project. Never steal an unexpired lease. Browser tests use disposable profiles, controlled fixture sites and measured process cleanup. Do not create Chrome processes in the customer's logged-in profile for tests.

Target tests first, then full required suite once before integration. Do not repeatedly rebuild unaffected modules. CI runs separate Postgres+API+real-Chrome suites from package tests. Pin browser/test tooling and clean orphan processes. Use fake provider servers for failure determinism, then qualification accounts for actual providers; both types of evidence are needed.

## Ownership and handoff

Task statuses: planned -> ready -> in_progress -> review -> integrated -> consumer_verified (where applicable). blocked requires exact missing dependency and owner; no silently bypassed gates. Record branch/commit, checks, evidence links, reviewer and next dependent tasks in `docs/launch/evidence/TASK_ID.md` (new file per task; do not concurrently rewrite the manifest).

Every integration batch follows: fetch -> inspect local changes -> new worktree -> apply/rebase only authorized changes -> targeted tests -> review -> required checks -> exact-head merge if authorized -> verify main -> evidence. Leave unrelated user edits intact. Do not introduce an entire stale feature branch merely to obtain one credential adapter; compare it to the launch baseline and port the bounded delta.

## Failure escalation rules

Stop dependent implementation and escalate: contradictory security contract; missing publication rights; two source consumers with incompatible semantics; denied cross-user test unexpectedly succeeds; provider outcome uncertain; source promotion claimed without consumer evidence; store policy requires runtime design changes; IAM role broader than approved profile. Provide a minimal reproducer and bounded options, not an invented workaround. Routine choices within frozen contracts remain worker decisions.

No lower-reasoning agent is asked to decide pricing, legal retention/tax/refund policy, execute real payments, approve IAM privileges, grant itself maintainer approval or claim public readiness. These are explicit D07/release-owner gates with concrete prepared artifacts.
