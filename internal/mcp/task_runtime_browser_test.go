package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Only the provider is simulated: this exercises Chrome, the shipped extension,
// the authenticated bridge, HTTP MCP, the policy driver and durable receipts.
func TestRuntime_ChromeIntegration(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("set FERRO_TEST_BROWSER=1; requires Chrome")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><body><h1>Read only fixture</h1><div id="value">fresh browser fact</div></body></html>`))
	}))
	defer fixture.Close()
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": runtimePlan}}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30}})
	}))
	defer provider.Close()
	home := shortTempDir(t)
	origins, _ := json.Marshal([]string{fixture.URL})
	if err := os.WriteFile(filepath.Join(home, "allowlist.json"), origins, 0600); err != nil {
		t.Fatal(err)
	}
	owner, err := NewOwner(ctx, Config{Home: home, Backend: "extension", BridgeAddr: "127.0.0.1:0", LLMBaseURL: provider.URL, LLMModel: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	helper := exec.CommandContext(ctx, "node", filepath.Join("..", "..", "extension", "testsupport", "paired-browser.cjs"))
	helper.Stderr = os.Stderr
	helper.Cancel = func() error { return helper.Process.Signal(os.Interrupt) }
	helper.WaitDelay = 5 * time.Second
	stdin, err := helper.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = helper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		if err := helper.Wait(); err != nil {
			t.Errorf("extension helper: %v", err)
		}
	}()
	if err = json.NewEncoder(stdin).Encode(map[string]string{"url": fixture.URL, "base": "http://" + owner.bridge.Addr(), "token": owner.bridge.Token()}); err != nil {
		t.Fatal(err)
	}
	scan := bufio.NewScanner(stdout)
	if !scan.Scan() || scan.Text() != "paired" {
		t.Fatal("extension did not pair")
	}
	srv := httptest.NewServer(remoteHandler(owner, "integration-token", ""))
	defer srv.Close()
	client := sdk.NewClient(&sdk.Implementation{Name: "v2-chrome", Version: "1"}, nil)
	session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: authTransport{"integration-token"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	in := runtimeRequest("chrome-read")
	in.Policy.Origins = []string{fixture.URL}
	call := func() TaskResult {
		t.Helper()
		response, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "run_task", Arguments: in})
		if err != nil {
			t.Fatal(err)
		}
		if response.IsError {
			t.Fatalf("task error: %+v", response.Content)
		}
		var result TaskResult
		if err := json.Unmarshal([]byte(response.Content[0].(*sdk.TextContent).Text), &result); err != nil {
			t.Fatal(err)
		}
		if err := ValidateTaskResult(result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := call()
	if first.Status != TaskSucceeded || string(first.Result) != `{"value":"fresh browser fact"}` || first.Usage.TotalTokens == nil || *first.Usage.TotalTokens != 30 {
		t.Fatalf("unexpected browser result: %+v", first)
	}
	again := call()
	if again.ExecutionID != first.ExecutionID || calls.Load() != 1 {
		t.Fatalf("duplicate dispatched: calls=%d", calls.Load())
	}
	receipt, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "get_task_receipt", Arguments: map[string]any{"execution_id": first.ExecutionID}})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.IsError {
		t.Fatalf("receipt error: %+v", receipt.Content)
	}
}
