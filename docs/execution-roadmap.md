# Ferro execution roadmap

Status: foundation merged; bounded runtime and private hosted transport under final review on 2026-09-24. Detailed acceptance is recorded in [the dispatch ledger](evidence/bulk-v2/dispatch.json).

## Two delivery tracks, one integration owner

| Track | Outcome | Authoritative documents | First gate |
|---|---|---|---|
| Local bounded execution | Inexpensive, bounded MCP tasks and a recoverable sequential Zatiti pilot | [Bulk design](plan-bulk-browser-execution.md), [dispatch runbook](tasks/bulk-browser/RUNBOOK.md), L01–L10 packets | bulk.G00 baseline, then bulk.G01 compiling contracts |
| Zatiti direct-action companion | Governed connection, direct-action receipts/preconditions and user flow requirements | [Zatiti browser integration plan](plan-zatiti-browser-integration.md) | Compatibility mapping in bulk.G01; separate ZB packets and acceptance |
| Hosted paid service | Account signup, paid access, cloud coordination and public extension distribution, with verified AMSL adoption | [Launch overview](launch/README.md), [architecture](launch/architecture.md), [task manifest](launch/tasks.json), [readiness](launch/readiness.md) | launch.D01–D07 baseline, provenance, contracts and owner decisions |

The local track can deliver useful work before hosting is ready. Its Zatiti pilot is not a public-launch prerequisite. The hosted launch excludes unattended scheduling and public remote MCP OAuth; local MCP and external Zatiti dispatch do not silently expand that scope. Both preserve existing local mode.

The companion plan is requirements and coverage mapping; it does not dispatch work or mark a ZB packet complete. Bulk.G03 qualifies Ferro's read-only executor fixtures, G04 qualifies Zatiti dispatch/recovery, and G05 qualifies the matching package and authorized pilot. G04's read-only lifecycle and G05's pilot are not gated on governed direct mutations (ZB02/ZB03/ZB07); mutation enablement requires its own accepted contract and evidence. See the [runbook gate crosswalk](tasks/bulk-browser/RUNBOOK.md#zatiti-companion-coverage-and-gate-crosswalk).

## Private hosted pilot exception

The separately authorized [single-owner pilot](adr/010-private-hosted-pilot.md)
uses fixed private credentials while the AMSL account and billing components
are developed independently. It does not complete the paid-service launch
track or establish tenant isolation. The selected deployment is AWS containers
managed through Pulumi with tested scale-to-zero/wake-up behavior; a dedicated
always-on EC2 pilot is superseded. Preserve the local installation throughout.

## Authority and dependency rules

Use `bulk.Gxx` and `bulk.Lxx` for dispatch, `bulk.Bxx` for design coverage, `bulk.defer.Dxx` for deferred bulk risks, and `launch.<ID>` for hosted tasks. Bare IDs are local to their document tree. Bulk B-rows are not additional agent assignments. The bulk runbook controls dispatch and ownership; its parent controls behavior. Hosted tasks.json controls hosted dependencies and generates task-cards.md. This roadmap controls cross-track sequencing. A conflicting contract blocks the affected assignment until its owning coordinator records a resolution; do not silently choose one plan.

1. Inventory once per source revision: bulk.G00 and launch.D01 may reference the same baseline report, but each must verify its own scope. Record main, remaining worktrees, saved-work archives and installed extension/service revisions separately. A historical inventory is not current acceptance evidence.
2. Freeze contracts separately: bulk.G01 owns local v2 execution; launch.D05 owns hosted interfaces. Before either accepts shared execution types, compare profiles, budgets, policy, cancellation, result/usage uncertainty and receipt semantics. Record a compatibility table in the contract ADR. Whichever contract lands second must consume or explicitly adapt the first, with regression tests.
3. Only one integrator edits shared runner, executor, provider, MCP/chat configuration and composition roots at a time. The coordinator maintains a cross-track file reservation ledger with task, paths, base revision and release condition before dispatch. Hosted tasks that overlap bulk.G01/G02 wait for the reservation to close and receive a new immutable baseline. Existing manifest validation cannot detect cross-track conflicts by itself.
4. Share behavioral contracts, not authority assumptions. Local receipt storage is not a multi-tenant hosted store. Hosted receipts and commands require account/device authorization, durable server storage and fencing. Stable authenticated receipt ownership must survive a fresh client session; transient lease/cancel identities remain separate. Hosted adoption requires its own isolation and restart tests.
5. AMSL receives only evidence-backed reusable capabilities through the RFC and launch provenance/review gates. Browser execution, tab policy, model orchestration and Zatiti integration stay product-specific. Two consumers alone do not prove matching semantics or publication rights.
6. Budget three coding workers plus one coordinator across both tracks initially, reduced to the harness limit. A lane used by one track is unavailable to the other. Follow the shared build lease; parallel packet readiness does not authorize simultaneous heavy builds.

## Milestones and evidence

| Milestone | Required evidence | Does not establish |
|---|---|---|
| Baseline and contracts | Accepted inventories, compilable frozen contracts, explicit shared-file reservations | Runtime implementation |
| Bounded local executor | bulk.G02 integration and bulk.G03 real MCP fixture verdict, including legacy regressions | Live savings or hosted isolation |
| Local pilot | bulk.G04 durable Zatiti lifecycle and bulk.G05 matching package plus authorized 20-item pilot | Public unattended service |
| First cloud task | Launch manifest dependencies and two-user isolation checks on the wired service/extension | Paid launch or store approval |
| Paid public release | Every launch readiness gate, immutable deployment, billing lifecycle and published extension | Unbounded automation |

Installed runtime fixes and credential-adapter work remain separate branches until reviewed and integrated through the baseline gate. This documentation merge does not merge those implementations, install software, launch workers, incur model spend or complete any gate.

## Maintenance

Update the owning manifest/packet and its evidence when scope changes, then update this roadmap if cross-track sequencing changes. Keep [the historical E12 plan](plan.md) and [shipped roadmap](roadmap.md) as history; neither overrides these planned gates. Run `python3 docs/launch/check_plan.py` after hosted task edits and regenerate cards with `--write` when needed.
