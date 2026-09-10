package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestDoFill_EmptyTextarea reproduces a dogfood finding from the ferro-mcp
// prototype (mcp-server branch, commit 6727b17): chromedp.Clear reads a
// textarea's existing value from its DOM child #text node, but a
// framework-controlled textarea (React, Vue, ...) never has one — its value
// lives in JS state, not static markup — so Clear failed on every such
// field, empty or not, aborting the whole fill step with "textarea node N
// does not have child #text node". A chat composer is exactly this shape.
func TestDoFill_EmptyTextarea(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("browser tests disabled")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// No default text content in the markup, mirroring a
		// framework-rendered, currently-uncontrolled textarea: Chrome gives
		// it zero DOM children, not an empty #text child.
		_, _ = w.Write([]byte(`<!doctype html><html><body><textarea placeholder="Message"></textarea></body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Flag("headless", true))...)
	defer cancelAlloc()
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()

	tctx, cancelTimeout := context.WithTimeout(ctx, 15*time.Second)
	defer cancelTimeout()
	if err := chromedp.Run(tctx, chromedp.Navigate(srv.URL)); err != nil {
		t.Fatal(err)
	}

	snap, err := TakeSnapshot(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	var ref int
	for _, e := range snap.Elements {
		if e.Tag == "textarea" {
			ref = e.Ref
		}
	}
	if ref == 0 {
		t.Fatalf("textarea not found in snapshot: %s", snap.Render())
	}

	x := NewExecutor(WaitStrategy{})
	ectx := withSnapshot(ctx, snap)
	if _, rerr := x.Execute(ectx, &Plan{Steps: []Action{
		{Kind: KindFill, Ref: ref, Text: "hello from ferro"},
		{Kind: KindDone},
	}}, extractStore{}); rerr != nil {
		t.Fatalf("fill on empty (childless) textarea failed: %v", rerr.Err)
	}

	var value string
	if err := chromedp.Run(ctx, chromedp.Value("textarea", &value, chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	if value != "hello from ferro" {
		t.Errorf("textarea value = %q, want %q", value, "hello from ferro")
	}
}

// TestExecuteOne_ClickAgainstSnapshot exercises ExecuteOne end to end
// against a real page: navigate, snapshot, then a single click driven
// through ExecuteOne rather than a full Plan -- this is exactly how
// internal/mcp's primitive click/fill/select/extract tools call the
// executor (T11.4).
func TestExecuteOne_ClickAgainstSnapshot(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("browser tests disabled")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><body>
			<button id="go" onclick="document.title='clicked'">Go</button>
			</body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Flag("headless", true))...)
	defer cancelAlloc()
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()

	tctx, cancelTimeout := context.WithTimeout(ctx, 15*time.Second)
	defer cancelTimeout()
	if err := chromedp.Run(tctx, chromedp.Navigate(srv.URL)); err != nil {
		t.Fatal(err)
	}

	snap, err := TakeSnapshot(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	var ref int
	for _, e := range snap.Elements {
		if e.Tag == "button" {
			ref = e.Ref
		}
	}
	if ref == 0 {
		t.Fatalf("button not found in snapshot: %s", snap.Render())
	}

	x := NewExecutor(WaitStrategy{})
	if _, err := x.ExecuteOne(ctx, snap, Action{Kind: KindClick, Ref: ref}, map[string]any{}); err != nil {
		t.Fatalf("ExecuteOne click failed: %v", err)
	}

	var title string
	if err := chromedp.Run(ctx, chromedp.Title(&title)); err != nil {
		t.Fatal(err)
	}
	if title != "clicked" {
		t.Errorf("title = %q, want %q", title, "clicked")
	}
}
