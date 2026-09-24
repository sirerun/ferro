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

	"github.com/dndungu/ferro/internal/core"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const runtimePlanV2 = `{"steps":[{"kind":"extract","fields":{"value":"#value"}},{"kind":"done","result":"{{extract.last}}"}]}`

type runtimeDriverV2 struct {
	*policyDriverSpyV2
	value   string
	entered chan struct{}
}

func (d *runtimeDriverV2) ExtractField(ctx context.Context, _ string) (string, error) {
	d.called("extract_field")
	if d.entered != nil {
		close(d.entered)
		<-ctx.Done()
		return "", ctx.Err()
	}
	return d.value, nil
}
func runtimeFixtureV2(t *testing.T, plan string) (*Owner, *runtimeDriverV2, *atomic.Int64) {
	t.Helper()
	calls := new(atomic.Int64)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": plan}}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30}})
	}))
	t.Cleanup(provider.Close)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "allowlist.json"), []byte(`["https://allowed.example"]`), 0600); err != nil {
		t.Fatal(err)
	}
	allow, err := NewAllowlist(filepath.Join(home, "allowlist.json"))
	if err != nil {
		t.Fatal(err)
	}
	driver := &runtimeDriverV2{policyDriverSpyV2: newPolicyDriverSpyV2("https://allowed.example/page"), value: "first"}
	o := &Owner{cfg: Config{Home: home, Backend: "fixture", LLMBaseURL: provider.URL, LLMModel: "fixture", BlockTimeout: time.Minute, MaxElements: 100}, allow: allow, driver: driver, gate: make(chan struct{}, 1), stopCh: make(chan struct{})}
	if err := o.initializeTasksV2(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })
	return o, driver, calls
}
func runtimeRequestV2(id string) RunTaskV2Request {
	return RunTaskV2Request{Schema: "ferro.task/v2", TaskID: id, Goal: "read the value", ModelProfile: "legacy-mcp", Policy: &TaskPolicyV2{Mode: "read_only", Origins: []string{"https://allowed.example"}}, OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`)}
}
func callRuntimeV2(t *testing.T, o *Owner, in RunTaskV2Request) TaskResultV2 {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	text, failed, err := o.Call(context.Background(), "run_task_v2", raw)
	if err != nil || failed {
		t.Fatalf("call failed=%v err=%v result=%s", failed, err, text)
	}
	var result TaskResultV2
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTaskResultV2(result); err != nil {
		t.Fatalf("invalid envelope: %v: %s", err, text)
	}
	return result
}
func TestRuntimeV2_ResultUsageAndDeduplication(t *testing.T) {
	o, d, calls := runtimeFixtureV2(t, runtimePlanV2)
	in := runtimeRequestV2("once")
	result := callRuntimeV2(t, o, in)
	if result.Status != TaskSucceededV2 || string(result.Result) != `{"value":"first"}` || result.Usage.TotalTokens == nil || *result.Usage.TotalTokens != 30 {
		t.Fatalf("bad result: %+v", result)
	}
	d.value = "second"
	same := callRuntimeV2(t, o, in)
	if same.ExecutionID != result.ExecutionID || calls.Load() != 1 || d.calls["extract_field"] != 1 {
		t.Fatal("duplicate executed again")
	}
	in.Goal = "changed"
	raw, _ := json.Marshal(in)
	_, failed, _ := o.Call(context.Background(), "run_task_v2", raw)
	if !failed || calls.Load() != 1 {
		t.Fatal("changed duplicate did not conflict")
	}
	if err := o.receipts.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenReceiptStoreV2(filepath.Join(o.cfg.Home, "tasks-v2"), 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	o.receipts = store
	text, failed, err := o.Call(context.WithValue(context.Background(), clientKey{}, "fresh-client"), "get_task_receipt", json.RawMessage(`{"task_id":"once"}`))
	if err != nil || failed || !strings.Contains(text, result.ExecutionID) {
		t.Fatalf("fresh client recovery: %s %v", text, err)
	}
}
func TestRuntimeV2_WarmReplayReadsCurrentFacts(t *testing.T) {
	o, d, calls := runtimeFixtureV2(t, runtimePlanV2)
	in := runtimeRequestV2("warm1")
	in.ReplayLabel = "same-work"
	first := callRuntimeV2(t, o, in)
	if first.Status != TaskSucceededV2 {
		t.Fatal(first)
	}
	d.value = "updated"
	in.TaskID = "warm2"
	second := callRuntimeV2(t, o, in)
	if second.Status != TaskSucceededV2 || string(second.Result) != `{"value":"updated"}` || calls.Load() != 1 || second.Budget.Requests != 0 || d.calls["extract_field"] != 2 {
		t.Fatalf("warm replay cached facts or called provider: %+v calls=%d", second, calls.Load())
	}
}
func TestRuntimeV2_PolicyAndBudgets(t *testing.T) {
	t.Run("deny before model", func(t *testing.T) {
		o, _, calls := runtimeFixtureV2(t, runtimePlanV2)
		if err := os.WriteFile(o.cfg.AllowlistPath(), []byte(`[]`), 0600); err != nil {
			t.Fatal(err)
		}
		result := callRuntimeV2(t, o, runtimeRequestV2("denied"))
		if result.Status != TaskBlockedV2 || calls.Load() != 0 {
			t.Fatalf("denied=%+v calls=%d", result, calls.Load())
		}
	})
	t.Run("model cannot mutate", func(t *testing.T) {
		o, d, calls := runtimeFixtureV2(t, `{"steps":[{"kind":"key","text":"Enter"},{"kind":"done","result":{}}]}`)
		result := callRuntimeV2(t, o, runtimeRequestV2("mutation"))
		if result.Status != TaskBlockedV2 || d.calls["key"] != 0 || calls.Load() != 1 {
			t.Fatalf("mutation=%+v calls=%v", result, d.calls)
		}
	})
	t.Run("action ceiling", func(t *testing.T) {
		o, _, calls := runtimeFixtureV2(t, runtimePlanV2)
		in := runtimeRequestV2("bounded")
		one := int64(1)
		in.Limits.Actions = &one
		result := callRuntimeV2(t, o, in)
		if result.Status != TaskBudgetExhaustedV2 || result.Budget.Actions != 1 || calls.Load() != 1 {
			t.Fatalf("unbounded=%+v calls=%d", result, calls.Load())
		}
	})
	t.Run("invalid output retains usage", func(t *testing.T) {
		o, _, _ := runtimeFixtureV2(t, `{"steps":[{"kind":"done","result":{"value":42}}]}`)
		result := callRuntimeV2(t, o, runtimeRequestV2("schema"))
		if result.Status == TaskSucceededV2 || len(result.Result) != 0 || result.Usage.TotalTokens == nil {
			t.Fatalf("invalid schema success or usage lost: %+v", result)
		}
	})
}
func TestRuntimeV2_CancellationRetainsReceipt(t *testing.T) {
	o, d, _ := runtimeFixtureV2(t, runtimePlanV2)
	d.entered = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	raw, _ := json.Marshal(runtimeRequestV2("cancelled"))
	done := make(chan struct{})
	go func() { defer close(done); _, _, _ = o.Call(ctx, "run_task_v2", raw) }()
	select {
	case <-d.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("task never reached extraction")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("task did not cancel")
	}
	receipt, err := o.receipts.Lookup(context.Background(), privateReceiptOwnerV2, "cancelled")
	if err != nil || receipt.Result == nil || receipt.Result.Status != TaskCancelledV2 || receipt.Result.Budget.Requests != 1 {
		t.Fatalf("lost cancelled receipt: %+v %v", receipt, err)
	}
}
func TestRuntimeV2_MCPWireAndReceiptTools(t *testing.T) {
	o, _, calls := runtimeFixtureV2(t, runtimePlanV2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, b := sdk.NewInMemoryTransports()
	ss, err := NewServer(o).Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	client := sdk.NewClient(&sdk.Implementation{Name: "v2-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	in := runtimeRequestV2("wire")
	raw, _ := json.Marshal(in)
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "run_task_v2", Arguments: args})
	if err != nil || result.IsError {
		t.Fatalf("MCP run: %+v %v", result, err)
	}
	receipt, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "get_task_receipt", Arguments: map[string]any{"task_id": "wire"}})
	if err != nil || receipt.IsError {
		t.Fatalf("MCP receipt: %+v %v", receipt, err)
	}
	args["task_id"] = "invalid"
	args["owner"] = "attacker"
	result, err = session.CallTool(ctx, &sdk.CallToolParams{Name: "run_task_v2", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || calls.Load() != 1 {
		t.Fatal("unknown owner field accepted or dispatched")
	}
}

var _ core.PageDriver = (*runtimeDriverV2)(nil)
