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

	"github.com/dndungu/ferro/internal/core"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func qualificationSessionV2(t *testing.T, o *Owner) (*sdk.ClientSession, context.Context) {
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

func qualificationArgsV2(t *testing.T, value any) map[string]any {
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

func qualificationCallV2(t *testing.T, session *sdk.ClientSession, ctx context.Context, name string, args map[string]any) (*sdk.CallToolResult, string) {
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

func qualifiedProviderV2(t *testing.T, o *Owner, handler http.HandlerFunc) *atomic.Int64 {
	t.Helper()
	count := new(atomic.Int64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); handler(w, r) }))
	t.Cleanup(server.Close)
	o.cfg.LLMBaseURL = server.URL
	o.cfg.LLMModel = "qualification-fixture"
	o.cfg.LLMAPIKey = ""
	return count
}
func writeCompletionV2(w http.ResponseWriter, plan string, usage bool) {
	w.Header().Set("Content-Type", "application/json")
	out := map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": plan}}}}
	if usage {
		out["usage"] = map[string]int{"prompt_tokens": 9, "completion_tokens": 11, "total_tokens": 20}
	}
	_ = json.NewEncoder(w).Encode(out)
}
func readMCPResultV2(t *testing.T, text string) TaskResultV2 {
	t.Helper()
	var result TaskResultV2
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		t.Fatalf("decode result: %v: %s", err, text)
	}
	if err := ValidateTaskResultV2(result); err != nil {
		t.Fatalf("invalid task result: %v: %s", err, text)
	}
	return result
}

func TestTaskRuntimeV2Qualification_InvalidSchemaAndDeniedOriginDoNoProviderWork(t *testing.T) {
	t.Run("invalid schema preflight", func(t *testing.T) {
		o, driver, _ := runtimeFixtureV2(t, runtimePlanV2)
		calls := qualifiedProviderV2(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletionV2(w, runtimePlanV2, true) })
		session, ctx := qualificationSessionV2(t, o)
		in := runtimeRequestV2("invalid_schema")
		in.OutputSchema = json.RawMessage(`{"$ref":"https://example.org/remote.json"}`)
		result, _ := qualificationCallV2(t, session, ctx, "run_task_v2", qualificationArgsV2(t, in))
		if !result.IsError || calls.Load() != 0 || driver.calls["extract_field"] != 0 {
			t.Fatalf("isError=%v provider=%d driver=%v", result.IsError, calls.Load(), driver.calls)
		}
	})
	t.Run("service origin denied", func(t *testing.T) {
		o, driver, _ := runtimeFixtureV2(t, runtimePlanV2)
		if err := os.WriteFile(o.cfg.AllowlistPath(), []byte(`[]`), 0600); err != nil {
			t.Fatal(err)
		}
		calls := qualifiedProviderV2(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletionV2(w, runtimePlanV2, true) })
		session, ctx := qualificationSessionV2(t, o)
		result, text := qualificationCallV2(t, session, ctx, "run_task_v2", qualificationArgsV2(t, runtimeRequestV2("service_denied")))
		if result.IsError {
			t.Fatalf("expected admitted blocked envelope, got %s", text)
		}
		out := readMCPResultV2(t, text)
		if out.Status != TaskBlockedV2 || calls.Load() != 0 || driver.calls["extract_field"] != 0 {
			t.Fatalf("status=%s provider=%d driver=%v", out.Status, calls.Load(), driver.calls)
		}
	})
}

func TestTaskRuntimeV2Qualification_MalformedPlanStopsAtExactRequestCeiling(t *testing.T) {
	o, _, _ := runtimeFixtureV2(t, runtimePlanV2)
	calls := qualifiedProviderV2(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletionV2(w, "not a plan", true) })
	one := int64(1)
	in := runtimeRequestV2("malformed_plan")
	in.Limits.ModelRequests = &one
	in.Limits.PlanningPasses = &one
	session, ctx := qualificationSessionV2(t, o)
	result, text := qualificationCallV2(t, session, ctx, "run_task_v2", qualificationArgsV2(t, in))
	if result.IsError {
		t.Fatalf("run task transport error: %s", text)
	}
	out := readMCPResultV2(t, text)
	if out.Status != TaskBudgetExhaustedV2 || out.Budget.Requests != 1 || calls.Load() != 1 {
		t.Fatalf("status=%s requests=%d provider=%d result=%s", out.Status, out.Budget.Requests, calls.Load(), text)
	}
}

