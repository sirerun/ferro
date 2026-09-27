package mcp

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirerun/ferro/internal/core"
)

func qualificationSession(t *testing.T, o *Owner) (*sdk.ClientSession, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	serverSession, err := NewServer(o).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "g03-qualification", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, ctx
}

func qualificationArgs(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var args map[string]any
	if err = json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	return args
}

func qualificationCall(t *testing.T, session *sdk.ClientSession, ctx context.Context, name string, args map[string]any) (*sdk.CallToolResult, string) {
	t.Helper()
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("MCP %s: %v", name, err)
	}
	if len(result.Content) == 0 {
		t.Fatalf("MCP %s returned no content", name)
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("MCP %s returned %T", name, result.Content[0])
	}
	return result, text.Text
}

func qualifiedProvider(t *testing.T, o *Owner, handler http.HandlerFunc) *atomic.Int64 {
	t.Helper()
	count := new(atomic.Int64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); handler(w, r) }))
	t.Cleanup(server.Close)
	o.cfg.LLMBaseURL = server.URL
	o.cfg.LLMModel = "qualification-fixture"
	o.cfg.LLMAPIKey = ""
	return count
}
func writeCompletion(w http.ResponseWriter, plan string, usage bool) {
	w.Header().Set("Content-Type", "application/json")
	out := map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": plan}}}}
	if usage {
		out["usage"] = map[string]int{"prompt_tokens": 9, "completion_tokens": 11, "total_tokens": 20}
	}
	_ = json.NewEncoder(w).Encode(out)
}
func readMCPResult(t *testing.T, text string) TaskResult {
	t.Helper()
	var result TaskResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("decode result: %v: %s", err, text)
	}
	if err := ValidateTaskResult(result); err != nil {
		t.Fatalf("invalid task result: %v: %s", err, text)
	}
	return result
}

func TestTaskRuntimeQualification_InvalidSchemaAndDeniedOriginDoNoProviderWork(t *testing.T) {
	t.Run("invalid schema preflight", func(t *testing.T) {
		o, driver, _ := runtimeFixture(t, runtimePlan)
		calls := qualifiedProvider(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletion(w, runtimePlan, true) })
		session, ctx := qualificationSession(t, o)
		in := runtimeRequest("invalid_schema")
		in.OutputSchema = json.RawMessage(`{"$ref":"https://example.org/remote.json"}`)
		result, _ := qualificationCall(t, session, ctx, "run_task", qualificationArgs(t, in))
		if !result.IsError || calls.Load() != 0 || driver.calls["extract_field"] != 0 {
			t.Fatalf("isError=%v provider=%d driver=%v", result.IsError, calls.Load(), driver.calls)
		}
	})
	t.Run("service origin denied", func(t *testing.T) {
		o, driver, _ := runtimeFixture(t, runtimePlan)
		if err := os.WriteFile(o.cfg.AllowlistPath(), []byte(`[]`), 0600); err != nil {
			t.Fatal(err)
		}
		calls := qualifiedProvider(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletion(w, runtimePlan, true) })
		session, ctx := qualificationSession(t, o)
		result, text := qualificationCall(t, session, ctx, "run_task", qualificationArgs(t, runtimeRequest("service_denied")))
		if result.IsError {
			t.Fatalf("expected admitted blocked envelope, got %s", text)
		}
		out := readMCPResult(t, text)
		if out.Status != TaskBlocked || calls.Load() != 0 || driver.calls["extract_field"] != 0 {
			t.Fatalf("status=%s provider=%d driver=%v", out.Status, calls.Load(), driver.calls)
		}
	})
}

