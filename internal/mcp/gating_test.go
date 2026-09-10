package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dndungu/ferro/internal/core"
)

// gatingFixtureServer serves a minimal page with one of each element type
// the gated primitive tools need in order to exercise a real allow-path
// action, not just a pass-through origin check: a text input, a <select>,
// a plain button, and a landmark heading for extract to read. Deliberately
// its own tiny fixture rather than testdata/pages/shop.html -- shop.html is
// shared by nearly every other browser-gated test in this epic, and several
// of them hardcode its element refs, so adding a <select> to it risks
// renumbering refs those tests depend on.
func gatingFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><html><body>
			<h1 id="heading">Gate Test</h1>
			<input type="text" name="q" placeholder="Search">
			<select name="color"><option value="red">Red</option><option value="blue">Blue</option></select>
			<button type="button">Go</button>
		</body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestPrimitiveTools_AllowAndDenyPerGatedTool is T11.8's acceptance test:
// it exercises an allow case and a deny case for every ADR-005-gated tool
// -- navigate, click, fill, select, key, scroll, extract. (run_task's own
// allow/deny pair is covered separately by TestRunTask_GatedByAllowlist,
// since it needs a fake LLM server rather than this fixture.)
//
// The allow half drives a real DOM action against a live tab for every one
// of them, not just Allowlist.Check in isolation (already covered by
// allowlist_test.go) -- a regression that wired checkOrigin against the
// wrong target, or forgot to call it at all for one specific tool, would
// show up here even though the Allowlist type's own tests would still pass.
func TestPrimitiveTools_AllowAndDenyPerGatedTool(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("set FERRO_TEST_BROWSER=1 to run browser tests (requires Chrome)")
	}

	fixture := gatingFixtureServer(t)
	home := shortTempDir(t)
	allowlistPath := filepath.Join(home, "allowlist.json")
	if err := os.WriteFile(allowlistPath, []byte(`["`+fixture.URL+`"]`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, Headless: true}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	o, err := NewOwner(ctx, cfg)
	if err != nil {
		t.Fatalf("NewOwner: %v", err)
	}
	defer func() { _ = o.Close() }()

	call := func(tool string, args any) (string, bool) {
		t.Helper()
		argsJSON, err := json.Marshal(args)
		if err != nil {
			t.Fatalf("%s: marshal args: %v", tool, err)
		}
		text, isError, err := o.Call(ctx, tool, argsJSON)
		if err != nil {
			t.Fatalf("%s: Call: %v", tool, err)
		}
		return text, isError
	}

	// --- Allow: fixture.URL is allowlisted, so every gated tool must
	// actually reach its real DOM action.
	if text, isError := call("navigate", navigateArgs{URL: fixture.URL}); isError {
		t.Fatalf("navigate (allow) returned an error: %s", text)
	}

	snapText, isError := call("snapshot", snapshotArgs{})
	if isError {
		t.Fatalf("snapshot returned an error: %s", snapText)
	}
	var snap core.Snapshot
	if err := json.Unmarshal([]byte(snapText), &snap); err != nil {
		t.Fatalf("decode snapshot: %v\n%s", err, snapText)
	}
	refByTag := func(tag string) int {
		t.Helper()
		for _, e := range snap.Elements {
			if e.Tag == tag {
				return e.Ref
			}
		}
		t.Fatalf("no %s element in snapshot: %+v", tag, snap.Elements)
		return 0
	}
	inputRef := refByTag("input")
	selectRef := refByTag("select")
	buttonRef := refByTag("button")

	if text, isError := call("fill", fillArgs{Ref: inputRef, Text: "hello"}); isError {
		t.Fatalf("fill (allow) returned an error: %s", text)
	}
	if text, isError := call("select", selectArgs{Ref: selectRef, Value: "blue"}); isError {
		t.Fatalf("select (allow) returned an error: %s", text)
	}
	if text, isError := call("key", keyArgs{Text: "Enter"}); isError {
		t.Fatalf("key (allow) returned an error: %s", text)
	}
	if text, isError := call("scroll", scrollArgs{To: "bottom"}); isError {
		t.Fatalf("scroll (allow) returned an error: %s", text)
	}
	if text, isError := call("click", clickArgs{Ref: buttonRef}); isError {
		t.Fatalf("click (allow) returned an error: %s", text)
	}
	extractText, isError := call("extract", extractArgs{Fields: map[string]string{"heading": "#heading"}})
	if isError {
		t.Fatalf("extract (allow) returned an error: %s", extractText)
	}
	var extracted map[string]any
	if err := json.Unmarshal([]byte(extractText), &extracted); err != nil {
		t.Fatalf("decode extract result: %v\n%s", err, extractText)
	}
	if extracted["heading"] != "Gate Test" {
		t.Fatalf("extract (allow) = %+v, want heading %q", extracted, "Gate Test")
	}

	// --- Deny: rewrite the allowlist to empty. Sleep past 1s-granularity
	// filesystem mtimes so the reload actually observes the change (ADR
	// 005's no-restart-needed hot reload).
	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(allowlistPath, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}

	origin, err := originOf(fixture.URL)
	if err != nil {
		t.Fatal(err)
	}
	denyCases := []struct {
		tool string
		args any
	}{
		{"navigate", navigateArgs{URL: fixture.URL}},
		{"click", clickArgs{Ref: buttonRef}},
		{"fill", fillArgs{Ref: inputRef, Text: "blocked"}},
		{"select", selectArgs{Ref: selectRef, Value: "red"}},
		{"key", keyArgs{Text: "Enter"}},
		{"scroll", scrollArgs{To: "top"}},
		{"extract", extractArgs{Fields: map[string]string{"heading": "#heading"}}},
	}
	for _, tc := range denyCases {
		t.Run(tc.tool, func(t *testing.T) {
			text, isError := call(tc.tool, tc.args)
			if !isError {
				t.Fatalf("%s (deny) succeeded, want an allowlist error: %s", tc.tool, text)
			}
			if !strings.Contains(text, origin) {
				t.Fatalf("%s (deny) error %q does not name the blocked origin %q", tc.tool, text, origin)
			}
		})
	}
}
