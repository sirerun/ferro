# Ferro Git inventory and review — 2026-09-24

Historical inventory captured before later branch archival and plan reconciliation. Re-run the baseline gate for current branch, archive and installed-state decisions.

Origin was fetched before comparison; origin/main is daa5fd8. This is a source and selected-test review, not full browser/security qualification. Existing dirty worktrees were not modified, stashed, rebased or merged.

## Plan corrections

Commit 8f0be08 adds F04 session/CSRF as an A06 checkout prerequisite and requires real middleware boundary tests. New A19 owns database wiring; I03 depends on A19; A18 live adoption/restore depends on A19, I03 and F13. Database adoption remains a release prerequisite. Validator passes 67 tasks / 23 dependency frontiers.

## Branch disposition

| Branch | Evidence | Recommended action |
|---|---|---|
| codex/hosted-launch-plan | Based on current origin/main; plan and corrective commits only | Ready for plan review; no rebase needed now |
| codex/ferro-chat-polish | Tree exactly equals origin/main despite different commit history | Already shipped by squash; no rebase or merge needed |
| main / codex/chrome-api-service | Old five-commit browser-service history; origin contains squashed later work | Preserve dirty main changes before aligning local main; do not replay shipped commits |
| codex/amsl-credentials | One distinct credential adapter commit f2d8244 above the old browser-service base | Port that bounded commit onto current origin/main; resolve current MCP/config/module/docs integration, then validate |
| codex/local-workflow | Old local-chat history plus dirty changes; lacks later safety fixes | Port only the remaining new changes onto current origin/main; do not replace current files wholesale |
| docs/t12-0-shipped, docs/t12-1-shipped, docs/wave1-complete | Historical shipped documentation branches; remote tracking refs gone | Archive candidates after final patch/content audit, not launch blockers |
| fix/runtime-completeness, worktree-agent-a3ced456c29cd71e4 | No commits ahead of origin/main; ancestor branches | Already contained; no rebase needed |

## Dirty worktrees

There are no staged changes. Root main: five modified tracked files and one untracked popup test. Local-workflow: nine modified tracked files and four untracked prompt files. Other listed worktrees are clean.

Root main changes add a single connection/disconnection button, bridge reachability status, reconnect messaging, status protocol tests, and README guidance to use the actual bridge port. These changes should be ported against current origin/main, preserving its newer pairing security and cleanup behavior.

Local-workflow changes add chat selection/history, chat export details and glass styles, automatic active-tab following, side-panel handoff, and embedded text prompt files. The manifest version, popup HTML, side-panel HTML and styles already match origin/main. Most chat history behavior is also already shipped. Prompt extraction remains new; planner text also adds an instruction to prioritize the fresh page snapshot over conversation history. Copying the complete old files would regress newer UTF-8 history limits, disconnect warnings, stale-ref handling and pairing safety.

## Newly observed defects (not modified)

- P2, local-workflow extension/background.js:352-383: tab switching stops polling before disconnect/pair fetches. A rejected fetch (network error or timeout) goes directly to the outer catch without restarting polling or reconciling pairing. The HTTP-error recovery path does not cover transport exceptions. Persisted connection can remain configured while automation stops.
- P2, local-workflow extension/sidepanel.js:304: onActivated forwards every window's tab activation and drops windowId. A panel can re-pair when another Chrome window changes active tab. Bind following to its owning window and verify multi-window behavior before adoption.
- P2, root extension/popup.js:26-39: disconnect failures populate actionError, but configured-state rendering ignores it. A failed disconnect is replaced with ordinary connection/reconnect status, concealing why the requested action failed. Add a failure-path test when porting.

## Verification and limitations

- Plan validator and git diff --check pass.
- Root popup/protocol tests: 7 passed.
- Local-workflow protocol tests: 8 passed.
- Compared polish tree with origin/main: identical.
- No Go builds, real Chrome runs, rebase trials or merges performed. Exact conflict lists require a future isolated integration attempt; named collision areas are source-based expectations.
- Ajent inbox remains unavailable (HTTP 409); no empty-inbox claim.

Recommended order: review/merge plan; preserve and port connection-status changes; separately port prompt extraction; resolve the tab-following product choice in the design round and repair its failure paths; integrate the bounded AMSL adapter on a fresh base. No historical branch needs a blanket rebase.
