# Roadmap

See `docs/devlog.md` for the pre-2026-09-10 investigation history (the
original "Runtime hardening" claim and its correction) -- superseded here by
the Shipped entries below, which reflect independently re-verified reality.

## Shipped (E12, in progress)
- E12 T12.1 `internal/extbridge` poll/reply HTTP server: PR #6, merged
  2026-09-10 (rebase). Independently re-verified against merged `main`
  (build/vet/gofmt clean, 13 named tests plus the full suite green). One
  coordinator fix during review: corrected a doc comment overclaiming
  `/reply` is tab-id-checked (it isn't -- bearer token + the unforgeable
  action id gate it instead). Two ADR-006 wire details resolved and
  documented in the package: tab id travels via `X-Ferro-Tab-Id` header,
  `/next` long-polls 30s before 204. See `docs/plan.md` T12.1.

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

## In progress
- E12 Wave 1, dispatched 2026-09-10 via `/apply` (pool mode, claims held):
  T12.0 (extract `core.PageDriver` from executor.go, agent `ferra_t12-0`,
  still running) and T12.2 (generic MV3 extension + ref-numbering parity
  port, agent `ferra_t12-2`, still running), each in its own isolated
  worktree. T12.1 shipped -- see Shipped above. T12.1 was routed to
  subagent dispatch instead of kazi's autonomous-harness lane for this run
  (a deliberate, disclosed deviation: solo-project proportionality plus the
  laptop's disk pressure at dispatch time, ~7GB free).

## In flight
- (none -- no PRs opened yet for E12 Wave 1)

## Planned
- E12 remaining waves (T12.3-T12.12): integration, backend selection,
  blocked-state handling, the Tailscale remote listener, tests, docs, and
  the final lint/build/test gate -- blocked on Wave 1 landing. See
  `docs/plan.md` E12 and ADR 006/007.
- E9's T9.6 (trim redundant planner-prompt prose) still open, small and
  unclaimed -- see `docs/plan.md`.
- E2, E4, E5, E6 from the v0.1 plan: outline only, expand when picked up.

## Blocked
- (none)
