# Runtime fixture qualification

Candidate: `60e7b32`. Contract revision 2 remains locked at
`ec3b05d2bbdf682abee7e11364f3864aea9c6700` (18 file hashes).
Final fresh headless review accepted the corrections and nearby failure paths with
no bugs found. This record does not claim installation or hosted deployment.

## Executed checks

- `GOWORK=off FERRO_TEST_BROWSER=1 GOMAXPROCS=2 GOFLAGS=-p=2 go test -race ./...`:
  passed; MCP package 26.269 seconds, including real Chrome with the extension.
- `go vet ./...`: passed under the same bounded build environment.
- `python3 docs/contracts/check_bulk_v2.py`: passed using frozen validators.
- `python3 docs/contracts/check_bulk_lock.py`: all 18 hashes verified.
- `FERRO_TEST_BROWSER=1 node --test extension/*.test.cjs`: passed, no skipped cases.
- Shared build lease acquired and released; no overlapping broad race lane.

## Qualified behavior

Actual MCP sessions exercise admission and exact schema/origin rejection,
request/input/planning ceilings, unknown provider usage, provider transport loss,
concurrent duplicate/conflicting IDs, read-only enforcement across initial and
repair/replay paths, fresh replay extraction, artifacts and digest-preserving
reads, disconnects, allowlist revocation and stable-owner receipt recovery.
A subprocess is killed during an admitted in-flight task; a fresh process
recovers an uncertain receipt and does not redispatch the same ID.

The real Chrome test uses an actual extension and HTTP MCP session with a
controlled model fixture. Extension browser tests verify tab handoff, token reuse
and tab-scoped panels. This is not paid-provider or live-account qualification.

## Review corrections

Fresh independent reviews found and corrected: partial extraction masking budget
failure; numeric tab identities colliding across browsers; input-token ceiling
classification; hosted poll-gap takeover; cancellation masking dispatched
uncertainty; planning-pass exhaustion classification; retained extension state
failing to re-pair after hosted restart; operation-local browser deadlines
bypassing legacy repair; unrelated leases blocking duplicate receipt recovery.

Regression tests demonstrated failures before fixes. Coordinator inspection also
reproduced cancellation during repair being replaced by the original browser
error; the final correction preserves cancellation and reconciles provider usage
once. Existing uncertainty and no-retry assertions remain covered.

## Remaining gates

G02 runtime composition and G03 fixture qualification are implemented and tested,
accepted after final review. G04 consumer adapter adoption and G05 the real
20-item paid/provider pilot remain pending. No automatic mutation authority,
multi-tenant isolation, simultaneous multi-tab execution or hosted deployment is
established by this fixture verdict.
