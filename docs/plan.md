# Work plan: ferro remote/extension execution backend (E12)

## Context

ferro is a Go library for token-efficient AI browser automation: the LLM is
asked once for a declarative JSON plan and deterministic Go code executes it
against Chrome via chromedp. The public surface is `ferro.go`; the engine is
`internal/core`, the tab pool `internal/browser`, LLM clients `internal/llm`.
`cmd/ferro-mcp`/`internal/mcp` expose that engine as an MCP (Model Context
Protocol) server over a shared, dedicated Chrome profile (ADR 004, ADR 005).
See `DESIGN.md` for the architecture and `docs/roadmap.md` for what has
shipped (E1-E11 are all done or intentionally deferred as outlines; see
`docs/devlog.md` for investigation history from that work).

**New in this pass (2026-09-10):** David wants agents running on his DGX GPU
server and the "Rakazo" agent fleet -- machines other than the one his
browser runs on -- to keep doing real browser work overnight, using his
REAL, already-signed-in Chrome profile (not `ferro-mcp`'s existing dedicated
automation profile), on websites that have no MCP/API surface at all. Three
decisions are already made (David, via AskUserQuestion, 2026-09-10) and are
not open for re-litigation in this plan:

1. **Host:** David's laptop, not an always-on machine. This means the
   browser (and this whole backend) is only reachable when the laptop is on
   and connected -- accepted, not a defect; see the Risk Register.
2. **Network transport:** Tailscale-only (David's existing private mesh
   network). No public listener, ever.
3. **Architecture:** extend ferro's existing engine and MCP tool surface
   with a second execution backend, rather than hardening
   `~/Code/dndungu/ox`'s extension into a separate, parallel system. `ox` is
   a real, independent, actively-used project (its own repo, its own git
   history) that automates one specific site (`oxalpha.com/chat`) via a
   Manifest V3 Chrome extension using `chrome.debugger`-based trusted clicks
   against a real, already-logged-in Chrome profile -- proven prior art for
   the technique this plan generalizes, not something this plan modifies.

Two ADRs record the resulting architecture decisions in full:
`docs/adr/006-extension-execution-backend.md` (the extension itself, the
Go-side bridge protocol, the `core.PageDriver` refactor that lets `run_task`
and every primitive MCP tool work against this backend with zero change to
the planner/repair/cache logic) and
`docs/adr/007-tailscale-remote-transport.md` (the Tailscale-bound listener,
its bearer-token gate, and why a public listener was never on the table).

Constraints carried over from prior passes: the executor contains zero LLM
calls; standard library only for everything except MCP itself and the
`chrome.debugger`-based extension (unavoidably JavaScript, not Go); any
OpenAI-compatible LLM endpoint must keep working.

## Discovery summary

- `internal/core/executor.go`'s `doFill`/`doClick`/`doExtract` and the
  snapshot-generation path call `chromedp.Run`/`chromedp.Evaluate` directly
  today -- there is no existing seam to plug a second backend into. ADR
  006's `core.PageDriver` interface is that seam; T12.0 creates it.
- `internal/core/snapshot.go` is the page compiler: it decides which
  elements are interactive/visible and numbers them (`[N]`). This exact
  algorithm has to exist a second time, in JavaScript, inside the new
  extension's content script, for ref numbers to mean the same element on
  both backends -- this is ADR 006's single hardest and most safety-
  critical requirement, not a routine port.
- `~/Code/dndungu/ox/extension/` (read-only reference; that repo is not
  modified by this plan) already proves the core technique against a real,
  signed-in profile: `chrome.debugger`-issued trusted clicks (with retry
  logic for Chrome dropping the debugger mid-attach), a native-setter-plus-
  `input`-event technique for text fill, a `blocked()` DOM heuristic for
  CAPTCHA/verification/login gates, and a token-paired popup
  (`popup.html`/`popup.js`) binding the extension to exactly one tab. Its
  `adapter.js` is hardcoded to `oxalpha.com`'s specific CSS classes
  (`.msg-user`, `.new-chat-btn`, etc.) -- the new extension ports the
  *techniques*, not the site-specific selectors.