func TestTaskRuntimeV2Qualification_MissingProviderUsageRemainsUnknown(t *testing.T) {
	o, _, _ := runtimeFixtureV2(t, runtimePlanV2)
	calls := qualifiedProviderV2(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletionV2(w, runtimePlanV2, false) })
	session, ctx := qualificationSessionV2(t, o)
	result, text := qualificationCallV2(t, session, ctx, "run_task_v2", qualificationArgsV2(t, runtimeRequestV2("usage_unknown")))
	if result.IsError {
		t.Fatalf("MCP returned error: %s", text)
	}
	out := readMCPResultV2(t, text)
	if out.Status != TaskSucceededV2 || calls.Load() != 1 || out.Usage.InputTokens != nil || out.Usage.OutputTokens != nil || out.Usage.TotalTokens != nil {
		t.Fatalf("usage fabricated or task failed: %+v calls=%d", out.Usage, calls.Load())
	}
	if out.Budget.UncertainRequests != 1 {
		t.Fatalf("unknown usage not reserved: %+v", out.Budget)
	}
}

func TestTaskRuntimeV2Qualification_LostProviderResponseRetainsUnknownReceipt(t *testing.T) {
	o, _, _ := runtimeFixtureV2(t, runtimePlanV2)
	calls := qualifiedProviderV2(t, o, func(w http.ResponseWriter, _ *http.Request) {
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
	session, ctx := qualificationSessionV2(t, o)
	result, text := qualificationCallV2(t, session, ctx, "run_task_v2", qualificationArgsV2(t, runtimeRequestV2("provider_response_lost")))
	if result.IsError {
		t.Fatalf("expected durable result envelope after provider loss: %s", text)
	}
	out := readMCPResultV2(t, text)
	if out.Status == TaskSucceededV2 || len(out.Result) != 0 || out.ResultArtifactID != "" || out.Budget.UncertainRequests != 1 || out.Usage.TotalTokens != nil || calls.Load() != 1 {
		t.Fatalf("lost provider response fabricated success or lost uncertainty: %+v provider=%d", out, calls.Load())
	}
}

type disconnectedExtractDriverV2 struct {
	core.PageDriver
	calls atomic.Int64
}

func (d *disconnectedExtractDriverV2) ExtractField(context.Context, string) (string, error) {
	d.calls.Add(1)
	return "", &core.StopError{Code: "disconnected", Message: "fixture transport disconnected"}
}

func TestTaskRuntimeV2Qualification_BrowserDisconnectReturnsUncertainReceipt(t *testing.T) {
	o, driver, _ := runtimeFixtureV2(t, runtimePlanV2)
	disconnected := &disconnectedExtractDriverV2{PageDriver: driver}
	o.driver = disconnected
	calls := qualifiedProviderV2(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletionV2(w, runtimePlanV2, true) })
	session, ctx := qualificationSessionV2(t, o)
	_, text := qualificationCallV2(t, session, ctx, "run_task_v2", qualificationArgsV2(t, runtimeRequestV2("browser_disconnect")))
	receipt := readMCPResultV2(t, text)
	if receipt.Status != TaskOutcomeUncertainV2 || receipt.SideEffectState != SideEffectUnknownV2 || receipt.Error == nil || receipt.Error.Retry != "reconcile_only" || receipt.Result != nil || receipt.ResultArtifactID != "" || calls.Load() != 1 || disconnected.calls.Load() != 1 {
		t.Fatalf("disconnect was not retained as uncertain: %+v provider=%d extraction=%d", receipt, calls.Load(), disconnected.calls.Load())
	}
}

func TestTaskRuntimeV2Qualification_LargeExtractedResultReadBackByMCP(t *testing.T) {
	o, driver, _ := runtimeFixtureV2(t, runtimePlanV2)
	driver.value = strings.Repeat("x", 20000)
	calls := qualifiedProviderV2(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletionV2(w, runtimePlanV2, true) })
	in := runtimeRequestV2("large_artifact")
	in.Evidence = EvidenceArtifactsV2
	in.OutputSchema = json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`)
	session, ctx := qualificationSessionV2(t, o)
	result, text := qualificationCallV2(t, session, ctx, "run_task_v2", qualificationArgsV2(t, in))
	if result.IsError {
		t.Fatalf("run task error: %s", text)
	}
	out := readMCPResultV2(t, text)
	if out.Status != TaskSucceededV2 || len(out.Result) != 0 || out.ResultArtifactID == "" || calls.Load() != 1 {
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
		_, chunkText := qualificationCallV2(t, session, ctx, "read_task_artifact", args)
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

func TestTaskRuntimeV2Qualification_WarmReplayExtractsCurrentFactsThroughMCP(t *testing.T) {
	o, driver, _ := runtimeFixtureV2(t, runtimePlanV2)
	calls := qualifiedProviderV2(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletionV2(w, runtimePlanV2, true) })
	session, ctx := qualificationSessionV2(t, o)
	firstRequest := runtimeRequestV2("mcp_warm_first")
	firstRequest.ReplayLabel = "same-work"
	first, firstText := qualificationCallV2(t, session, ctx, "run_task_v2", qualificationArgsV2(t, firstRequest))
	if first.IsError {
		t.Fatalf("first MCP run: %s", firstText)
	}
	firstResult := readMCPResultV2(t, firstText)
	if firstResult.Status != TaskSucceededV2 || string(firstResult.Result) != `{"value":"first"}` {
		t.Fatalf("first result=%+v", firstResult)
	}
	driver.value = "current-after-replay"
	secondRequest := firstRequest
	secondRequest.TaskID = "mcp_warm_second"
	second, secondText := qualificationCallV2(t, session, ctx, "run_task_v2", qualificationArgsV2(t, secondRequest))
	if second.IsError {
		t.Fatalf("second MCP run: %s", secondText)
	}
	secondResult := readMCPResultV2(t, secondText)
	if secondResult.Status != TaskSucceededV2 || string(secondResult.Result) != `{"value":"current-after-replay"}` || secondResult.Budget.Requests != 0 || calls.Load() != 1 || driver.calls["extract_field"] != 2 {
		t.Fatalf("MCP warm replay stale/called model: result=%+v provider=%d driver=%v", secondResult, calls.Load(), driver.calls)
	}
}

type repairFailExtractDriverV2 struct{ *runtimeDriverV2 }

func (d *repairFailExtractDriverV2) ExtractField(context.Context, string) (string, error) {
	d.called("extract_field")
	return "", fmt.Errorf("fixture extraction failure")
}

func TestTaskRuntimeV2Qualification_InitialAndRepairedMutationPlansNeverDispatch(t *testing.T) {
	initialMutation := `{"steps":[{"kind":"key","text":"Enter"},{"kind":"done","result":{"value":"x"}}]}`
	t.Run("initial plan", func(t *testing.T) {
		o, driver, _ := runtimeFixtureV2(t, initialMutation)
		calls := qualifiedProviderV2(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletionV2(w, initialMutation, true) })
		session, ctx := qualificationSessionV2(t, o)
		result, text := qualificationCallV2(t, session, ctx, "run_task_v2", qualificationArgsV2(t, runtimeRequestV2("initial_mutation")))
		if result.IsError {
			t.Fatalf("MCP: %s", text)
		}
		out := readMCPResultV2(t, text)
		if out.Status != TaskBlockedV2 || calls.Load() != 1 || driver.calls["key"] != 0 {
			t.Fatalf("initial mutation dispatched: %+v provider=%d driver=%v", out, calls.Load(), driver.calls)
		}
	})
	t.Run("repair proposal", func(t *testing.T) {
		firstPlan := `{"steps":[{"kind":"extract","fields":{"value":"#value"}},{"kind":"done","result":"{{extract.last}}"}]}`
		o, base, _ := runtimeFixtureV2(t, firstPlan)
		driver := &repairFailExtractDriverV2{runtimeDriverV2: base}
		o.driver = driver
		var requests atomic.Int64
		calls := qualifiedProviderV2(t, o, func(w http.ResponseWriter, _ *http.Request) {
			if requests.Add(1) == 1 {
				writeCompletionV2(w, firstPlan, true)
			} else {
				writeCompletionV2(w, `{"kind":"key","text":"Enter"}`, true)
			}
		})
		session, ctx := qualificationSessionV2(t, o)
		result, text := qualificationCallV2(t, session, ctx, "run_task_v2", qualificationArgsV2(t, runtimeRequestV2("repair_mutation")))
		if result.IsError {
			t.Fatalf("MCP: %s", text)
		}
		out := readMCPResultV2(t, text)
		if out.Status != TaskBlockedV2 || calls.Load() != 2 || driver.calls["key"] != 0 {
			t.Fatalf("repair mutation dispatched: %+v provider=%d driver=%v", out, calls.Load(), driver.calls)
		}
	})
	t.Run("blocked replay candidate is never cached", func(t *testing.T) {
		mutation := `{"steps":[{"kind":"key","text":"Enter"},{"kind":"done","result":{"value":"x"}}]}`
		valid := runtimePlanV2
		o, driver, _ := runtimeFixtureV2(t, mutation)
		var requests atomic.Int64
		calls := qualifiedProviderV2(t, o, func(w http.ResponseWriter, _ *http.Request) {
			if requests.Add(1) == 1 {
				writeCompletionV2(w, mutation, true)
			} else {
				writeCompletionV2(w, valid, true)
			}
		})
		session, ctx := qualificationSessionV2(t, o)
		first := runtimeRequestV2("replay_mutation_first")
		first.ReplayLabel = "same-replay"
		one, text := qualificationCallV2(t, session, ctx, "run_task_v2", qualificationArgsV2(t, first))
		if one.IsError {
			t.Fatalf("first MCP: %s", text)
		}
		first.TaskID = "replay_mutation_second"
		two, text := qualificationCallV2(t, session, ctx, "run_task_v2", qualificationArgsV2(t, first))
		if two.IsError {
			t.Fatalf("second MCP: %s", text)
		}
		out := readMCPResultV2(t, text)
		if out.Status != TaskSucceededV2 || calls.Load() != 2 || driver.calls["key"] != 0 {
			t.Fatalf("unsafe replay or mutation call: %+v provider=%d driver=%v", out, calls.Load(), driver.calls)
		}
	})
}

func TestTaskRuntimeV2QualificationRestartChild(t *testing.T) {
	if os.Getenv("FERRO_G03_RESTART_CHILD") != "1" {
		return
	}
	home := os.Getenv("FERRO_G03_RESTART_HOME")
	providerURL := os.Getenv("FERRO_G03_RESTART_PROVIDER")
	o, base, _ := runtimeFixtureV2(t, runtimePlanV2)
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
	store, err := OpenReceiptStoreV2(filepath.Join(home, "tasks-v2"), 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	o.cfg.Home, o.cfg.LLMBaseURL, o.cfg.LLMModel = home, providerURL, "qualification-fixture"
	o.allow, o.receipts = allow, store
	blocking := &restartBlockingDriverV2{runtimeDriverV2: base, entered: make(chan struct{})}
	o.driver = blocking
	session, ctx := qualificationSessionV2(t, o)
	args := qualificationArgsV2(t, runtimeRequestV2("restart_inflight"))
	go func() { _, _ = session.CallTool(ctx, &sdk.CallToolParams{Name: "run_task_v2", Arguments: args}) }()
	select {
	case <-blocking.entered:
		fmt.Fprintln(os.Stdout, "READY")
	case <-ctx.Done():
		t.Fatal("task did not reach blocked browser operation")
	}
	select {} // The parent kills this process to simulate an unclean crash.
}

type restartBlockingDriverV2 struct {
	*runtimeDriverV2
	entered chan struct{}
	once    sync.Once
}

func (d *restartBlockingDriverV2) ExtractField(context.Context, string) (string, error) {
	d.called("extract_field")
	d.once.Do(func() { close(d.entered) })
	select {} // The parent kills the subprocess while execution is in flight.
}

func TestTaskRuntimeV2Qualification_RestartReconcilesInflightReceiptWithoutRedispatch(t *testing.T) {
	home := t.TempDir()
	var providerCalls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		writeCompletionV2(w, runtimePlanV2, true)
	}))
	defer provider.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestTaskRuntimeV2QualificationRestartChild$")
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
	store, err := OpenReceiptStoreV2(filepath.Join(home, "tasks-v2"), 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	o := &Owner{cfg: Config{Home: home, BlockTimeout: time.Minute}, receipts: store, gate: make(chan struct{}, 1), stopCh: make(chan struct{})}
	session, ctx := qualificationSessionV2(t, o)
	_, receiptText := qualificationCallV2(t, session, ctx, "get_task_receipt", map[string]any{"task_id": "restart_inflight"})
	var receipt ReceiptV2
	if err = json.Unmarshal([]byte(receiptText), &receipt); err != nil {
		t.Fatalf("decode recovered receipt: %v: %s", err, receiptText)
	}
	if receipt.State != ReceiptUncertainV2 || receipt.TaskID != "restart_inflight" {
		t.Fatalf("in-flight receipt not reconciled: %+v", receipt)
	}
	// A duplicate after restart returns that uncertain receipt; it must not dispatch.
	_, duplicateText := qualificationCallV2(t, session, ctx, "run_task_v2", qualificationArgsV2(t, runtimeRequestV2("restart_inflight")))
	var duplicate ReceiptV2
	if err = json.Unmarshal([]byte(duplicateText), &duplicate); err != nil {
		t.Fatalf("decode duplicate response: %v: %s", err, duplicateText)
	}
	if duplicate.State != ReceiptUncertainV2 || providerCalls.Load() != 1 {
		t.Fatalf("restart redispatched: state=%s provider calls=%d", duplicate.State, providerCalls.Load())
	}
}

func TestTaskRuntimeV2Qualification_ConcurrentDuplicateAndChangedInput(t *testing.T) {
	o, _, _ := runtimeFixtureV2(t, runtimePlanV2)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	calls := qualifiedProviderV2(t, o, func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		writeCompletionV2(w, runtimePlanV2, true)
	})
	session, ctx := qualificationSessionV2(t, o)
	in := runtimeRequestV2("concurrent_duplicate")
	args := qualificationArgsV2(t, in)
	first := make(chan struct {
		res  *sdk.CallToolResult
		text string
	}, 1)
	go func() {
		res, text := qualificationCallSafeV2(session, ctx, "run_task_v2", args)
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
	secondSession, secondCtx := qualificationSessionV2(t, o)
	duplicateStarted := make(chan struct{})
	duplicate := make(chan struct {
		res  *sdk.CallToolResult
		text string
	}, 1)
	go func() {
		close(duplicateStarted)
		res, text := qualificationCallSafeV2(secondSession, secondCtx, "run_task_v2", args)
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
		_ = readMCPResultV2(t, got.text)
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
	conflict, conflictText := qualificationCallV2(t, secondSession, secondCtx, "run_task_v2", qualificationArgsV2(t, changed))
	if !conflict.IsError || calls.Load() != 1 {
		t.Fatalf("changed input did not conflict: isError=%v calls=%d %s", conflict.IsError, calls.Load(), conflictText)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider attempts=%d", calls.Load())
	}
}

func qualificationCallSafeV2(session *sdk.ClientSession, ctx context.Context, name string, args map[string]any) (*sdk.CallToolResult, string) {
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

func TestTaskRuntimeV2Qualification_OriginRevocationAfterProviderPreventsAction(t *testing.T) {
	o, driver, _ := runtimeFixtureV2(t, runtimePlanV2)
	calls := qualifiedProviderV2(t, o, func(w http.ResponseWriter, _ *http.Request) {
		writeCompletionV2(w, runtimePlanV2, true)
		if err := os.WriteFile(o.cfg.AllowlistPath(), []byte(`[]`), 0600); err != nil {
			t.Errorf("revoke allowlist: %v", err)
		}
	})
	session, ctx := qualificationSessionV2(t, o)
	result, text := qualificationCallV2(t, session, ctx, "run_task_v2", qualificationArgsV2(t, runtimeRequestV2("revoked_after_plan")))
	if result.IsError {
		t.Fatalf("MCP call: %s", text)
	}
	out := readMCPResultV2(t, text)
	if out.Status != TaskBlockedV2 || calls.Load() != 1 || driver.calls["extract_field"] != 0 {
		t.Fatalf("post-revocation execution: status=%s provider=%d driver=%v", out.Status, calls.Load(), driver.calls)
	}
}

func TestTaskRuntimeV2Qualification_FreshMCPClientRecoversAfterStoreRestart(t *testing.T) {
	o, _, _ := runtimeFixtureV2(t, runtimePlanV2)
	calls := qualifiedProviderV2(t, o, func(w http.ResponseWriter, _ *http.Request) { writeCompletionV2(w, runtimePlanV2, true) })
	firstSession, ctx := qualificationSessionV2(t, o)
	result, text := qualificationCallV2(t, firstSession, ctx, "run_task_v2", qualificationArgsV2(t, runtimeRequestV2("restart_recovery")))
	if result.IsError {
		t.Fatalf("run task: %s", text)
	}
	original := readMCPResultV2(t, text)
	if err := o.receipts.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenReceiptStoreV2(filepath.Join(o.cfg.Home, "tasks-v2"), 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	o.receipts = store
	freshSession, freshCtx := qualificationSessionV2(t, o)
	receiptCall, receiptText := qualificationCallV2(t, freshSession, freshCtx, "get_task_receipt", map[string]any{"task_id": "restart_recovery"})
	if receiptCall.IsError || !strings.Contains(receiptText, original.ExecutionID) || calls.Load() != 1 {
		t.Fatalf("fresh client recovery failed: isError=%v calls=%d text=%s", receiptCall.IsError, calls.Load(), receiptText)
	}
	var recovered TaskResultV2
	if err := json.Unmarshal([]byte(receiptText), &recovered); err != nil || recovered.ExecutionID != original.ExecutionID {
		t.Fatalf("receipt did not return terminal result: %v %s", err, receiptText)
	}
}
