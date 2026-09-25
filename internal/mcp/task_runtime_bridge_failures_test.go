package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntime_DispatchedNavigationCancellationRemainsUncertain(t *testing.T) {
	home := shortTempDir(t)
	if err := os.WriteFile(filepath.Join(home, "allowlist.json"), []byte(`["https://allowed.example"]`), 0600); err != nil {
		t.Fatal(err)
	}
	owner, err := NewOwner(context.Background(), Config{Home: home, Backend: "extension", BridgeAddr: "127.0.0.1:0", LLMBaseURL: "http://127.0.0.1:1", LLMModel: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	bridgeCall := func(method, path string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+owner.bridge.Addr()+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+owner.bridge.Token())
		req.Header.Set("X-Ferro-Tab-Id", "41")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	paired := bridgeCall("POST", "/pair")
	paired.Body.Close()
	if paired.StatusCode != http.StatusNoContent {
		t.Fatalf("pair status %d", paired.StatusCode)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	in := runtimeRequest("navigation-cancel")
	in.StartURL = "https://allowed.example/page"
	raw, _ := json.Marshal(in)
	done := make(chan struct{})
	var text string
	var failed bool
	var callErr error
	go func() { defer close(done); text, failed, callErr = owner.Call(ctx, "run_task", raw) }()
	dispatched := bridgeCall("GET", "/next")
	body, err := io.ReadAll(dispatched.Body)
	dispatched.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var command struct {
		Action struct {
			Op string `json:"op"`
		} `json:"action"`
	}
	if err = json.Unmarshal(body, &command); err != nil {
		t.Fatal(err)
	}
	if dispatched.StatusCode != 200 || command.Action.Op != "navigate" {
		t.Fatalf("unexpected command: %s", body)
	}
	// The browser received the navigation; lose its reply before cancellation.
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not finish")
	}
	if failed || callErr != nil {
		t.Fatalf("call failed=%v err=%v text=%s", failed, callErr, text)
	}
	var result TaskResult
	if err = json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != TaskOutcomeUncertain || result.SideEffectState != SideEffectUnknown || result.Error == nil || result.Error.Retry != "reconcile_only" {
		t.Fatalf("uncertain navigation erased: %+v", result)
	}
	receipt, err := owner.receipts.Lookup(context.Background(), privateReceiptOwner, in.TaskID)
	if err != nil || receipt.Result == nil || receipt.Result.Status != TaskOutcomeUncertain {
		t.Fatalf("uncertain receipt lost: %+v %v", receipt, err)
	}
}