- `internal/mcp`'s `caller` interface (`wire.go`) and tool registration
  (`server.go`, `tools_primitive.go`, `tools_run_task.go`) already abstract
  over "how a tool call actually gets answered" (an `Owner` answers
  directly; a `Leader` acting as a shim relays over a socket). Adding a
  second backend to `Owner` (CDP-backed vs. extension-backed) reuses this
  same shape rather than inventing a parallel MCP layer.
- `github.com/modelcontextprotocol/go-sdk` (already a dependency, v1.7.0)
  needs to be checked for its HTTP/SSE transport's exact type name during
  T12.6 -- not yet confirmed against this specific vendored version.
- Tailscale is already in active use across David's machines (a `*.ts.net`
  hostname is configured elsewhere), so `tailscale ip -4` is assumed
  available on the laptop; T12.6 must fail closed (no remote listener, not
  an error) if it is not.

### Use case summary (new this pass)

| ID | Use case | Status |
|----|----------|--------|
| UC-013 | `internal/core.Executor` runs identically against either a chromedp or an extension `PageDriver` | MISSING |
| UC-014 | A ref-numbered snapshot from the extension backend matches the CDP backend's numbering for the same page | MISSING |
| UC-015 | An agent drives David's real, signed-in Chrome tab via the extension backend using the same MCP tool set `ferro-mcp` already exposes | MISSING |
| UC-016 | A tool call against a page showing a CAPTCHA/login/verification gate returns a distinguishable "blocked" result instead of hanging | MISSING |
| UC-017 | An MCP client on a different machine (DGX/Rakazo), connected over Tailscale with a valid bearer token, can call ferro-mcp tools | MISSING |
| UC-018 | A remote client's connection attempt fails fast and distinguishably when the laptop is offline, rather than hanging | MISSING |

Manifest: `.claude/scratch/usecases-manifest.json`.

## Scope and deliverables

In scope:

- `core.PageDriver` interface extracted from `internal/core/executor.go`;
  `ChromedpDriver` preserving current behavior exactly (T12.0).
- `internal/extbridge`: the Go-side poll/reply bridge server (T12.1) and
  `ExtensionDriver` implementing `core.PageDriver` against it (T12.3).
- `extension/`: a new, generic Chrome extension in this repo (adapted from,
  not shared with, `~/Code/dndungu/ox/extension/`) driving arbitrary sites
  via ferro's `Action`/ref vocabulary (T12.2).
- `cmd/ferro-mcp`/`internal/mcp` backend selection so the existing tool set
  (`run_task` and all primitives) works against either backend (T12.4).
- Unattended blocked-state handling: a bounded, distinguishable "blocked"
  result instead of an indefinite hang (T12.5).
- A Tailscale-bound remote MCP listener with its own bearer-token gate
  (T12.6), and verification that an offline laptop fails fast and
  distinguishably for a remote client (T12.7).
- Tests (T12.8-T12.10) and docs (T12.11) mirroring E11's depth.

