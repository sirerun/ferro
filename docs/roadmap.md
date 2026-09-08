# Roadmap

## Runtime hardening — 2026-09-07
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
- Compiling module with core, browser pool, OpenAI-compatible client, resolution cache, examples, integration suite skeleton (uncommitted as of 2026 09 04).

## In progress
- (none)

## In flight
- (none; no origin remote yet)

## Planned
- E7 RunOn: run tasks on a caller-owned BrowserContext (T7.0 to T7.4), planned 2026 09 04.
- E8 Extract degrades per field instead of aborting (T8.1 to T8.5), planned 2026 09 04.
- E9 Schema-validated planner output, ADR 002 (T9.1 to T9.6), planned 2026 09 04.
- E10 Document chromedp context-lifetime rule, ADR 001 (T10.1 to T10.3), planned 2026 09 04.
- E2, E4, E5, E6 from the v0.1 plan: outline only, expand after E7 to E10.

## Blocked
- (none)