func TestTaskRuntimeQualification_MalformedPlanStopsAtExactRequestCeiling(t *testing.T) {
	o, _, _ := runtimeFixture(t, runtimePlan)
	calls := qualifiedProvider(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletion(w, "not a plan", true) })
	one := int64(1)
	in := runtimeRequest("malformed_plan")
	in.Limits.ModelRequests = &one
	in.Limits.PlanningPasses = &one
	session, ctx := qualificationSession(t, o)
	result, text := qualificationCall(t, session, ctx, "run_task", qualificationArgs(t, in))
	if result.IsError {
		t.Fatalf("run task transport error: %s", text)
	}
	out := readMCPResult(t, text)
	if out.Status != TaskBudgetExhausted || out.Budget.Requests != 1 || calls.Load() != 1 {
		t.Fatalf("status=%s requests=%d provider=%d result=%s", out.Status, out.Budget.Requests, calls.Load(), text)
	}
}

func TestTaskRuntimeQualification_MissingProviderUsageRemainsUnknown(t *testing.T) {
	o, _, _ := runtimeFixture(t, runtimePlan)
	calls := qualifiedProvider(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletion(w, runtimePlan, false) })
	session, ctx := qualificationSession(t, o)
	result, text := qualificationCall(t, session, ctx, "run_task", qualificationArgs(t, runtimeRequest("usage_unknown")))
	if result.IsError {
		t.Fatalf("MCP returned error: %s", text)
	}
	out := readMCPResult(t, text)
	if out.Status != TaskSucceeded || calls.Load() != 1 || out.Usage.InputTokens != nil || out.Usage.OutputTokens != nil || out.Usage.TotalTokens != nil {
		t.Fatalf("usage fabricated or task failed: %+v calls=%d", out.Usage, calls.Load())
	}
	if out.Budget.UncertainRequests != 1 {
		t.Fatalf("unknown usage not reserved: %+v", out.Budget)
	}
}

func TestTaskRuntimeQualification_LostProviderResponseRetainsUnknownReceipt(t *testing.T) {
	o, _, _ := runtimeFixture(t, runtimePlan)
	calls := qualifiedProvider(t, o, func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("fixture HTTP server does not support hijacking")
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("drop provider response: %v", err)
			return
		}
		_ = conn.Close() // request arrived; response and usage were lost
	})
	session, ctx := qualificationSession(t, o)
	result, text := qualificationCall(t, session, ctx, "run_task", qualificationArgs(t, runtimeRequest("provider_response_lost")))
	if result.IsError {
		t.Fatalf("expected durable result envelope after provider loss: %s", text)
	}
	out := readMCPResult(t, text)
	if out.Status == TaskSucceeded || len(out.Result) != 0 || out.ResultArtifactID != "" || out.Budget.UncertainRequests != 1 || out.Usage.TotalTokens != nil || calls.Load() != 1 {
		t.Fatalf("lost provider response fabricated success or lost uncertainty: %+v provider=%d", out, calls.Load())
	}
}

type disconnectedExtractDriver struct {
	core.PageDriver
	calls atomic.Int64
}

func (d *disconnectedExtractDriver) ExtractField(context.Context, string) (string, error) {
	d.calls.Add(1)
	return "", &core.StopError{Code: "disconnected", Message: "fixture transport disconnected"}
}

func TestTaskRuntimeQualification_BrowserDisconnectReturnsUncertainReceipt(t *testing.T) {
	o, driver, _ := runtimeFixture(t, runtimePlan)
	disconnected := &disconnectedExtractDriver{PageDriver: driver}
	o.driver = disconnected
	calls := qualifiedProvider(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletion(w, runtimePlan, true) })
	session, ctx := qualificationSession(t, o)
	_, text := qualificationCall(t, session, ctx, "run_task", qualificationArgs(t, runtimeRequest("browser_disconnect")))
	receipt := readMCPResult(t, text)
	if receipt.Status != TaskOutcomeUncertain || receipt.SideEffectState != SideEffectUnknown || receipt.Error == nil || receipt.Error.Retry != "reconcile_only" || receipt.Result != nil || receipt.ResultArtifactID != "" || calls.Load() != 1 || disconnected.calls.Load() != 1 {
		t.Fatalf("disconnect was not retained as uncertain: %+v provider=%d extraction=%d", receipt, calls.Load(), disconnected.calls.Load())
	}
}

