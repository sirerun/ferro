package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirerun/ferro/internal/core"
)

const runtimePlan = `{"steps":[{"kind":"extract","fields":{"value":"#value"}},{"kind":"done","result":"{{extract.last}}"}]}`

type runtimeDriver struct {
	*policyDriverSpy
	value   string
	entered chan struct{}
}

func (d *runtimeDriver) ExtractField(ctx context.Context, _ string) (string, error) {
	d.called("extract_field")
	if d.entered != nil {
		close(d.entered)
		<-ctx.Done()
		return "", ctx.Err()
	}
	return d.value, nil
}
func runtimeFixture(t *testing.T, plan string) (*Owner, *runtimeDriver, *atomic.Int64) {
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
	driver := &runtimeDriver{policyDriverSpy: newPolicyDriverSpy("https://allowed.example/page"), value: "first"}
	o := &Owner{cfg: Config{Home: home, Backend: "fixture", LLMBaseURL: provider.URL, LLMModel: "fixture", BlockTimeout: time.Minute, MaxElements: 100}, allow: allow, driver: driver, gate: make(chan struct{}, 1), stopCh: make(chan struct{})}
	if err := o.initializeTasks(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })
	return o, driver, calls
}
func runtimeRequest(id string) RunTaskRequest {
	return RunTaskRequest{Schema: "ferro.task/v2", TaskID: id, Goal: "read the value", ModelProfile: "legacy-mcp", Policy: &TaskPolicy{Mode: "read_only", Origins: []string{"https://allowed.example"}}, OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`)}
}
func callRuntime(t *testing.T, o *Owner, in RunTaskRequest) TaskResult {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	text, failed, err := o.Call(context.Background(), "run_task", raw)
	if err != nil || failed {
		t.Fatalf("call failed=%v err=%v result=%s", failed, err, text)
	}
	var result TaskResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTaskResult(result); err != nil {
		t.Fatalf("invalid envelope: %v: %s", err, text)
	}
	return result
}
func TestRuntime_ResultUsageAndDeduplication(t *testing.T) {
	o, d, calls := runtimeFixture(t, runtimePlan)
	in := runtimeRequest("once")
	result := callRuntime(t, o, in)
	if result.Status != TaskSucceeded || string(result.Result) != `{"value":"first"}` || result.Usage.TotalTokens == nil || *result.Usage.TotalTokens != 30 {
		t.Fatalf("bad result: %+v", result)
	}
	d.value = "second"
	same := callRuntime(t, o, in)
	if same.ExecutionID != result.ExecutionID || calls.Load() != 1 || d.calls["extract_field"] != 1 {
		t.Fatal("duplicate executed again")
	}
	in.Goal = "changed"
	raw, _ := json.Marshal(in)
	_, failed, _ := o.Call(context.Background(), "run_task", raw)
	if !failed || calls.Load() != 1 {
		t.Fatal("changed duplicate did not conflict")
	}
	if err := o.receipts.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenReceiptStore(filepath.Join(o.cfg.Home, "tasks-v2"), 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	o.receipts = store
	text, failed, err := o.Call(context.WithValue(context.Background(), clientKey{}, "fresh-client"), "get_task_receipt", json.RawMessage(`{"task_id":"once"}`))
	if err != nil || failed || !strings.Contains(text, result.ExecutionID) {
		t.Fatalf("fresh client recovery: %s %v", text, err)
	}
}

func TestRunTaskRejectsAdvancedFieldsWithoutTaskIDBeforeExecution(t *testing.T) {
	for _, field := range []string{
		`"task_id":""`,
		`"task_id":null`,
		`"policy":{"mode":"read_only","origins":["https://allowed.example"]}`,
		`"Policy":{"mode":"read_only","origins":["https://allowed.example"]}`,
		`"limits":{"actions":1}`,
		`"output_schema":{"type":"object"}`,
		`"replay_key":"retry"`,
		`"evidence":"compact"`,
	} {
		t.Run(field, func(t *testing.T) {
			o, driver, providerCalls := runtimeFixture(t, runtimePlan)
			args := json.RawMessage(`{"goal":"inspect the page",` + field + `}`)
			_, failed, err := o.Call(context.Background(), "run_task", args)
			if err != nil || !failed {
				t.Fatalf("advanced request without task_id: failed=%v err=%v", failed, err)
			}
			if providerCalls.Load() != 0 || len(driver.calls) != 0 {
				t.Fatalf("invalid request performed work: provider_calls=%d browser_calls=%v", providerCalls.Load(), driver.calls)
			}
		})
	}
}

func TestRunTaskSimpleRejectsUnknownFieldsBeforeExecution(t *testing.T) {
	o, driver, providerCalls := runtimeFixture(t, runtimePlan)
	_, failed, err := o.Call(context.Background(), "run_task", json.RawMessage(`{"goal":"inspect the page","polciy":{"mode":"read_only"}}`))
	if err != nil || !failed {
		t.Fatalf("unknown request field was accepted: failed=%v err=%v", failed, err)
	}
	if providerCalls.Load() != 0 || len(driver.calls) != 0 {
		t.Fatalf("unknown request field performed work: provider_calls=%d browser_calls=%v", providerCalls.Load(), driver.calls)
	}
}

type failingKeyDriver struct{ *runtimeDriver }

func (d *failingKeyDriver) Key(context.Context, string) error {
	d.called("key")
	return errors.New("key response lost")
}

func TestRuntime_RecordsBrowserSideEffects(t *testing.T) {
	plan := `{"steps":[{"kind":"key","text":"Enter"},{"kind":"done","result":{"value":"submitted"}}]}`
	t.Run("successful mutation", func(t *testing.T) {
		o, _, _ := runtimeFixture(t, plan)
		in := runtimeRequest("clicked")
		in.Policy.Mode = "read_write"
		result := callRuntime(t, o, in)
		if result.Status != TaskSucceeded || result.SideEffectState != SideEffectConfirmed {
			t.Fatalf("successful write receipt lost side-effect state: %+v", result)
		}
	})
	t.Run("uncertain mutation failure", func(t *testing.T) {
		o, driver, _ := runtimeFixture(t, plan)
		failing := &failingKeyDriver{runtimeDriver: driver}
		o.driver = failing
		in := runtimeRequest("uncertain-click")
		in.Policy.Mode = "read_write"
		result := callRuntime(t, o, in)
		if result.Status != TaskFailed || result.SideEffectState != SideEffectUnknown {
			t.Fatalf("failed dispatched write must be marked unknown: %+v", result)
		}
	})
}

func TestSideEffectTrackerKeepsEarlierUncertaintyAfterLaterSuccess(t *testing.T) {
	var tracker sideEffectTracker
	tracker.observe(SideEffectUnknown, false)
	tracker.observe(SideEffectUnknown, true)
	tracker.observe(SideEffectUnknown, false)
	tracker.observe(SideEffectConfirmed, true)
	if got := tracker.state(); got != SideEffectUnknown {
		t.Fatalf("later success erased an earlier uncertain write: state=%s", got)
	}
}

func TestRuntime_WarmReplayReadsCurrentFacts(t *testing.T) {
	o, d, calls := runtimeFixture(t, runtimePlan)
	in := runtimeRequest("warm1")
	in.ReplayLabel = "same-work"
	first := callRuntime(t, o, in)
	if first.Status != TaskSucceeded {
		t.Fatal(first)
	}
	d.value = "updated"
	in.TaskID = "warm2"
	second := callRuntime(t, o, in)
	if second.Status != TaskSucceeded || string(second.Result) != `{"value":"updated"}` || calls.Load() != 1 || second.Budget.Requests != 0 || d.calls["extract_field"] != 2 {
		t.Fatalf("warm replay cached facts or called provider: %+v calls=%d", second, calls.Load())
	}
}
func TestRuntime_PolicyAndBudgets(t *testing.T) {
	t.Run("deny before model", func(t *testing.T) {
		o, _, calls := runtimeFixture(t, runtimePlan)
		if err := os.WriteFile(o.cfg.AllowlistPath(), []byte(`[]`), 0600); err != nil {
			t.Fatal(err)
		}
		result := callRuntime(t, o, runtimeRequest("denied"))
		if result.Status != TaskBlocked || calls.Load() != 0 {
			t.Fatalf("denied=%+v calls=%d", result, calls.Load())
		}
	})
	t.Run("model cannot mutate", func(t *testing.T) {
		o, d, calls := runtimeFixture(t, `{"steps":[{"kind":"key","text":"Enter"},{"kind":"done","result":{}}]}`)
		result := callRuntime(t, o, runtimeRequest("mutation"))
		if result.Status != TaskBlocked || d.calls["key"] != 0 || calls.Load() != 1 {
			t.Fatalf("mutation=%+v calls=%v", result, d.calls)
		}
	})
	t.Run("action ceiling", func(t *testing.T) {
		o, _, calls := runtimeFixture(t, runtimePlan)
		in := runtimeRequest("bounded")
		one := int64(1)
		in.Limits.Actions = &one
		result := callRuntime(t, o, in)
		if result.Status != TaskBudgetExhausted || result.Budget.Actions != 1 || calls.Load() != 1 {
			t.Fatalf("unbounded=%+v calls=%d", result, calls.Load())
		}
	})
	t.Run("invalid output retains usage", func(t *testing.T) {
		o, _, _ := runtimeFixture(t, `{"steps":[{"kind":"done","result":{"value":42}}]}`)
		result := callRuntime(t, o, runtimeRequest("schema"))
		if result.Status == TaskSucceeded || len(result.Result) != 0 || result.Usage.TotalTokens == nil {
			t.Fatalf("invalid schema success or usage lost: %+v", result)
		}
	})
}
func TestRuntime_CancellationRetainsReceipt(t *testing.T) {
	o, d, _ := runtimeFixture(t, runtimePlan)
	d.entered = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	raw, _ := json.Marshal(runtimeRequest("cancelled"))
	done := make(chan struct{})
	go func() { defer close(done); _, _, _ = o.Call(ctx, "run_task", raw) }()
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
	receipt, err := o.receipts.Lookup(context.Background(), privateReceiptOwner, "cancelled")
	if err != nil || receipt.Result == nil || receipt.Result.Status != TaskCancelled || receipt.Result.Budget.Requests != 1 {
		t.Fatalf("lost cancelled receipt: %+v %v", receipt, err)
	}
}
func TestRuntime_MCPWireAndReceiptTools(t *testing.T) {
	o, _, calls := runtimeFixture(t, runtimePlan)
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
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var runTaskFound bool
	for _, tool := range tools.Tools {
		if tool.Name == "run_task" {
			runTaskFound = true
			if tool.Annotations == nil || tool.Annotations.ReadOnlyHint {
				t.Fatal("run_task must be advertised as read/write")
			}
		}
	}
	if !runTaskFound {
		t.Fatal("run_task is not registered")
	}
	in := runtimeRequest("wire")
	raw, _ := json.Marshal(in)
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "run_task", Arguments: args})
	if err != nil || result.IsError {
		t.Fatalf("MCP run: %+v %v", result, err)
	}
	receipt, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "get_task_receipt", Arguments: map[string]any{"task_id": "wire"}})
	if err != nil || receipt.IsError {
		t.Fatalf("MCP receipt: %+v %v", receipt, err)
	}
	args["task_id"] = "invalid"
	args["owner"] = "attacker"
	result, err = session.CallTool(ctx, &sdk.CallToolParams{Name: "run_task", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || calls.Load() != 1 {
		t.Fatal("unknown owner field accepted or dispatched")
	}
}

var _ core.PageDriver = (*runtimeDriver)(nil)