Out of scope for this pass: a multi-tab extension backend (v1 is one paired
tab, matching `ferro-mcp`'s existing single-shared-tab model); an always-on
host machine (David's explicit choice was the laptop); any transport other
than Tailscale (no public listener, no LAN-only mode); modifying
`~/Code/dndungu/ox` itself (separate, independent, actively-used project --
read-only reference only); solving CAPTCHA/human-verification automatically
(both this backend and `ox` treat that as a human handoff, never an
automation target).

## Checkable work breakdown

Kazi is on PATH. Engineering tasks carry `acc:` lines.

### E9 Schema-validated planner output (ADR 002)
fidelity: executable

T9.1-T9.5 shipped via PR #2, 2026-09-10 (exported `PlanSchema()`, envelope-
drift rejection, the `SchemaCompleter` interface, and the HTTP-400
auto-fallback) -- see `docs/roadmap.md`'s Shipped section for the
independently re-verified detail. One task remains:

- [ ] **T9.6** Trim the prose envelope rules from the planner prompt now
  that the schema enforces them (`internal/core/runner.go:180` still
  contains the literal phrase "exactly this envelope"). Keep the worked
  examples; remove the redundant prose rules around them.
  verifies: [UC-005]
  acc: [`go test ./...` green and `grep -rc "exactly this envelope" internal/` is 0]

### E12 Extension execution backend: drive David's real browser remotely
fidelity: executable

Decision rationale in full: `docs/adr/006-extension-execution-backend.md`
(the extension, the bridge, the `core.PageDriver` refactor) and
`docs/adr/007-tailscale-remote-transport.md` (the remote listener and its
bearer-token gate). Read both before starting any task below -- they
contain the actual protocol/interface shapes these tasks implement, not
just background.

**Wave 1** (all three build against the two ADRs' already-fixed spec;
no task in this wave depends on another):

- [x] **T12.0** Extract `core.PageDriver` from `internal/core/executor.go`:
  an interface covering navigate, click(selector), fill(selector, text),
  select, key, scroll, extract(fields), run-the-snapshot-compiler-script,
  and wait-visible -- exactly the calls `doFill`/`doClick`/`doExtract`/the
  snapshot path make to chromedp today. Implement `ChromedpDriver` wrapping
  the existing chromedp calls with zero behavior change. `Executor` takes a
  `PageDriver` instead of calling chromedp directly.
  Shipped 2026-09-10, PR #8 (rebase-merged). Independently re-verified:
  `internal/core/driver.go`'s `ChromedpDriver` is a verbatim relocation of
  every JS/chromedp bundle that used to live inline in executor.go (line-
  by-line diffed, not just trusted); `grep -c "chromedp\." internal/core/executor.go`
  confirmed 0; both `go test -race ./...` and `FERRO_TEST_BROWSER=1 go test
  -race ./...` re-run on the merged tip, all packages green, no orphaned
  Chrome processes. One coordinator-independent deviation the task itself
  caught and fixed: `Runner.Run`'s per-run `Executor` copy silently dropped
  a `WithDriver` override (dormant today, would have bitten T12.3);
  patched by chaining `.WithDriver(r.Executor.driver)`. Two known,
  disclosed scope edges for T12.3/T12.4 to pick up (not blocking, not
  hidden): (1) `internal/core/locator.go`'s cached-selector validation
  still calls `chromedp.Run` directly (ADR 006's 9-primitive list has no
  "verify cached selector" operation; degrades to a cache miss under a
  non-chromedp backend, not a hard failure); (2) `Runner` and
  `internal/mcp/tools_primitive.go` still call the package-level
  `TakeSnapshot` directly rather than through a driver -- `Executor` never
  routed snapshot calls through itself either way, so this is unchanged
  behavior, not a regression.
  verifies: [infrastructure]
  lane: agent
  acc: [`go test -race ./...` and `FERRO_TEST_BROWSER=1 go test -race ./...` both pass with the exact same pass/fail set as before this task, and `grep -c "chromedp\." internal/core/executor.go` drops to 0 (all chromedp calls now live only in the ChromedpDriver implementation)]
- [x] **T12.1** `internal/extbridge`: the Go-side poll/reply HTTP server per
  ADR 006 (`GET /next`, `POST /reply`, bearer-token authed, one active
  pairing at a time), bound to loopback only for now (ADR 007's Tailscale
  binding is T12.6, layered on top, not built here). Shipped 2026-09-10,
  PR #6 (rebase-merged). Two ADR-006 wire details it had to decide and
  document since the ADR didn't fully specify them: the extension's tab id
  travels as an `X-Ferro-Tab-Id` header on `/next` (no third pairing
  endpoint exists), and `/next` long-polls up to 30s (`WithPollTimeout`
  overridable) before returning 204, rather than returning 204 instantly.
  Independently re-verified: build/vet/gofmt clean, 13 named tests
  (`internal/extbridge`) plus the full `go test ./...` suite green on
  merged `main`. One coordinator fix during review: the package doc
  overclaimed that `POST /reply` is tab-id-checked, matching `/next` --
  it isn't (only bearer-token + the unforgeable per-action id gate it),
  corrected in the same PR. Whether `/reply` should also assert the tab id
  once the real extension (T12.2) is wired in is left as a T12.3 call, not
  decided here.
  verifies: [infrastructure]
  acc: [a test HTTP client can long-poll `/next`, receive a queued action, and post a `/reply` that the server-side caller (a Go test double) receives with matching id]
- [x] **T12.2** `extension/`: a new Manifest V3 Chrome extension in this
  repo (`manifest.json`, `background.js`, `content.js`, `adapter.js`,
  `popup.html`/`popup.js`), adapted from `~/Code/dndungu/ox/extension/`'s
  structure per ADR 006: generic action execution (not one hardcoded site),
  `chrome.debugger`-based trusted clicks with the same dropped-debugger
  retry logic `ox` proved out, native-setter-plus-`input`-event fill, a
  generalized `blocked()` heuristic (CAPTCHA/verification/login gates, not
  site-specific text), and a JavaScript port of
  `internal/core/snapshot.go`'s element-selection and numbering algorithm.
  verifies: [UC-014]
  lane: agent
  acc: [loading the extension unpacked and pointing it at a local test HTML fixture, its content-script snapshot function returns a numbered element list; a second test asserts this list's ordering and element selection matches `internal/core/snapshot.go`'s output for the same fixture file, checked by a shared, versioned fixture page both the Go test and a Node-based extension test load]

**Wave 2** (integration; depends on all of Wave 1):

- [ ] **T12.3** `ExtensionDriver` in `internal/extbridge`, implementing
  `core.PageDriver` (T12.0) by encoding an `Action` to the bridge (T12.1)
  and decoding the paired extension's (T12.2) reply into the same result
  shapes `ChromedpDriver` produces.
  verifies: [UC-013]
  acc: [`internal/core.Executor` configured with `ExtensionDriver` against a real, paired Chrome tab (Chrome loaded with the T12.2 extension unpacked, browser-gated) executes a scripted navigate/click/fill/extract sequence against a local fixture page and produces the same result `ChromedpDriver` produces for the same fixture and same script]

**Wave 3** (depend on T12.3; disjoint files, parallelizable):

- [ ] **T12.4** Backend selection in `cmd/ferro-mcp`/`internal/mcp`:
  `FERRO_MCP_BACKEND=cdp|extension` (default `cdp`, preserving all existing
  behavior). When `extension`, `Owner` (`internal/mcp/owner.go`) is
  constructed with `ExtensionDriver` instead of launching Chrome via
  `ferro.NewBrowser` -- the same allowlist (`internal/mcp/allowlist.go`),
  the same tool registrations (`tools_primitive.go`, `tools_run_task.go`),
  and the same one-owner-per-`$FERRO_MCP_HOME` leader election (ADR 004)
  apply unchanged.
  verifies: [UC-015]
  acc: [`ferro-mcp` started with `FERRO_MCP_BACKEND=extension` and a paired browser answers `run_task` and every primitive tool the same way the default `cdp` backend does against an equivalent fixture page, and a call against a non-allowlisted origin is still denied before any browser action, exactly as ADR 005 requires for the cdp backend]
- [ ] **T12.5** Unattended blocked-state handling per ADR 006: the
  extension's `blocked()` detection causes the bridge to return
  `{"blocked": "<reason>"}` instead of leaving the poll/reply exchange
  pending; the owner surfaces this as a normal MCP tool result within a
  bounded wait (default 15 minutes, `FERRO_MCP_BLOCK_TIMEOUT` overridable)
  rather than hanging past it.
  verifies: [UC-016]
  acc: [a fixture page rendering a fake "verification required" banner causes an in-flight primitive tool call and an in-flight `run_task` call to each return a `blocked` result within the timeout window, not hang past it; a fixture page with no such banner is unaffected]

**Wave 4** (remote transport; depends on T12.4 only):

- [ ] **T12.6** Tailscale-bound remote MCP listener per ADR 007: resolve
  the bind address via `tailscale ip -4` (or `FERRO_MCP_BIND_HOST`
  override), start an MCP-over-HTTP listener on it using
  `github.com/modelcontextprotocol/go-sdk`'s HTTP transport (confirm the
  exact type against the vendored SDK version as part of this task), gated
  by a bearer token at `$FERRO_MCP_HOME/remote-token` (0600, generated on
  first run) checked before any tool dispatch. If Tailscale is unavailable,
  skip starting this listener (fail closed, not an error) and log why.
  verifies: [UC-017]
  lane: agent
  acc: [a remote MCP client connecting to the Tailscale-bound address with the correct bearer token can call a tool and get a normal result; the same client with a missing or wrong token is rejected before any tool dispatch (no browser action occurs); binding to `0.0.0.0` or the machine's LAN/public interface is never attempted, verified by asserting the listener's bound address equals the resolved Tailscale IP]

