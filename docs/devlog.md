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