func TestTaskRuntimeQualification_LargeExtractedResultReadBackByMCP(t *testing.T) {
	o, driver, _ := runtimeFixture(t, runtimePlan)
	driver.value = strings.Repeat("x", 20000)
	calls := qualifiedProvider(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletion(w, runtimePlan, true) })
	in := runtimeRequest("large_artifact")
	in.Evidence = EvidenceArtifacts
	in.OutputSchema = json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`)
	session, ctx := qualificationSession(t, o)
	result, text := qualificationCall(t, session, ctx, "run_task", qualificationArgs(t, in))
	if result.IsError {
		t.Fatalf("run task error: %s", text)
	}
	out := readMCPResult(t, text)
	if out.Status != TaskSucceeded || len(out.Result) != 0 || out.ResultArtifactID == "" || calls.Load() != 1 {
		t.Fatalf("artifact result: %+v calls=%d", out, calls.Load())
	}
	meta := out.Artifacts[0]
	if meta.ID != out.ResultArtifactID || meta.Size <= 16384 || meta.MediaType != "application/json" {
		t.Fatalf("bad artifact metadata: %+v", meta)
	}
	var data []byte
	for offset := int64(0); offset < meta.Size; {
		limit := int64(65536)
		if meta.Size-offset < limit {
			limit = meta.Size - offset
		}
		args := map[string]any{"execution_id": out.ExecutionID, "artifact_id": meta.ID, "offset": offset, "limit": limit}
		_, chunkText := qualificationCall(t, session, ctx, "read_task_artifact", args)
		var response struct {
			Data   string `json:"data"`
			Offset int64  `json:"offset"`
			Bytes  int    `json:"bytes"`
		}
		if err := json.Unmarshal([]byte(chunkText), &response); err != nil {
			t.Fatal(err)
		}
		chunk, err := base64.StdEncoding.DecodeString(response.Data)
		if err != nil {
			t.Fatal(err)
		}
		if response.Offset != offset || response.Bytes != len(chunk) || len(chunk) == 0 {
			t.Fatalf("bad chunk response: %+v decoded=%d", response, len(chunk))
		}
		data = append(data, chunk...)
		offset += int64(len(chunk))
	}
	digest := sha256.Sum256(data)
	if int64(len(data)) != meta.Size || hex.EncodeToString(digest[:]) != meta.SHA256 {
		t.Fatalf("artifact digest/size mismatch size=%d meta=%+v", len(data), meta)
	}
	var value map[string]string
	if err := json.Unmarshal(data, &value); err != nil || value["value"] != driver.value {
		t.Fatalf("artifact data invalid: %v value-len=%d", err, len(value["value"]))
	}
}

func TestTaskRuntimeQualification_WarmReplayExtractsCurrentFactsThroughMCP(t *testing.T) {
	o, driver, _ := runtimeFixture(t, runtimePlan)
	calls := qualifiedProvider(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletion(w, runtimePlan, true) })
	session, ctx := qualificationSession(t, o)
	firstRequest := runtimeRequest("mcp_warm_first")
	firstRequest.ReplayLabel = "same-work"
	first, firstText := qualificationCall(t, session, ctx, "run_task", qualificationArgs(t, firstRequest))
	if first.IsError {
		t.Fatalf("first MCP run: %s", firstText)
	}
	firstResult := readMCPResult(t, firstText)
	if firstResult.Status != TaskSucceeded || string(firstResult.Result) != `{"value":"first"}` {
		t.Fatalf("first result=%+v", firstResult)
	}
	driver.value = "current-after-replay"
	secondRequest := firstRequest
	secondRequest.TaskID = "mcp_warm_second"
	second, secondText := qualificationCall(t, session, ctx, "run_task", qualificationArgs(t, secondRequest))
	if second.IsError {
		t.Fatalf("second MCP run: %s", secondText)
	}
	secondResult := readMCPResult(t, secondText)
	if secondResult.Status != TaskSucceeded || string(secondResult.Result) != `{"value":"current-after-replay"}` || secondResult.Budget.Requests != 0 || calls.Load() != 1 || driver.calls["extract_field"] != 2 {
		t.Fatalf("MCP warm replay stale/called model: result=%+v provider=%d driver=%v", secondResult, calls.Load(), driver.calls)
	}
}

type repairFailExtractDriver struct{ *runtimeDriver }

func (d *repairFailExtractDriver) ExtractField(context.Context, string) (string, error) {
	d.called("extract_field")
	return "", fmt.Errorf("fixture extraction failure")
}

func TestTaskRuntimeQualification_InitialAndRepairedMutationPlansNeverDispatch(t *testing.T) {
	initialMutation := `{"steps":[{"kind":"key","text":"Enter"},{"kind":"done","result":{"value":"x"}}]}`
	t.Run("initial plan", func(t *testing.T) {
		o, driver, _ := runtimeFixture(t, initialMutation)
		calls := qualifiedProvider(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletion(w, initialMutation, true) })
		session, ctx := qualificationSession(t, o)
		result, text := qualificationCall(t, session, ctx, "run_task", qualificationArgs(t, runtimeRequest("initial_mutation")))
		if result.IsError {
			t.Fatalf("MCP: %s", text)
		}
		out := readMCPResult(t, text)
		if out.Status != TaskBlocked || calls.Load() != 1 || driver.calls["key"] != 0 {
			t.Fatalf("initial mutation dispatched: %+v provider=%d driver=%v", out, calls.Load(), driver.calls)
		}
	})
	t.Run("repair proposal", func(t *testing.T) {
		firstPlan := `{"steps":[{"kind":"extract","fields":{"value":"#value"}},{"kind":"done","result":"{{extract.last}}"}]}`
		o, base, _ := runtimeFixture(t, firstPlan)
		driver := &repairFailExtractDriver{runtimeDriver: base}
		o.driver = driver
		var requests atomic.Int64
		calls := qualifiedProvider(t, o, func(w http.ResponseWriter, _ *http.Request) {
			if requests.Add(1) == 1 {
				writeCompletion(w, firstPlan, true)
			} else {
				writeCompletion(w, `{"kind":"key","text":"Enter"}`, true)
			}
		})
		session, ctx := qualificationSession(t, o)
		result, text := qualificationCall(t, session, ctx, "run_task", qualificationArgs(t, runtimeRequest("repair_mutation")))
		if result.IsError {
			t.Fatalf("MCP: %s", text)
		}
		out := readMCPResult(t, text)
		if out.Status != TaskBlocked || calls.Load() != 2 || driver.calls["key"] != 0 {
			t.Fatalf("repair mutation dispatched: %+v provider=%d driver=%v", out, calls.Load(), driver.calls)
		}
	})
	t.Run("blocked replay candidate is never cached", func(t *testing.T) {
		mutation := `{"steps":[{"kind":"key","text":"Enter"},{"kind":"done","result":{"value":"x"}}]}`
		valid := runtimePlan
		o, driver, _ := runtimeFixture(t, mutation)
		var requests atomic.Int64
		calls := qualifiedProvider(t, o, func(w http.ResponseWriter, _ *http.Request) {
			if requests.Add(1) == 1 {
				writeCompletion(w, mutation, true)
			} else {
				writeCompletion(w, valid, true)
			}
		})
		session, ctx := qualificationSession(t, o)
		first := runtimeRequest("replay_mutation_first")
		first.ReplayLabel = "same-replay"
		one, text := qualificationCall(t, session, ctx, "run_task", qualificationArgs(t, first))
		if one.IsError {
			t.Fatalf("first MCP: %s", text)
		}
		first.TaskID = "replay_mutation_second"
		two, text := qualificationCall(t, session, ctx, "run_task", qualificationArgs(t, first))
		if two.IsError {
			t.Fatalf("second MCP: %s", text)
		}
		out := readMCPResult(t, text)
		if out.Status != TaskSucceeded || calls.Load() != 2 || driver.calls["key"] != 0 {
			t.Fatalf("unsafe replay or mutation call: %+v provider=%d driver=%v", out, calls.Load(), driver.calls)
		}
	})
}

func TestTaskRuntimeQualificationRestartChild(t *testing.T) {
	if os.Getenv("FERRO_G03_RESTART_CHILD") != "1" {
		return
	}
	home := os.Getenv("FERRO_G03_RESTART_HOME")
	providerURL := os.Getenv("FERRO_G03_RESTART_PROVIDER")
	o, base, _ := runtimeFixture(t, runtimePlan)
	if err := o.receipts.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "allowlist.json"), []byte(`["https://allowed.example"]`), 0600); err != nil {
		t.Fatal(err)
	}
	allow, err := NewAllowlist(filepath.Join(home, "allowlist.json"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenReceiptStore(filepath.Join(home, "tasks-v2"), 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	o.cfg.Home, o.cfg.LLMBaseURL, o.cfg.LLMModel = home, providerURL, "qualification-fixture"
	o.allow, o.receipts = allow, store
	blocking := &restartBlockingDriver{runtimeDriver: base, entered: make(chan struct{})}
	o.driver = blocking
	session, ctx := qualificationSession(t, o)
	args := qualificationArgs(t, runtimeRequest("restart_inflight"))
	go func() { _, _ = session.CallTool(ctx, &sdk.CallToolParams{Name: "run_task", Arguments: args}) }()
	select {
	case <-blocking.entered:
		fmt.Fprintln(os.Stdout, "READY")
	case <-ctx.Done():
		t.Fatal("task did not reach blocked browser operation")
	}
	select {} // The parent kills this process to simulate an unclean crash.
}

type restartBlockingDriver struct {
	*runtimeDriver
	entered chan struct{}
	once    sync.Once
}

func (d *restartBlockingDriver) ExtractField(context.Context, string) (string, error) {
	d.called("extract_field")
	d.once.Do(func() { close(d.entered) })
	select {} // The parent kills the subprocess while execution is in flight.
}

func TestTaskRuntimeQualification_RestartReconcilesInflightReceiptWithoutRedispatch(t *testing.T) {
	home := t.TempDir()
	var providerCalls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		writeCompletion(w, runtimePlan, true)
	}))
	defer provider.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestTaskRuntimeQualificationRestartChild$")
	cmd.Env = append(os.Environ(), "FERRO_G03_RESTART_CHILD=1", "FERRO_G03_RESTART_HOME="+home, "FERRO_G03_RESTART_PROVIDER="+provider.URL)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lineCh := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); lineCh <- strings.TrimSpace(line) }()
	select {
	case line := <-lineCh:
		if line != "READY" {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("child failed before admitted execution: ready=%q stderr=%s", line, stderr.String())
		}
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("child did not enter execution; stderr=%s", stderr.String())
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err == nil {
		t.Fatal("crashed subprocess exited successfully after kill")
	}
	store, err := OpenReceiptStore(filepath.Join(home, "tasks-v2"), 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	o := &Owner{cfg: Config{Home: home, BlockTimeout: time.Minute}, receipts: store, gate: make(chan struct{}, 1), stopCh: make(chan struct{})}
	session, ctx := qualificationSession(t, o)
	_, receiptText := qualificationCall(t, session, ctx, "get_task_receipt", map[string]any{"task_id": "restart_inflight"})
	var receipt Receipt
	if err = json.Unmarshal([]byte(receiptText), &receipt); err != nil {
		t.Fatalf("decode recovered receipt: %v: %s", err, receiptText)
	}
	if receipt.State != ReceiptUncertain || receipt.TaskID != "restart_inflight" {
		t.Fatalf("in-flight receipt not reconciled: %+v", receipt)
	}
	// A duplicate after restart returns that uncertain receipt; it must not dispatch.
	_, duplicateText := qualificationCall(t, session, ctx, "run_task", qualificationArgs(t, runtimeRequest("restart_inflight")))
	var duplicate Receipt
	if err = json.Unmarshal([]byte(duplicateText), &duplicate); err != nil {
		t.Fatalf("decode duplicate response: %v: %s", err, duplicateText)
	}
	if duplicate.State != ReceiptUncertain || providerCalls.Load() != 1 {
		t.Fatalf("restart redispatched: state=%s provider calls=%d", duplicate.State, providerCalls.Load())
	}
}

func TestTaskRuntimeDuplicateReceiptBypassesOtherSessionLease(t *testing.T) {
	o, driver, providerCalls := runtimeFixture(t, runtimePlan)
	sessionA, ctxA := qualificationSession(t, o)
	sessionB, ctxB := qualificationSession(t, o)
	in := runtimeRequest("lease_duplicate")
	args := qualificationArgs(t, in)

	first, _ := qualificationCall(t, sessionA, ctxA, "run_task", args)
	if first.IsError {
		t.Fatal("initial task failed")
	}
	_, leaseText := qualificationCall(t, sessionB, ctxB, "acquire_tab", map[string]any{})
	if !strings.Contains(leaseText, "acquired") {
		t.Fatalf("session B did not acquire the tab: %s", leaseText)
	}

	duplicate, duplicateText := qualificationCall(t, sessionA, ctxA, "run_task", args)
	if duplicate.IsError {
		t.Fatalf("same task did not return its receipt: %s", duplicateText)
	}
	var repeated TaskResult
	if err := json.Unmarshal([]byte(duplicateText), &repeated); err != nil {
		t.Fatalf("decode repeated result: %v: %s", err, duplicateText)
	}
	var initial TaskResult
	if err := json.Unmarshal([]byte(first.Content[0].(*sdk.TextContent).Text), &initial); err != nil {
		t.Fatalf("decode initial result: %v", err)
	}
	if repeated.ExecutionID != initial.ExecutionID || providerCalls.Load() != 1 || driver.calls["extract_field"] != 1 {
		t.Fatalf("duplicate re-executed or changed receipt: initial=%+v repeated=%+v provider=%d actions=%v", initial, repeated, providerCalls.Load(), driver.calls)
	}

	changed := in
	changed.Goal = "a different request"
	conflict, conflictText := qualificationCall(t, sessionA, ctxA, "run_task", qualificationArgs(t, changed))
	if !conflict.IsError || providerCalls.Load() != 1 || driver.calls["extract_field"] != 1 {
		t.Fatalf("changed same-key request was not rejected without execution: isError=%v text=%s provider=%d actions=%v", conflict.IsError, conflictText, providerCalls.Load(), driver.calls)
	}
	receipt, err := o.receipts.Lookup(context.Background(), privateReceiptOwner, in.TaskID)
	if err != nil || receipt.ExecutionID != initial.ExecutionID {
		t.Fatalf("receipt principal or identity changed: receipt=%+v err=%v", receipt, err)
	}
}

func TestTaskRuntimeQualification_ConcurrentDuplicateAndChangedInput(t *testing.T) {
	o, _, _ := runtimeFixture(t, runtimePlan)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	calls := qualifiedProvider(t, o, func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		writeCompletion(w, runtimePlan, true)
	})
	session, ctx := qualificationSession(t, o)
	in := runtimeRequest("concurrent_duplicate")
	args := qualificationArgs(t, in)
	first := make(chan struct {
		res  *sdk.CallToolResult
		text string
	}, 1)
	go func() {
		res, text := qualificationCallSafe(session, ctx, "run_task", args)
		first <- struct {
			res  *sdk.CallToolResult
			text string
		}{res, text}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("provider was not entered")
	}
	secondSession, secondCtx := qualificationSession(t, o)
	duplicateStarted := make(chan struct{})
	duplicate := make(chan struct {
		res  *sdk.CallToolResult
		text string
	}, 1)
	go func() {
		close(duplicateStarted)
		res, text := qualificationCallSafe(secondSession, secondCtx, "run_task", args)
		duplicate <- struct {
			res  *sdk.CallToolResult
			text string
		}{res, text}
	}()
	<-duplicateStarted
	releaseOnce.Do(func() { close(release) })
	select {
	case got := <-first:
		if got.res == nil || got.res.IsError {
			t.Fatalf("original call failed: %s", got.text)
		}
		_ = readMCPResult(t, got.text)
	case <-ctx.Done():
		t.Fatal("original call did not finish")
	}
	select {
	case got := <-duplicate:
		if got.res == nil || got.res.IsError {
			t.Fatalf("same-key duplicate errored: %s", got.text)
		}
	case <-secondCtx.Done():
		t.Fatal("same-key duplicate did not return the existing receipt")
	}
	changed := in
	changed.Goal = "changed input"
	conflict, conflictText := qualificationCall(t, secondSession, secondCtx, "run_task", qualificationArgs(t, changed))
	if !conflict.IsError || calls.Load() != 1 {
		t.Fatalf("changed input did not conflict: isError=%v calls=%d %s", conflict.IsError, calls.Load(), conflictText)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider attempts=%d", calls.Load())
	}
}

func qualificationCallSafe(session *sdk.ClientSession, ctx context.Context, name string, args map[string]any) (*sdk.CallToolResult, string) {
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err.Error()
	}
	if len(result.Content) == 0 {
		return result, ""
	}
	text, ok := result.Content[0].(*sdk.TextContent)
	if !ok {
		return result, "unexpected content"
	}
	return result, text.Text
}

func TestTaskRuntimeQualification_OriginRevocationAfterProviderPreventsAction(t *testing.T) {
	o, driver, _ := runtimeFixture(t, runtimePlan)
	calls := qualifiedProvider(t, o, func(w http.ResponseWriter, _ *http.Request) {
		writeCompletion(w, runtimePlan, true)
		if err := os.WriteFile(o.cfg.AllowlistPath(), []byte(`[]`), 0600); err != nil {
			t.Errorf("revoke allowlist: %v", err)
		}
	})
	session, ctx := qualificationSession(t, o)
	result, text := qualificationCall(t, session, ctx, "run_task", qualificationArgs(t, runtimeRequest("revoked_after_plan")))
	if result.IsError {
		t.Fatalf("MCP call: %s", text)
	}
	out := readMCPResult(t, text)
	if out.Status != TaskBlocked || calls.Load() != 1 || driver.calls["extract_field"] != 0 {
		t.Fatalf("post-revocation execution: status=%s provider=%d driver=%v", out.Status, calls.Load(), driver.calls)
	}
}

func TestTaskRuntimeQualification_FreshMCPClientRecoversAfterStoreRestart(t *testing.T) {
	o, _, _ := runtimeFixture(t, runtimePlan)
	calls := qualifiedProvider(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletion(w, runtimePlan, true) })
	firstSession, ctx := qualificationSession(t, o)
	result, text := qualificationCall(t, firstSession, ctx, "run_task", qualificationArgs(t, runtimeRequest("restart_recovery")))
	if result.IsError {
		t.Fatalf("run task: %s", text)
	}
	original := readMCPResult(t, text)
	if err := o.receipts.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenReceiptStore(filepath.Join(o.cfg.Home, "tasks-v2"), 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	o.receipts = store
	freshSession, freshCtx := qualificationSession(t, o)
	receiptCall, receiptText := qualificationCall(t, freshSession, freshCtx, "get_task_receipt", map[string]any{"task_id": "restart_recovery"})
	if receiptCall.IsError || !strings.Contains(receiptText, original.ExecutionID) || calls.Load() != 1 {
		t.Fatalf("fresh client recovery failed: isError=%v calls=%d text=%s", receiptCall.IsError, calls.Load(), receiptText)
	}
	var recovered TaskResult
	if err := json.Unmarshal([]byte(receiptText), &recovered); err != nil || recovered.ExecutionID != original.ExecutionID {
		t.Fatalf("receipt did not return terminal result: %v %s", err, receiptText)
	}
}
