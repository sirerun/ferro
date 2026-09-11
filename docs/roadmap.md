# Roadmap

See `docs/devlog.md` for the pre-2026-09-10 investigation history (the
original "Runtime hardening" claim and its correction) -- superseded here by
the Shipped entries below, which reflect independently re-verified reality.

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
- (none)

## In flight
- (none)

## Planned
- E12 extension execution backend (T12.0-T12.12): drives David's real,
  signed-in Chrome profile (not `ferro-mcp`'s existing dedicated profile)
  via a new Chrome extension, so DGX/Rakazo agents can work overnight over
  a Tailscale-bound remote MCP listener. Decided 2026-09-10 (David, via
  AskUserQuestion): host is David's laptop, transport is Tailscale-only,
  architecture extends ferro's engine rather than hardening
  `~/Code/dndungu/ox`'s extension separately. See `docs/plan.md` E12 and
  ADR 006/007. No work started yet.
- E9's T9.6 (trim redundant planner-prompt prose) still open, small and
  unclaimed -- see `docs/plan.md`.
- E2, E4, E5, E6 from the v0.1 plan: outline only, expand when picked up.

## Blocked
- (none)
