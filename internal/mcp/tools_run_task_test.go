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

	"github.com/sirerun/ferro"
)

func TestRunTaskCanUseFerroChatModelProfile(t *testing.T) {
	home := shortTempDir(t)
	owner := &Owner{cfg: Config{
		Home: home, LLMBaseURL: "https://mcp.example/v1", LLMModel: "mcp-model", LLMAPIKey: "mcp-key",
		MaxRepairs: 2,
	}, runner: ferro.NewRunner(nil)}
	if err := privateJSON(filepath.Join(home, "chat-model.json"), chatModel{
		BaseURL: "https://openrouter.ai/api/v1", Model: "openai/gpt-4o-mini", APIKey: "chat-key",
	}); err != nil {
		t.Fatal(err)
	}

	chatClient, err := owner.taskProfileClientV1(context.Background(), "legacy-chat")
	if err != nil {
		t.Fatalf("resolve saved chat profile: %v", err)
	}
	client, ok := chatClient.(*ferro.OpenAICompatible)
	if !ok {
		t.Fatalf("chat client type = %T", chatClient)
	}
	if client.BaseURL != "https://openrouter.ai/api/v1" || client.Model != "openai/gpt-4o-mini" || client.APIKey != "chat-key" {
		t.Fatalf("chat runner did not use saved settings: endpoint=%q model=%q key=%q", client.BaseURL, client.Model, client.APIKey)
	}

	legacyRunner, err := owner.runnerForTaskProfileV1(context.Background(), "legacy-mcp")
	if err != nil {
		t.Fatalf("resolve default MCP profile: %v", err)
	}
	if legacyRunner != owner.runner {
		t.Fatal("legacy-mcp no longer uses the configured MCP runner")
	}
	if _, err := owner.runnerForTaskProfileV1(context.Background(), "unknown"); err == nil {
		t.Fatal("unknown model profile was accepted")
	}
}

func TestRunTaskUsesSelectedChatProfileForMCPGoal(t *testing.T) {
	home := shortTempDir(t)
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("provider path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer saved-chat-key" {
			t.Errorf("provider authorization = %q", got)
		}
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		if request.Model != "openai/gpt-4o-mini" {
			t.Errorf("provider model = %q", request.Model)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"steps":[{"kind":"done","result":"3 leads"}]}`}}}})
	}))
	t.Cleanup(provider.Close)
	if err := os.WriteFile(filepath.Join(home, "allowlist.json"), []byte(`["https://allowed.example"]`), 0600); err != nil {
		t.Fatal(err)
	}
	allow, err := NewAllowlist(filepath.Join(home, "allowlist.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := privateJSON(filepath.Join(home, "chat-model.json"), chatModel{
		BaseURL: provider.URL + "/v1", Model: "openai/gpt-4o-mini", APIKey: "saved-chat-key",
	}); err != nil {
		t.Fatal(err)
	}
	driver := newPolicyDriverSpy("https://allowed.example/people")
	owner := &Owner{
		cfg:   Config{Home: home, Backend: "fixture", BlockTimeout: 15 * time.Second, MaxElements: 100, MaxRepairs: 2},
		allow: allow, driver: driver, runner: ferro.NewRunner(nil), gate: make(chan struct{}, 1),
	}

	args, err := json.Marshal(RunTaskArgs{
		Goal:     "Research up to three matching profiles and return a concise result.",
		StartURL: "https://allowed.example/people", ModelProfile: "legacy-chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	text, failed, err := owner.Call(context.Background(), "run_task", args)
	if err != nil || failed {
		t.Fatalf("run_task failed=%v err=%v result=%s", failed, err, text)
	}
	var output RunTaskOutput
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("decode output: %v: %s", err, text)
	}
	if output.Result != "3 leads" || output.Metrics.LLMCalls != 1 || calls.Load() != 1 {
		t.Fatalf("unexpected result or model calls: output=%+v calls=%d", output, calls.Load())
	}
}

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
