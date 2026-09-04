package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// evalAsPromise tells chromedp's Runtime.evaluate to await the expression's
// Promise and unmarshal its resolved value, instead of the raw Promise
// object (which fails to unmarshal into anything but **runtime.RemoteObject).
func evalAsPromise(p *runtime.EvaluateParams) *runtime.EvaluateParams {
	return p.WithAwaitPromise(true)
}

// domSettle waits until the page has had no DOM mutations for the debounce
// window, capped at the budget. Implemented as one in-page script: installs
// a MutationObserver, resolves when quiet. Single round trip.
//
// NOTE (kazi work plan E1-T3): "execution context destroyed" errors from
// Evaluate — which happen when a navigation interrupts the observer script —
// should be treated as settled (navigation implies the DOM changed), not as
// an error. That fix is tracked as an open task; this is the RFC's sketch.
const settleJS = `
(timeoutMs, debounceMs) => new Promise(resolve => {
  let last = Date.now();
  const obs = new MutationObserver(() => { last = Date.now(); });
  obs.observe(document, {childList: true, subtree: true, attributes: true});
  const iv = setInterval(() => {
    if (Date.now() - last >= debounceMs) {
      clearInterval(iv); obs.disconnect(); resolve('settled');
    }
  }, 50);
  setTimeout(() => { clearInterval(iv); obs.disconnect(); resolve('budget'); }, timeoutMs);
})`

// domSettle evaluates settleJS as an IIFE parameterized with the budget and
// debounce window (in milliseconds).
func domSettle(ctx context.Context, w WaitStrategy) error {
	var status string
	err := chromedp.Run(ctx, chromedp.Evaluate(
		fmt.Sprintf(`(%s)(%d, %d)`, settleJS, w.Budget.Milliseconds(), w.SettleDebounce.Milliseconds()),
		&status,
		evalAsPromise, // settleJS returns a Promise; await it, don't unmarshal it raw
	))
	_ = status
	if isNavigationInterrupted(err) {
		// A click that triggers navigation (form submit, link) tears down the
		// execution context the MutationObserver was running in before it can
		// resolve — CDP reports that as an error, but navigation itself is
		// conclusive proof the DOM changed, so treat it as settled rather
		// than propagate a failure for expected behavior.
		return nil
	}
	return err
}

// isNavigationInterrupted reports whether err is one of the CDP errors that
// fire when a page navigation destroys the JS context an Evaluate call was
// running in — not a real failure, just navigation racing the script.
func isNavigationInterrupted(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "execution context destroyed") ||
		strings.Contains(msg, "Execution context was destroyed") ||
		strings.Contains(msg, "Cannot find context with specified id") ||
		strings.Contains(msg, "Inspected target navigated or closed")
}
