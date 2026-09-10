package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunTask_GatedByAllowlist is T11.5's acceptance test: a run_task call
// whose StartURL is allowlisted completes and returns a result plus
// LLMCalls/Repairs/Plannings metrics; a run_task call whose StartURL is not
// allowlisted returns an error before any LLM call is made.
func TestRunTask_GatedByAllowlist(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("set FERRO_TEST_BROWSER=1 to run browser tests (requires Chrome)")
	}

	fixture := fixtureServer(t)

	var llmCalls int64
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&llmCalls, 1)
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": searchPlan}}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(llm.Close)

	home := shortTempDir(t)
	// Deliberately empty allowlist: deny-by-default.
	if err := os.WriteFile(filepath.Join(home, "allowlist.json"), []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Config{Home: home, LLMBaseURL: llm.URL, LLMModel: "fake", Headless: true}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	o, err := NewOwner(ctx, cfg)
	if err != nil {
		t.Fatalf("NewOwner: %v", err)
	}
	defer func() { _ = o.Close() }()

	args, err := json.Marshal(RunTaskArgs{Goal: "search for coffee", StartURL: fixture.URL})
	if err != nil {
		t.Fatal(err)
	}

	// Deny path: fixture.URL is not allowlisted yet.
	text, isError, err := o.Call(ctx, "run_task", args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !isError {
		t.Fatalf("run_task against a non-allowlisted origin succeeded: %s", text)
	}
	if !strings.Contains(text, fixture.URL) {
		t.Errorf("denial %q does not name the blocked origin %q", text, fixture.URL)
	}
	if got := atomic.LoadInt64(&llmCalls); got != 0 {
		t.Errorf("llmCalls = %d, want 0 -- a denied run_task must never reach the planner", got)
	}

	// Allow path: extend the allowlist (no restart needed, per ADR 005) and
	// retry the identical call. Sleep past filesystems with 1s-granularity
	// mtimes so the reload actually triggers.
	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(home, "allowlist.json"), []byte(`["`+fixture.URL+`"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	text, isError, err = o.Call(ctx, "run_task", args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if isError {
		t.Fatalf("run_task against an allowlisted origin failed: %s", text)
	}
	var out RunTaskOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode result: %v\n%s", err, text)
	}
	if out.Metrics.LLMCalls == 0 {
		t.Error("metrics.LLMCalls = 0, want at least 1 once allowed")
	}
	if got := atomic.LoadInt64(&llmCalls); got == 0 {
		t.Error("the allowed retry never reached the fake LLM server")
	}
}
