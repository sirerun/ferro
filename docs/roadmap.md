# Roadmap

## Reconciliation — 2026-09-10
- The 2026-09-07 "Runtime hardening" entry below overstated completion: it
  claimed items verified by tests that do not exist under the names it
  cited. Re-verified against actual code/tests today; see
  `docs/plan.md`'s "Implementation update -- 2026-09-10" for the full
  per-task breakdown. Net result: E7 and E8 are genuinely done (under
  different test names than originally spec'd). E9 is mostly done
  (envelope-drift rejection, `SchemaCompleter`) but T9.1 (exported
  `PlanSchema()`), T9.5 (HTTP-400 auto-fallback), and T9.6 (prompt trim)
  are not. E10 (context-lifetime documentation) is entirely not done.

## Runtime hardening — 2026-09-07 (see reconciliation above before trusting this)
- Implemented on `fix/runtime-completeness`: successful-plan replay; versioned,
  atomic selector/plan persistence with automatic flush and error metrics;
  schema extraction and final-result validation; typed planner shape errors and
  one correction retry; bare-action repair; partial/ref extraction; RunOn;
  cancellation and concurrent-run isolation.
- Verified: default race suite ran 12 top-level tests and skipped 12 browser tests;
  Chrome-enabled race suite ran all 24 top-level tests, with zero failures/skips.
  `go vet ./...` and `go build ./...` passed. All browser traffic used localhost
  fixture pages and deterministic model replies; no live-model benchmark claimed.
- Original runtime regressions were reproduced before implementation: cache
  serialization, plan replay, schema structuring, partial extraction, bare repair,
  and planner envelope rejection.

## Shipped
- Compiling module with core, browser pool, OpenAI-compatible client, resolution cache, examples, integration suite skeleton.
- E7 RunOn: run tasks on a caller-owned BrowserContext (T7.0-T7.4), reconciled 2026 09 10.
- E8 Extract degrades per field instead of aborting (T8.1-T8.5), reconciled 2026 09 10.

## In progress
- (none)

## In flight
- (none)

## Planned
- E9 Schema-validated planner output, ADR 002 -- remaining gaps only
  (T9.1 exported PlanSchema, T9.5 HTTP-400 auto-fallback, T9.6 prompt
  trim), planned 2026 09 04, reconciled 2026 09 10.
- E10 Document chromedp context-lifetime rule, ADR 001 (T10.1 to T10.3),
  planned 2026 09 04, still entirely open as of 2026 09 10.
- E11 ferro-mcp: MCP server exposing the signed-in browser to agents
  (T11.1 to T11.11), planned 2026 09 10. See ADR 004 (leader-elected
  shared-browser daemon) and ADR 005 (per-origin allowlist).
- E2, E4, E5, E6 from the v0.1 plan: outline only, expand after E9-E11.

## Blocked
- (none)
