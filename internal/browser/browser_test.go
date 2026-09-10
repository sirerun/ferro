package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/dndungu/ferro/internal/core"
)

// TestSnapshot_InteractiveElements tests the page compiler against inline
// HTML through a pooled tab — no LLM, no planning. This is where
// selector/visibility bugs surface cheapest.
func TestSnapshot_InteractiveElements(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("browser tests disabled")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><body>
			<h1>Shop</h1>
			<form action="/search">
				<input type="text" name="q" placeholder="Search products">
				<button type="submit">Go</button>
			</form></body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	b, err := New(Config{Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	bctx, err := b.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer bctx.Release()

	if err := bctx.Navigate(srv.URL); err != nil {
		t.Fatal(err)
	}

	snap, err := core.TakeSnapshot(bctx.CDP(), 100)
	if err != nil {
		t.Fatal(err)
	}

	// Expectations against the fixture: h1 "Shop" (landmark), input
	// (textbox), button "Go".
	var sawInput, sawButton bool
	for _, e := range snap.Elements {
		if e.Tag == "input" {
			sawInput = true
			if e.Name != "Search products" {
				t.Errorf("input name = %q, want placeholder", e.Name)
			}
		}
		if e.Tag == "button" && strings.Contains(e.Text, "Go") {
			sawButton = true
		}
	}
	if !sawInput || !sawButton {
		t.Errorf("snapshot missing elements: input=%v button=%v\nrender:\n%s",
			sawInput, sawButton, snap.Render())
	}
}

// TestContextLifetime is the regression test for ADR 001 (docs/adr/001-chromedp-context-lifetime.md):
// the context passed to the *first* chromedp.Run on a tab owns that tab's
// CDP event-listener goroutine for the tab's entire lifetime.
func TestContextLifetime(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("browser tests disabled")
	}

	t.Run("wrapping the first Run's context kills the tab on cancel", func(t *testing.T) {
		// Deliberately reproduce the landmine outside newTab's safeguards:
		// build a bare chromedp context and pass a WithTimeout wrapper of
		// it — not the bare context itself — to the first chromedp.Run.
		allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
			chromedp.NoFirstRun,
			chromedp.NoDefaultBrowserCheck,
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
		)
		defer cancelAlloc()
		cdpCtx, cancelCdp := chromedp.NewContext(allocCtx)
		defer cancelCdp()

		wrapped, cancelWrap := context.WithTimeout(cdpCtx, 10*time.Second)
		if err := chromedp.Run(wrapped, chromedp.Navigate("about:blank")); err != nil {
			cancelWrap()
			t.Fatalf("first Run (via wrapped context) should succeed: %v", err)
		}

		// Cancelling the wrapper after a successful call tears the whole
		// session down per ADR 001, even though nothing is in flight.
		cancelWrap()

		if err := chromedp.Run(cdpCtx, chromedp.Navigate("about:blank")); err == nil {
			t.Fatal("expected the next Run on this tab to fail once the first Run's context was cancelled, got nil error")
		}
	})

	t.Run("the pool's select-based launch survives normal use", func(t *testing.T) {
		// Inverse of the above: Browser.newTab enforces its launch deadline
		// with a select on a goroutine and only ever passes the bare
		// pooledContext.cdpCtx to the first chromedp.Run (see browser.go's
		// package doc and newTab). A tab acquired this way must keep
		// working across repeated Runs.
		b, err := New(Config{Headless: true})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = b.Close() }()

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		bctx, err := b.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer bctx.Release()

		if err := chromedp.Run(bctx.CDP(), chromedp.Navigate("about:blank")); err != nil {
			t.Fatalf("first Run on the pooled tab: %v", err)
		}
		if err := chromedp.Run(bctx.CDP(), chromedp.Navigate("about:blank")); err != nil {
			t.Fatalf("second Run on the same pooled tab should still succeed: %v", err)
		}
	})
}
