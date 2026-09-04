# ADR 001: The first context passed to chromedp.Run owns the tab's lifetime

## Status
Accepted

## Date
2026-09-04

## Context
Two separate defects in ferro (bug #1 and bug #7 in the dogfood session
of 2026-09-04) had the same root cause: a chromedp context was wrapped
(`context.WithTimeout`, `context.WithCancel`) and the wrapper was passed to
the *first* `chromedp.Run` on a tab. chromedp starts the tab's CDP
event-listener goroutine on that first Run and ties it to whichever context
it was given. Cancelling the wrapper later, even after a successful call,
tears the whole session down and every subsequent Run fails with
"invalid context" or "context canceled".

The rule is not documented in chromedp and the failure surfaces far from
the cause, so it will bite the next contributor.

## Decision
- The bare tab context (`pooledContext.cdpCtx`) is the only context ever
  passed to the *first* `chromedp.Run` on a tab. Launch deadlines are
  enforced with a `select` on a goroutine, never a `WithTimeout` wrapper.
- After launch, callers may derive short-lived children from `cdpCtx` for
  individual actions (as `taskContext.Release` does for its reset), because
  the listener is already bound to the parent.
- Snapshot and executor code must receive `BrowserContext.CDP()` (or a child
  of it), never the caller's plain `ctx`, or `chromedp.Run` reports
  "invalid context".
- The rule lives in three places: this ADR, a package-level comment in
  `internal/browser/browser.go`, and a section in `DESIGN.md`. Any new call
  site that wraps a CDP context cites this ADR in a comment.

## Consequences
- Positive: the class of "tab dies mid-run" bugs has a single documented
  cause and a grep-able convention.
- Negative: launch timeout handling is slightly more verbose than a
  `WithTimeout` one-liner. Accepted.
