# Devlog

## 2026-09-10: plan.md drifted from reality twice in one project; both times the fix was to run the acc: command, not read the note

The 2026-09-07 "Runtime hardening" roadmap entry claimed E7-E10 were
implemented and verified, citing test names: `TestRunOn`, `TestRunOn_SharedTab`,
`TestValidate_ExtractFields`, `TestExtract_RefSelector`,
`TestExtract_PartialFailure`, `TestExtract_Batch`, `TestPlanSchema_IsValidJSON`,
`TestValidatePlanShape`, `TestPlan_EnvelopeDrift`, `TestOpenAI_SchemaRequest`,
`TestOpenAI_SchemaFallback`, `TestContextLifetime`. None of these exist in the
repo. Reconciling on 2026-09-10 (`go test -run <name>` returning "no tests to
run" for each) found the underlying work was mostly real, just landed under
different names during a 2026-09-07 rewrite of `internal/core`:
`TestRuntimeRunOnAndCancellation`, `TestRuntimePartialExtraction`, and others.
Some items were genuinely never built (`ferro.PlanSchema()` export, the
HTTP-400 auto-fallback, the ADR-001 documentation pass) and stayed open
rather than being marked done on the strength of the note.

The same failure mode recurred inside the same reconciliation: T11.0's own
note (written 2026-09-10, same day) cited `TestRuntimePartialExtraction` as
proof that a `map[string]string` template-lookup bug from the `mcp-server`
prototype no longer applied to current `main` -- that test name does not
exist either. The underlying claim was still correct (verified by reading
`doExtract`'s Fields path and `extractValue` directly: current `main`'s
Fields path builds `map[string]any`, which `extractValue`'s single type
assertion already covers), but the citation was wrong. Corrected to point at
`TestShapeResult_ExpandsExtractLastField`, the test that actually exercises
this shape.

**Lesson, restated because it bit twice:** a plan note claiming a test proves
something is a hypothesis, not evidence, until the named test is actually run
and actually exists. `docs/plan.md`'s Operating Procedure section now says
this directly: run the `acc:` command yourself before checking a box.

## 2026-09-10: PR #3 (E11, ferro-mcp) was built from a stale base and needed a real rebase, not just a merge

The `ferro-mcp-e11` worktree/branch was created from local `main` before PR #2
(the E9/E10 closeout) was merged. A mid-flight instruction to the building
agent to rebase before pushing did not visibly take effect in an early check
(`git merge-base main pr3-check` still showed the pre-PR#2 commit) -- but this
was checking a stale local fetch of the PR branch, not its actual current
head; a fresh `git fetch origin pull/3/head` showed the agent had, in fact,
rebased and force-pushed correctly afterward. Lesson: when re-fetching a PR
branch to verify a claim about its state, confirm the fetched SHA matches
`gh pr view --json headRefOid` before trusting anything derived from it --
a stale local ref produces a confident, wrong answer.

Independently, before that rebase, comparing `main` against the (then-stale)
PR branch showed a real risk worth naming even though it didn't end up
happening: because the PR branch predated PR #2, a naive merge would have
silently reverted PR #2's ADR-001 "Context lifetime" documentation (the
package doc in `internal/browser/browser.go` and the `CDP()` method comment
in `internal/core/executor.go`) back to their pre-PR#2 short forms, since the
PR branch's own edits to those same files (for `ProfileDirectory`) were based
on the old, undocumented versions. The actual rebase preserved both PRs'
content correctly, but this is the general hazard: two PRs editing the same
file from different points in its history need a rebase resolved by reading
both sides' intent, never an automatic "ours" or "theirs."

## 2026-09-10: a SIGKILL in test cleanup orphaned a Chrome process because it skipped the code path meant to prevent that

`ferro-mcp`'s owner process (`internal/mcp/owner.go`) launches Chrome and is
supposed to close it via a deferred `Leader.Close()` on graceful shutdown.
The browser-gated test proving the owner survives its own stdio disconnecting
(T11.6, `TestOwnerSurvivesOwnStdioDisconnect`) originally cleaned up the owner
process with `Process.Kill()` (SIGKILL). SIGKILL cannot be caught, so it
bypassed the deferred cleanup entirely and left the owner's Chrome subprocess
(a grandchild of the test, not a child) running after the test finished.
Fixed with a `terminateGracefully` helper (`cmd/ferro-mcp/session_test.go`)
that sends SIGTERM first, which `main.go`'s `signal.NotifyContext` turns into
a cancelled context and its own deferred `leader.Close()`, falling back to
`Kill()` only if the process doesn't exit within 5 seconds. General lesson
for any test that launches a real subprocess with its own cleanup logic:
killing the test's direct child is not enough if that child itself launched
something -- match the shutdown signal the code is actually designed to
handle, and only escalate to an uncatchable kill as a last resort.

## 2026-09-07 (historical, superseded): initial runtime-completeness pass

The `fix/runtime-completeness` branch implemented plan replay, versioned
atomic selector/plan persistence with flush and error metrics, schema
extraction and final-result validation, typed planner shape errors with one
correction retry, bare-action repair, partial/ref extraction, `RunOn`, and
cancellation/concurrent-run isolation. The default race suite ran 12
top-level tests (12 browser tests skipped without Chrome); the Chrome-enabled
race suite ran all 24 with zero failures. This entry is kept for provenance
only -- see the 2026-09-10 entry above for what this pass's own status note
got wrong.


## 2026-09-22 — Existing Chrome session service completed

The previously separate extension and bridge used incompatible command envelopes
and the extension omitted the required tab header. The resolved `Command` format
now carries `op`, resolved selector, expected origin and a deadline. The missing
ExtensionDriver, runner snapshot plumbing and MCP backend selection are wired.

Safety-related behavior is tested: an expired queued action is never dispatched;
a canceled dispatched action is terminal/uncertain; pairing generations prevent
old tasks crossing into a newly paired tab; session leases prevent agents from
interleaving primitive sequences; shutdown/cancellation propagate through the
Unix relay; model-generated steps and page snapshots enforce the origin policy.
All bridge round trips are capped at five seconds, inside the overall call limit.
Polling is twenty seconds so it does not exceed MV3's fetch response idle window.

Validation on the implementation branch: `go build ./...`, `go vet ./...`,
`go test -race -p 1 ./...`, `FERRO_TEST_BROWSER=1 go test -race -p 1 ./...`,
and `FERRO_TEST_BROWSER=1 node --test extension/*.test.cjs` all pass. The tests
include real Chrome loading the real unpacked extension and exercising direct
actions plus a model-generated plan (fixture model endpoint), blocked/login
handoffs, independent MCP clients and process boundaries. A temporary binary
service on the actual local Tailscale interface accepted authenticated status
and rejected absent/wrong bearer tokens; it was stopped and its credentials removed.
No real account actions or cross-machine routing were tested.

Test-harness finding: installed and newly downloaded Chrome for Testing builds
could not reach plain loopback fixture pages on this machine. Regular Chrome
could. Browser-target `Extensions.loadUnpacked` on regular Chrome, enabled in a
disposable profile, resolved the harness requirement without changing the user's
Chrome settings. Calling that method on a page target incorrectly reports that
the method is unavailable. Temporary downloaded test-browser files were removed.

Ajent inbox/search/diagnose continued returning HTTP 409; prior findings were
unavailable. Inspection and verification used the local repositories instead.


## 2026-09-22 — Headless Claude pre-merge review

An independent headless Claude session reviewed the complete Chrome service
branch against main. Its four findings were corrected: selector caching now
uses an optional validation capability preserved by the CDP policy decorator;
the extension authorizes its command URL without duplicate owner/decorator
location queries; idle HTTP sessions outlive the longest tab lease; and the
origin-gating comment matches current behavior. Drivers without selector
validation do not populate the selector cache.

Regression tests exercise actual guarded CDP runner cache hits and policy
revocation, and count HTTP extension commands through Owner.Call: navigation
uses one command; snapshot/fill each use one location lookup plus one action.
Revoking the allowlist prevents the action from dispatching. Build, vet, the
full browser-enabled Go race suite, and all four Node extension tests pass.

Claude re-reviewed the fixes and reported all four resolved with no remaining
concrete findings. The reviewed branch was fast-forwarded into local main.


## 2026-09-22 — Standalone local Chrome chat

Implemented the supplied floating Glass Chat design in Chrome's side panel,
with a frame-colored light/dark background and custom theme color. Model setup,
explicit tab pairing, exact-origin policy editing, bounded chat retention,
export and cancellation are wired to the existing Go executor. The default
read-only driver prevents clicks, typing, selection and key presses even when
the model requests them. Interaction mode remains explicitly selectable.

Validation: full Go build/vet, full browser-enabled race suite and all four
Node extension tests passed. Real Chrome UI tests exercise settings and model
authentication, read-only refusal, successful interaction and extraction,
chat reload, HTML-as-text rendering, active provider cancellation and denial
of cancellation by a different session. Targeted browser/race tests passed
again after adding API-key redaction for provider errors; the test endpoint
intentionally echoes its fixture key and the transcript receives [redacted].

Installed a local extension-backend build, with a launcher and setup guide.
The default bridge port was occupied by an existing local dashboard; this
installation uses port 4175 with matching launcher/panel defaults. Owner status
confirmed the service running on loopback with no tab paired. No real profile was driven, no live model key was selected, and
no research campaign or outbound action was executed. Human extension loading
and provider/source qualification remain explicit acceptance steps.

The shared build lease found at verification had expired and predated the
machine's latest boot; no matching build process remained. It was released
with the canonical compare-and-swap primitive before acquiring our own lease.
Ajent inbox/search/diagnose continued returning HTTP 409; local repository
inspection and tests supplied the evidence.


## 2026-09-22 — Receiver missing on pre-existing Chrome tabs

Reproduced the operator's exact "Receiving end does not exist" failure by
opening a fixture tab before installing the extension. The previous test opened
its tab after installation and missed this lifecycle. Pairing had only checked
the Go bridge; it could report success without any page receiver.

Pairing now pings the top-frame receiver, injects the packaged adapter/relay
using Chrome scripting permission when absent, and verifies readiness before
claiming a connection. Injection still requires existing host or activeTab
authority; restricted pages return actionable errors. The relay is idempotent
when programmatic and document-idle injection overlap. Concurrent preparation
shares one injection. Readiness is checked before commands, and actual input
is sent once: a lost response no longer causes blind repeated input.

The pre-install-tab browser regression failed before the fix and passed after.
Targeted browser/race and Node checks cover the existing extension path,
attachment, denied access, injection coalescing and one-time command dispatch.
The installed extension files are updated separately from the running Go
service; Chrome must reload the extension to activate the changed manifest.


## 2026-09-22 — Cursor focus mark as Ferro logo

Created a cyan cursor-focus mark from a center dot, segmented aiming ring and
pointer tail, echoing Ox's animated target cursor. Added PNG sizes for Chrome's
manifest/action icons and retained the generated PNG master plus an SVG for UI
use. The side-panel wordmark pulses a light outer ring; reduced-motion settings
disable that animation. The old extension popup shares the mark. Updated the
extension version so Chrome recognizes the UI/icon change.