**Wave 5** (depends on T12.6):

- [ ] **T12.7** Verify and document the laptop-offline signal: with the
  `ferro-mcp` process stopped (simulating the laptop being asleep or
  disconnected), a remote client's connection attempt fails within a
  bounded timeout with an error distinguishable from T12.5's "blocked"
  result (a TCP-level connection failure, not an MCP tool result).
  verifies: [UC-018]
  acc: [a remote client connecting to a Tailscale address with no `ferro-mcp` process listening fails within 10 seconds with a connection-level error, and README's new "Remote/DGX access" section (T12.11) states this explicitly so a remote agent's caller knows to distinguish it from a blocked-page result]

**Wave 6** (tests + docs, parallelizable; depend on the waves each covers):

- [ ] **T12.8** Unit tests for `internal/extbridge`: poll/reply
  encode/decode, pairing token issuance and rejection, and `ExtensionDriver`
  parity against a fake extension double (no real Chrome needed for this
  tier, mirroring `internal/mcp`'s existing table-test style).
  verifies: [UC-013, UC-014]
  acc: [`go test ./internal/extbridge/...` passes and covers at least one success and one rejected-token case]
- [ ] **T12.9** Browser-gated integration test: real Chrome, the real
  `extension/` loaded unpacked, paired against a running
  `ferro-mcp --backend extension`, drives a `run_task` goal end to end
  against a local fixture page (mirrors T11.9's role for the CDP backend).
  verifies: [UC-015, UC-016]
  acc: [`FERRO_TEST_BROWSER=1 go test -run TestFerroMCP_ExtensionBackendIntegration ./internal/mcp` passes]
- [ ] **T12.10** Remote-transport integration test: a second process acting
  as a remote MCP client connects over loopback standing in for the
  Tailscale address (real Tailscale is not available in CI), asserting the
  valid-token/invalid-token/no-listener cases from T12.6 and T12.7.
  verifies: [UC-017, UC-018]
  acc: [`go test -run TestRemoteTransport ./internal/mcp` passes, covering valid token, invalid token, and connection-refused cases]
- [ ] **T12.11** Docs: README "Remote/DGX access" section (installing the
  extension, pairing it, starting `ferro-mcp` with `FERRO_MCP_BACKEND=extension`,
  configuring the Tailscale bind and `remote-token`, and an explicit
  security note that this backend drives David's REAL logged-in sessions --
  a materially larger blast radius than the existing dedicated-profile CDP
  backend, mitigated by the allowlist, the pairing requirement, and the
  bearer token, not eliminated by them). `DESIGN.md` note on the
  `core.PageDriver` seam. References to ADR 006 and ADR 007 (already
  written).
  verifies: [UC-015, UC-017]
  acc: [README contains a "Remote/DGX access" heading with a security-note paragraph naming all three mitigations (allowlist, pairing, bearer token), and DESIGN.md references docs/adr/006 and docs/adr/007]

**Wave 7** (final gate; depends on every task above):

- [ ] **T12.12** Lint and format: `gofmt -l .` empty, `go vet ./...` clean,
  `go build ./...` green (including the new `internal/extbridge` package
  and `extension/`'s presence -- `extension/` itself is JavaScript, not
  built by `go build`, but its files must exist and its manifest must be
  valid JSON, checked by this task), `go test -race ./...` and
  `FERRO_TEST_BROWSER=1 go test -race ./...` both green. Update
  `docs/roadmap.md` to mark E12 shipped.
  verifies: [infrastructure]
  acc: [`gofmt -l . | wc -l` is 0, `go vet ./...` exits 0, `go build ./...` exits 0, `python3 -c "import json; json.load(open('extension/manifest.json'))"` exits 0, and both race suites pass]

### Archived: v0.1 plan (2026 08, pre-dogfood)

`E2`, `E4`, `E5`, `E6` remain outline epics from the original v0.1 RFC
extraction, unstarted: executor hardening (ref-resolution collisions), a
committed replay-benchmark artifact, real-model plan-validity measurement,
and release polish. None of E12's work blocks or is blocked by these; they
stay outlines until picked up. Their trigger deps (T9.6, T8.5) are
unchanged from the prior plan revision.

- **E2 Executor hardening** (outline). Task: `T2.0 PLAN: expand E2`
  (kind: plan, deps: T8.5 -- already done).
- **E4 Replay benchmark** (outline). Task: `T4.0 PLAN: expand E4`
  (kind: plan, deps: T9.6).
- **E5 Real-model validation** (outline). Task: `T5.0 PLAN: expand E5`
  (kind: plan, deps: T9.6).
- **E6 Polish, CI, release** (outline). Task: `T6.0 PLAN: expand E6`
  (kind: plan, deps: T4.0, T5.0).

## Parallel work

- Wave 1: T12.0, T12.1, T12.2 (independent; each builds against ADR 006's
  already-fixed protocol/interface spec, not against each other's code) --
  3 agents. T9.6 can also run in this wave (touches only
  `internal/core/runner.go`'s prompt text, disjoint from T12.0's driver
  refactor of the same file's *executor*, but serialize the two if the same
  agent isn't doing both, to avoid a merge conflict in `internal/core`).
- Wave 2: T12.3 (depends on all of Wave 1) -- 1 agent, integration
  bottleneck.
- Wave 3: T12.4, T12.5 (both depend on T12.3; disjoint files --
  `internal/mcp/owner.go`/`config.go` vs. `extension/adapter.js`/
  `internal/extbridge`) -- 2 agents.
- Wave 4: T12.6 (depends on T12.4 only) -- 1 agent.
- Wave 5: T12.7 (depends on T12.6) -- 1 agent.
- Wave 6: T12.8, T12.9, T12.10, T12.11 (T12.8 depends on T12.3; T12.9 on
  T12.4/T12.5; T12.10 on T12.6/T12.7; T12.11 depends on nothing code-wise,
  can start as soon as the ADRs are read) -- 4 agents.
- Wave 7: T12.12 (depends on everything) -- 1 agent, must run last.

## Risk register

| ID | Risk | Impact | Likelihood | Mitigation |
|----|------|--------|------------|------------|
| R1 | Ref-numbering drifts between `snapshot.go` (Go) and the extension's JS port | High -- a click/fill silently targets the wrong element | Medium | T12.2's parity test runs the same fixture through both; re-run on every `snapshot.go` change (Operating Procedure) |
| R2 | Extension backend's blast radius: every site David's real profile is logged into | High | Accepted, not mitigated to zero | ADR 005 allowlist (Go-side, cannot be bypassed by the extension) + single explicit tab pairing + ADR 007 bearer token |
| R3 | Laptop asleep/offline overnight | Medium -- planned overnight work simply doesn't happen | Medium-High (explicit tradeoff of choosing laptop over an always-on host) | T12.7 makes the failure fast and distinguishable so a remote agent can back off cleanly rather than hang; accepted, David's explicit choice |
| R4 | `core.PageDriver` refactor (T12.0) regresses the existing CDP backend | High if it happens, but caught immediately | Low | T12.0's acc: line requires the exact same pass/fail set on the full test suite before/after |
| R5 | `chrome.debugger` dropped mid-click (Chrome's own known flakiness, per `ox`'s existing retry logic) | Low -- a click fails and is retried or reported | Medium | Port `ox`'s retry-before-mousedown-starts logic verbatim (ADR 006) |
| R6 | Unattended blocked-state (CAPTCHA etc.) has no human to clear it overnight | Medium -- that task just doesn't complete until morning | Medium | T12.5's bounded, distinguishable "blocked" result lets a remote agent move on to other allowlisted work instead of hanging |

## Operating procedure

- Work in a worktree per task. One commit per task. Rebase and merge.
- Before marking a task done, run its `acc:` command yourself -- see
  `docs/devlog.md`'s 2026-09-10 entries for two separate times this exact
  plan file asserted a test existed that didn't.
- Any change to `internal/core/snapshot.go`'s element-selection or
  numbering logic MUST be accompanied by an update to the extension's
  mirrored JS logic and a passing run of T12.2's parity test, even after
  E12 itself ships -- this is a standing rule, not a one-time task, per ADR
  006's Consequences section.
- Browser-gated tests need Chrome on PATH and `FERRO_TEST_BROWSER=1`;
  extension-backend tests additionally need the `extension/` directory
  loadable unpacked (no packaging/store step exists or is planned).

## Progress log

- 2026 09 10 (d): T12.2 done (PR pending): `extension/` -- a generic
  Manifest V3 Chrome extension (`manifest.json`, `background.js`,
  `content.js`, `adapter.js`, `popup.html`/`popup.js`), adapted from
  `~/Code/dndungu/ox/extension/`'s techniques (chrome.debugger trusted
  click/key with ox's dropped-debugger retry logic verbatim, native-setter
  fill, single-tab pairing) but generic across sites, not oxalpha.com-
  specific. `adapter.js`'s `takeSnapshot()` is a faithful JS port of
  `internal/core/snapshot.go`'s element-selection/numbering algorithm
  (its DOM-walk half is copied from `snapshotJS` verbatim). Parity proven
  by a shared fixture (`extension/testdata/fixture.html`) loaded by both
  `internal/core/snapshot_parity_test.go` (chromedp, real headless
  Chrome) and `extension/snapshot.test.cjs` (Node's built-in test runner,
  real headless Chrome via a ~150-line zero-dependency CDP client in
  `extension/testsupport/cdp.cjs` -- jsdom/hand-mocked DOM were rejected
  because the algorithm depends on real getComputedStyle/
  getBoundingClientRect layout, which neither reproduces faithfully); both
  compare against a checked-in golden (`internal/core/testdata/
  fixture.golden.json`) and both pass. `blocked()` was generalized from
  ox's oxalpha-specific text matching to generic structural/vocabulary
  heuristics (password/email inputs, known challenge-provider iframes,
  generic verification/rate-limit phrasing). Navigation ("goto") is
  handled in `background.js` rather than mirroring ox's content-script
  poll loop, because a real page navigation destroys and reinjects the
  content script -- a deliberate, documented deviation from ox's
  structure, not from its techniques.

- 2026 09 10 (c): Added E12 (extension execution backend + Tailscale remote
  transport) after David asked for DGX/Rakazo agents to drive his real
  Chrome session overnight. Surveyed `~/Code/dndungu/ox/extension/` as
  prior art (read-only; that repo is untouched). Created ADR 006
  (extension backend, `core.PageDriver`, bridge protocol) and ADR 007
  (Tailscale-bound remote transport, bearer token). Added UC-013 through
  UC-018. Performed the plan's first trim pass: removed E7, E8, E10, E11
  (all fully shipped and merged; content preserved in `DESIGN.md`, ADRs
  001/002/004/005, and `docs/roadmap.md`'s Shipped section) and created
  `docs/devlog.md` to hold the Tier-3 investigation findings that were
  living in this file's now-removed "Implementation update" notes and
  T11.0's ported-fix commentary. E9 kept (T9.6 still open).
- 2026 09 10 (b): During `/apply` preflight, found a local-only worktree
  `ferro-wt-mcp` (branch `mcp-server`) already containing a working MCP
  server prototype; rebuilt E11 on top of it per David's decision. See
  `docs/devlog.md` for the full account (moved there during the 2026 09 10
  (c) trim).
- 2026 09 10 (a): Reconciled E7-E10 against actual code and test names;
  added E11. See `docs/devlog.md` for the full account (moved there during
  the 2026 09 10 (c) trim).
- 2026 09 04: Replaced the raw v0.1 extraction with a new plan. Added E7 to
  E10, archived E1 to E6 as outlines. Created ADR 001 and ADR 002. Created
  `docs/roadmap.md`.
