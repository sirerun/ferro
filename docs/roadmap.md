# Roadmap

For upcoming local bulk execution and hosted paid launch, use the [shared execution roadmap](execution-roadmap.md).

See `docs/devlog.md` for the pre-2026-09-10 investigation history (the
original "Runtime hardening" claim and its correction) -- superseded here by
the Shipped entries below, which reflect independently re-verified reality.

## Shipped — Chrome session service (2026-09-22)

E12 is complete: extension driver, agent session
leases, backend selection, authenticated Tailscale MCP HTTP, explicit blocked and
disconnected results, cancellation, and setup documentation. Build/vet, both
race suites, real-Chrome extension execution and snapshot parity pass. A live
Tailscale-interface smoke test accepted authenticated MCP status calls and
rejected missing/invalid tokens; its temporary service was stopped.

Browser verification uses fixture pages in disposable profiles, not real accounts.
Cross-machine Tailscale routing remains deployment-specific. Ignitionphase and
its publishing workflow are unchanged; file-transfer/multi-tab browser APIs remain
outside this change. See ADR 008 and README's setup sections.


## Earlier E12 milestones
- E12 T12.1 `internal/extbridge` poll/reply HTTP server: PR #6, merged
  2026-09-10 (rebase). Independently re-verified against merged `main`
  (build/vet/gofmt clean, 13 named tests plus the full suite green). One
  coordinator fix during review: corrected a doc comment overclaiming
  `/reply` is tab-id-checked (it isn't -- bearer token + the unforgeable
  action id gate it instead). Two ADR-006 wire details resolved and
  documented in the package: tab id travels via `X-Ferro-Tab-Id` header,
  `/next` long-polls 30s before 204. See `docs/plan.md` T12.1.
- E12 T12.0 `core.PageDriver` extraction from `internal/core/executor.go`:
  PR #8, merged 2026-09-10 (rebase). Pure refactor, zero behavior change --
  independently re-verified: `ChromedpDriver` is a verbatim relocation
  (line-by-line diffed), both `go test -race ./...` and
  `FERRO_TEST_BROWSER=1 go test -race ./...` green on merged `main` with no
  orphaned Chrome processes, `grep -c "chromedp\." internal/core/executor.go`
  confirmed 0. One self-caught deviation fixed in the same PR
  (`Runner.Run` dropped a `WithDriver` override). Two disclosed scope
  edges left for T12.3/T12.4 (locator.go's residual chromedp call,
  TakeSnapshot not yet driver-routed) -- see `docs/plan.md` T12.0.
- E12 T12.2 generic MV3 extension + ref-numbering parity port: PR #10,
  merged 2026-09-10 (rebase). Independently re-verified: `adapter.js`'s
  `takeSnapshot()` is a line-by-line port of `snapshot.go`'s DOM-walk
  (including its pre-existing dead `tag === 'INPUT' && el.type !== 'hidden'`
  clause, ported as-is -- not this task's job to fix); the Go-side
  (`snapshot_parity_test.go`, chromedp) and Node-side
  (`extension/snapshot.test.cjs`, a zero-dependency CDP client) parity
  tests both re-run independently against the checked-in golden, both
  green; `background.js`'s trusted-click retry logic diffed directly
  against `ox`'s original and confirmed verbatim. **Caught before
  merging:** this branch was built from a base that predated T12.0/T12.1's
  own doc-update merges, so its `docs/plan.md` diff would have silently
  reverted their checkboxes and shipped-notes despite GitHub reporting
  `mergeStateStatus: CLEAN` -- the same stale-base failure class as PR #3
  earlier this session (`docs/devlog.md`). Fixed in the same PR by
  resetting `plan.md` to `main`'s version and reapplying only T12.2's own
  edit before merge. **Wave 1 (T12.0, T12.1, T12.2) is now complete** --
  full `go test -race ./...` and `FERRO_TEST_BROWSER=1 go test -race ./...`
  (including the Node parity test) all green together on merged `main`.

## Shipped
- Compiling module with core, browser pool, OpenAI-compatible client, resolution cache, examples, integration suite skeleton.
- E7 RunOn: run tasks on a caller-owned BrowserContext (T7.0-T7.4), reconciled 2026 09 10.
- E8 Extract degrades per field instead of aborting (T8.1-T8.5), reconciled 2026 09 10.
- E9/E10 closeout (T9.1 exported PlanSchema, T9.5 HTTP-400 auto-fallback,
  T10.1-T10.3 context-lifetime docs + regression test): PR #2, merged
  2026 09 10 (rebase). T9.6 excluded from this PR by design -- folded into
  E11's T11.0. All 5 tasks independently re-verified against origin/main
  after merge (build/vet/fmt/race + the two new named tests), not just
  taken on the agent's word.
- E11 ferro-mcp (T11.0-T11.11): PR #3, merged 2026 09 10 (rebase) --
  `cmd/ferro-mcp` (leader-elected owner/shim daemon over one shared
  browser tab, ADR 004; deny-by-default per-origin allowlist, ADR 005;
  `run_task` plus the primitive tool set over the official
  `modelcontextprotocol/go-sdk`). Code-reviewed in full (every new
  `internal/mcp` file and `cmd/ferro-mcp/main.go` read directly) and
  independently re-verified against merged `main`: build/gofmt/vet clean,
  default and `FERRO_TEST_BROWSER=1` race suites both green, all 16 named
  tests confirmed individually running and passing. `docs/plan.md`'s
  T11.0-T11.10 checkboxes were stale at merge time (commits landed but
  boxes unchecked) and were corrected, along with one stale test-name
  citation in T11.0's note. The `ferro-wt-mcp` worktree and local
  `mcp-server` branch are deleted (David's 2026-09-10 decision), after
  confirming every commit's unique content (run_task, leader election,
  allowlist, the doFill and planner-prompt fixes, `MaxPlannings`,
  `FERRO_MCP_MAX_ELEMENTS`) is reachable from `main`.

## Completed foundation components — 2026-09-24
- [PR #16](https://github.com/sirerun/ferro/pull/16): contract revision 2, profiles, usage accounting, budgets, read-only policy driver, schemas and durable receipts (L01–L05, L07). Fresh headless review and combined race/real-Chrome/vet/contract checks passed; see [review evidence](evidence/bulk-v2/review-2026-09-24.md). Runtime wiring and live qualification are pending.

## In progress
- Bulk-browser execution: L06 replay, L08 result assembly, G02 runtime composition, L09 fixtures, L10 operator documentation and G03 qualification remain to be implemented or completed from the revision-2 baseline.

## In flight
- (none)

## Planned
- E9's T9.6 (trim redundant planner-prompt prose) still open, small and
  unclaimed -- see `docs/plan.md`.
- E2, E4, E5, E6 from the v0.1 plan: outline only, expand when picked up.

## Blocked
- (none)
