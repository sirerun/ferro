package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

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
