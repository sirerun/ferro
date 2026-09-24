# Ferro public launch through AMSL adoption

Cross-track authority: [shared execution roadmap](../execution-roadmap.md). Its shared-file reservations and total lane limit apply before dispatch in either track.

Status: prescriptive implementation plan, not an implementation or launch claim.
Prepared 2026-09-24 UTC against Ferro `daa5fd8` and AMSL RFC revision 3 (remote RFC blob verified during planning).

## Objective and scope

A new customer visits `ferro.sire.run`, signs in, subscribes, installs the Chrome extension, connects an explicitly chosen tab, supplies an OpenRouter key, completes browser work, stops it, manages billing and deletes their account. They install no local Go service. The extension executes in their own Chrome profile; the hosted Go service plans tasks and calls the model. Chrome must remain available.

A second deliverable is demonstrated AMSL reuse: source comparison -> publishable contract -> narrow implementation or existing-library reference -> immutable dependency -> Ferro adoption -> executed consumer evidence. A package with tests but no wired Ferro consumer is unfinished. A usage count is not evidence of matching semantics.

Public launch v1 is deliberately bounded: one personal account, one active browser connection and one active task per account; one top-level paired tab; OpenRouter BYOK; one monthly subscription; Google sign-in; read-only tasks by default; explicit per-task interaction consent. Account settings and billing live on the website; chat stays in the glass side panel. Preserve local mode. No team accounts, arbitrary model endpoints, unattended schedules, autonomous campaigns, cross-frame work, file transfer, arbitrary JS/CDP execution, public remote MCP OAuth, or cross-device conversation sync in this launch.

Interaction consent is explicit: show the selected tab, allowed origins, task text and interaction mode before starting. It authorizes that bounded task, not future tasks. Decline tasks whose required browser capability is unsupported. An LLM cannot broaden permissions. Do not claim that a broad interaction toggle implements transaction-specific payment or message approval. User-requested destructive/sending actions require a separate visible confirmation for the specific action in v1; if its effect cannot be identified reliably, refuse that action and hand control to the human (see architecture).

## How to execute

1. Read [architecture](architecture.md), [AMSL boundaries](amsl.md), [agent runbook](agent-runbook.md) and [release gates](readiness.md).
2. Use [tasks.json](tasks.json) as the dependency and ownership manifest. [task-cards.md](task-cards.md) is its generated, human-readable copy. Validate with `python3 docs/launch/check_plan.py`.
3. The coordinator completes D01-D07 and freezes API/schema/package contracts. Lower-reasoning agents do not invent identity, payment, lease, cryptographic or deployment contracts.
4. Assign only tasks whose dependencies have passed. Give one task per agent/worktree. Parallelize disjoint file sets, not shared composition roots. The checked manifest contains both implementation and real consumer tasks.
5. Pass the AMSL review and immutable dependency gates before integrating a component. Preserve local Ferro regression coverage throughout.
6. Cross the launch gates in order. “Code merged” is not “paid beta”, and “submitted to the store” is not “publicly installable”.

Task states begin `planned` even where exploratory source inspection exists: each task must produce the specified reviewed artifact or execution evidence. Source inspection for this plan was read-only; no launch runtime component, cloud resource, price or store submission was created.

## Inventory findings and reuse decisions

The mini and MacBook were inspected read-only. Source records use S01-S12 aliases in committed planning documents. Restricted repo paths/revisions and source notes live in the ignored `.claude/scratch/hosted-launch/private-census.md`; never publish that file into the public AMSL repos. Tests in source repos were inspected by name/source where stated, not executed. Full provenance and negative-path verification remain D02/D03 work.

| Behavior | Observed evidence | Disposition for this plan |
|---|---|---|
| Scoped machine credentials | Existing AMSL implementation and a Ferro adapter branch | Adopt and extend storage only; do not rebuild issuance/verification |
| Human login and sessions | Opaque/passkey sessions, JWT access/refresh and password-reset implementations have different contracts | Reference mature OIDC/session libraries; keep account policy local; no universal auth framework |
| Customer linkage / checkout | Application implementation, archived predecessor and an independent upstream implementation on MacBook | Candidate original implementation of durable coordination; use official Stripe SDK; provenance gate blocks copying |
| Subscription reconciliation | Existing deduplication and projection code; material ordering and policy differences | Candidate narrow inbox/reconciliation contract; do not extract a universal entitlement predicate |
| Go validation | Repeated vet/test/race jobs in independent repos | First low-risk new AMSL implementation; adopt in Ferro and a second real Go module |
| Deployment identity | Repeated provider-specific GitHub OIDC trust and limited deployment roles | Extract AWS profile + policy tests, with original/cleared provenance |
| Private database | Repeated private PostgreSQL, TLS, backup/deletion contracts in independent infrastructure repos | Extract AWS profile only; live restore is required consumer evidence |
| Container build and release | Repeated patterns but varied platforms and trust | Reference existing Actions initially; extract only the demonstrated remaining common job |
| Browser routing, task execution, model prompts, pricing, permissions UI | Ferro authority and product behavior | Ferro-owned; not AMSL scope under this RFC |

