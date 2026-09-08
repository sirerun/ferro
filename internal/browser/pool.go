package browser

import (
	"context"
	"time"

	"github.com/chromedp/chromedp"
)

// taskContext is the per-task handle a caller gets from Browser.Acquire. It
// implements core.BrowserContext structurally (Navigate/CDP/
// SnapshotMaxElements/Release) without importing core, avoiding a cycle.
type taskContext struct {
	pc          *pooledContext
	b           *Browser
	maxElements int
	returned    bool
}

func (t *taskContext) Navigate(url string) error {
	return chromedp.Run(t.pc.cdpCtx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
	)
}

func (t *taskContext) CDP() context.Context     { return t.pc.cdpCtx }
func (t *taskContext) SnapshotMaxElements() int { return t.maxElements }

// Release returns the tab to the pool after resetting it. A reset that
// fails destroys the tab rather than recycling a poisoned one.
func (t *taskContext) Release() {
	if t.returned {
		return
	}
	t.returned = true

	// Best-effort reset: kill JS state, storage optional per config.
	resetCtx, cancel := context.WithTimeout(t.pc.cdpCtx, 3*time.Second)
	defer cancel()
	err := chromedp.Run(resetCtx,
		chromedp.Evaluate(`window.stop()`, nil),
		chromedp.Navigate("about:blank"),
	)

	t.b.mu.Lock()
	defer t.b.mu.Unlock()
	if err != nil || t.b.closed {
		// Poisoned tab: destroy. Cheap enough; pool refills on demand.
		delete(t.b.all, t.pc)
		t.pc.cancel()
		return
	}
	t.pc.busy = false
	t.b.idle = append(t.b.idle, t.pc)
}
