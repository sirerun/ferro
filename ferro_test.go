package ferro

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// fakeLLM returns canned plans — no network, fully deterministic tests of
// the executor/runner machinery. The planner prompt itself needs a real
// model; that's an integration test behind a build tag (see integration/).
type fakeLLM struct {
	plan  string
	calls int
}

func (f *fakeLLM) Complete(ctx context.Context, system, user string) (string, error) {
	f.calls++
	return f.plan, nil
}

// testServer serves a tiny static site with a search box and results.
func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><body>
			<h1>Shop</h1>
			<form action="/search">
				<input type="text" name="q" placeholder="Search products">
				<button type="submit">Go</button>
			</form></body></html>`))
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		_, _ = w.Write([]byte(`<!doctype html><html><body>
			<h1>Results for ` + q + `</h1>
			<a href="/item/1">Coffee 1kg — $15.00</a>
			<a href="/item/2">Coffee 500g — $9.00</a>
			</body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestEndToEnd_SearchAndExtract exercises the public API end to end: a fake
// LLM (canned plan) driving a real headless Chrome through Runner.Run.
//
// Known sharp edges (see README.md / DESIGN.md):
//  1. Ref numbering mismatch — a hardcoded plan's refs depend on exactly
//     which elements the snapshot picks up.
//  2. dom_settle racing the form submit — a navigation can destroy the JS
//     execution context evaluating the settle promise.
//  3. {{extract.last}} with no prior Extract — this canned plan never runs
//     an Extract step, so the done step below uses a literal result.
func TestEndToEnd_SearchAndExtract(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("set FERRO_TEST_BROWSER=1 to run browser tests (requires Chrome)")
	}

	srv := testServer(t)

	// Canned plan: fill search, click go, wait for settle, done.
	llm := &fakeLLM{plan: `{"steps":[
		{"kind":"fill","ref":2,"text":"coffee"},
		{"kind":"click","ref":3},
		{"kind":"wait","for":"dom_settle"},
		{"kind":"done","result":"searched"}
	]}`}

	b, err := NewBrowser(BrowserConfig{Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bctx, err := b.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer bctx.Release()

	runner := NewRunner(llm)
	result, metrics, err := runner.Run(ctx, bctx, Task{
		Goal:     "search for coffee",
		StartURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result == nil {
		t.Fatal("nil result")
	}
	if metrics.LLMCalls != 1 {
		t.Errorf("LLMCalls = %d, want 1", metrics.LLMCalls)
	}
	if llm.calls != 1 {
		t.Errorf("llm.calls = %d, want 1", llm.calls)
	}
}

// TestRun_Convenience exercises the package-level Run helper.
func TestRun_Convenience(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("set FERRO_TEST_BROWSER=1 to run browser tests (requires Chrome)")
	}

	srv := testServer(t)
	llm := &fakeLLM{plan: `{"steps":[{"kind":"done","result":"ok"}]}`}

	b, err := NewBrowser(BrowserConfig{Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, _, err := Run(ctx, b, llm, Task{Goal: "no-op", StartURL: srv.URL})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result != "ok" {
		t.Errorf("result = %v, want %q", result, "ok")
	}
}