Two checkouts, a worktree, an archived predecessor and its successor are not independent consumers. Source availability is not publication permission. The MacBook census found an upstream checkout with restrictive license notices: no source from it is authorized for copying into Apache-2.0 AMSL. It is a behavioral comparison only, pending provenance review. None of this plan promotes an AMSL lifecycle status.

## Delivery stages and parallel lanes

| Stage | Coordinator gate | Parallel work after gate |
|---|---|---|
| 0: contract freeze | D01-D07 | Baseline/source/provenance investigation can overlap; coordinator alone resolves final interfaces |
| 1: foundations | Frozen principal, routes, schema ownership, package contracts | Credential store; Go workflow; checkout contract; reconciliation contract; cloud scaffold; extension transport; website shell; infrastructure components |
| 2: first cloud task | Foundation checks pass | Login/device linking; durable task/command routing; model-key storage; UI onboarding; payment packages; AWS composition |
| 3: paid dogfood | Two users complete separate cloud tasks without leakage | Checkout/portal, entitlement gates, revocation/deletion, live deployment, fault tests and product docs |
| 4: public qualification | Paid lifecycle and restore drills pass | Browser/platform matrix, accessibility, performance, store review, support rehearsal, independent security review |
| 5: release | All required readiness rows have evidence | Immutable deploy, smoke test, published extension and bounded launch monitoring |

Run at most three coding lanes plus one coordinator across both tracks initially; increase only if review bandwidth and the dependency graph support it. One coordinator owns merges/composition. On this mini follow the shared heavy-build lease; at most two heavy lanes per project and only one multi-package race lane. Do not move load to the MacBook without checking its local instructions/load. No builds were necessary for the planning census.

## Estimate and critical path

This expanded scope includes library extraction and verified adoption, not just hosting Ferro. Budget 3-6 calendar weeks with 3-4 implementation lanes and a dedicated coordinator/reviewer, plus external approval delays. First cloud task is the earliest milestone; a paid private beta can precede store approval. These are planning ranges, not measured throughput. D01 must replace them with a capacity-based schedule after contract/provenance review.

Critical path: baseline/contracts -> shared storage + credentials -> account/device link -> fenced browser dispatch -> wired cloud task -> paid access + deployment -> adversarial/failure qualification -> store approval + release. AMSL provenance or review delays can extend this. If a candidate fails evidence review, use a documented REFERENCE_EXISTING outcome; do not hide the delay by copying the same logic into Ferro. Browser-specific work may progress while AMSL reviews run.

## Decisions that require an accountable owner

D07 records actual values, owner and date for cloud account/region/budget, domain/DNS ownership, Google client and consent settings, Stripe legal entity/price/currency/tax/refund/cancellation policy, retention and support contacts, store publisher identity and extension ID. Suggested implementation profile is AWS ECS Fargate + ALB + private RDS PostgreSQL, Go HTTP/templates and existing JavaScript side panel. No production mutation or charge is authorized by this plan alone. Build concrete manifests/previews and review evidence before the release operator acts. Existing explicit authorization in a later execution session takes precedence over generic approval reminders.

Public AMSL records must contain no private repo names, hashes, infrastructure identifiers, home paths, customer details or incidents. Use synthetic fixtures and restricted/maintainer-reported evidence labels as the RFC requires. This private Ferro plan is not content for a public AMSL issue or package README.

## First implementation assignments after planning

- Coordinator: D01-D07. Discovery evidence already exists, but executable contracts and owner values still require completion.
- First AMSL delivery slice: A11 then A12, with actual Ferro and AMSL Go workflow runs. This earns reuse evidence before expanding delivery abstractions.
- Identity lane: A01 -> A02 -> A03, alongside F01/F02 and the reference-existing login/session integration.
- Billing lane: A04/A07 contracts -> A05/A08 implementation -> A06/A09 Ferro wiring; keep the package/consumer pair visible in every status report.
- Infrastructure lane: A13/A14 and A16/A17 -> real Ferro output consumption and disposable verification (A15/A18).
- Browser/UI lane: X01/W01 can implement against frozen contracts while the backend matures; Q01 cannot pass until the packaged extension and real server are wired.

The coordinator schedules at most three of these coding lanes at a time across both tracks, selecting only dependency-ready cards. Identity/payment/infra contract review is deliberately centralized; parallel coding does not remove that responsibility.
