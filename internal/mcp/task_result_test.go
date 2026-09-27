package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sirerun/ferro/internal/core"
)

type resultTestSink struct {
	data []byte
	err  error
}

func (s *resultTestSink) PutArtifact(_ context.Context, _ string, _ string, data []byte, mediaType string) (Artifact, error) {
	if s.err != nil {
		return Artifact{}, s.err
	}
	s.data = append([]byte(nil), data...)
	sum := sha256.Sum256(data)
	return Artifact{ID: "artifact_result", SHA256: hex.EncodeToString(sum[:]), MediaType: mediaType, Size: int64(len(data))}, nil
}

func resultRecord() ExecutionRecord {
	return ExecutionRecord{
		Owner: "principal", ExecutionID: "exec_result", TaskID: "task_result",
		Profile: "profile", ProfileRevision: "revision", Model: "model",
		Limits: core.DefaultLimits(), StartedAt: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC),
		EndedAt: time.Date(2026, 9, 24, 12, 0, 1, 0, time.UTC),
		Status:  TaskSucceeded, Validation: "valid", SideEffectState: SideEffectNone,
		Result: json.RawMessage(`{"value":"ok"}`),
	}
}

func TestResult_FailureKeepsUsage(t *testing.T) {
	record := resultRecord()
	record.Status = TaskFailed
	record.Validation = "not_run"
	record.Result = nil
	record.PartialResult = json.RawMessage(`{"partial":true}`)
	record.Usage = core.RequestUsage{InputTokens: int64PtrResult(31), TotalTokens: int64PtrResult(31)}
	record.Budget = core.BudgetSnapshot{Requests: 1, UncertainRequests: 1, ReportedUsage: core.RequestUsage{InputTokens: int64PtrResult(31)}}
	record.Error = &TaskError{Category: "provider_error", Stage: "planning", Retry: "reconcile_only", Detail: "credential=must-not-leak"}
	got, err := BuildTaskResult(context.Background(), record, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != TaskFailed || got.Usage.InputTokens == nil || *got.Usage.InputTokens != 31 || got.Budget.UncertainRequests != 1 || got.PartialResult == nil {
		t.Fatalf("failure lost independent metrics or partial result: %+v", got)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(string(encoded), "must-not-leak") {
		t.Fatalf("unsafe error detail was serialized: %s", encoded)
	}
}

func TestResult_ValidationBlocksSuccess(t *testing.T) {
	record := resultRecord()
	record.Validation = "invalid"
	got, err := BuildTaskResult(context.Background(), record, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != TaskFailed || got.Result != nil || got.ResultArtifactID != "" || got.Error == nil || got.Error.Category != "output_invalid" || got.Validation != "invalid" {
		t.Fatalf("invalid output was promoted: %+v", got)
	}
}

func TestResult_UnknownCost(t *testing.T) {
	record := resultRecord()
	record.Usage.InputTokens = int64PtrResult(0)
	record.Usage.BilledMicroUSD = nil
	record.Budget.ReportedUsage.InputTokens = int64PtrResult(0)
	record.Budget.ReportedUsage.BilledMicroUSD = nil
	got, err := BuildTaskResult(context.Background(), record, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Usage.InputTokens == nil || *got.Usage.InputTokens != 0 || got.Usage.BilledMicroUSD != nil || got.Budget.ReportedUsage.BilledMicroUSD != nil {
		t.Fatalf("unknown cost was converted to a known value: usage=%+v budget=%+v", got.Usage, got.Budget)
	}
}

func TestResult_OversizedArtifact(t *testing.T) {
	record := resultRecord()
	record.Result = json.RawMessage(`{"value":"` + strings.Repeat("x", taskResultInlineBytes+100) + `"}`)
	sink := &resultTestSink{}
	got, err := BuildTaskResult(context.Background(), record, sink)
	if err != nil {
		t.Fatal(err)
	}
	if got.Result != nil || got.ResultArtifactID != "artifact_result" || len(got.Artifacts) != 1 || int64(len(sink.data)) != got.Artifacts[0].Size || !json.Valid(sink.data) || string(sink.data) != string(record.Result) {
		t.Fatalf("oversized success was not fully artifact-backed: result=%s artifacts=%+v size=%d", got.Result, got.Artifacts, len(sink.data))
	}
	fault := errors.New("storage full")
	sink = &resultTestSink{err: fault}
	if _, err := BuildTaskResult(context.Background(), record, sink); !errors.Is(err, fault) {
		t.Fatalf("artifact sink failure was swallowed: %v", err)
	}
	spaced := resultRecord()
	spaced.Result = json.RawMessage(strings.Repeat(" ", taskResultInlineBytes+1) + `{"value":"ok"}`)
	spacedSink := &resultTestSink{}
	spacedResult, err := BuildTaskResult(context.Background(), spaced, spacedSink)
	if err != nil {
		t.Fatal(err)
	}
	if spacedResult.Result != nil || spacedResult.ResultArtifactID == "" || string(spacedSink.data) != string(spaced.Result) {
		t.Fatal("oversized source output was compacted into the inline result")
	}
}

func TestResult_UTF8Summary(t *testing.T) {
	record := resultRecord()
	record.Summary = strings.Repeat("é", 1100)
	got, err := BuildTaskResult(context.Background(), record, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Summary) > taskResultSummaryBytes || !utf8.ValidString(got.Summary) || !strings.HasPrefix(got.Summary, "é") {
		t.Fatalf("summary truncation broke UTF-8 or byte bound: bytes=%d valid=%v", len(got.Summary), utf8.ValidString(got.Summary))
	}
}

func TestResult_RetryMapping(t *testing.T) {
	for _, tc := range []struct {
		name string
		side SideEffectState
		want string
	}{
		{name: "known not executed", side: SideEffectNone, want: "known_not_executed"},
		{name: "unknown effects require reconciliation", side: SideEffectUnknown, want: "reconcile_only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := resultRecord()
			record.Status = TaskFailed
			record.Validation = "not_run"
			record.Result = nil
			record.SideEffectState = tc.side
			record.Error = &TaskError{Category: "provider_error", Stage: "planning", Retry: "known_not_executed"}
			got, err := BuildTaskResult(context.Background(), record, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got.Error == nil || got.Error.Retry != tc.want {
				t.Fatalf("retry=%v want %q", got.Error, tc.want)
			}
		})
	}
}

func TestResult_HTMLExpansionRemainsPersistable(t *testing.T) {
	record := resultRecord()
	record.Result = json.RawMessage(`{"html":"` + strings.Repeat("<", 3000) + `"}`)
	sink := &resultTestSink{}
	got, err := BuildTaskResult(context.Background(), record, sink)
	if err != nil {
		t.Fatal(err)
	}
	if got.Result != nil || got.ResultArtifactID == "" || len(sink.data) <= taskResultInlineBytes {
		t.Fatalf("HTML expansion was returned inline: result=%d artifact=%q stored=%d", len(got.Result), got.ResultArtifactID, len(sink.data))
	}
	serialized, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var reopened TaskResult
	if err := json.Unmarshal(serialized, &reopened); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTaskResult(reopened); err != nil {
		t.Fatalf("serialized result is not reopenable: %v", err)
	}
	if string(record.Result) != `{"html":"`+strings.Repeat("<", 3000)+`"}` {
		t.Fatal("builder modified caller-owned result bytes")
	}
	var original, artifact map[string]string
	if err := json.Unmarshal(record.Result, &original); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(sink.data, &artifact); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(artifact, original) {
		t.Fatalf("escaped artifact changed result value: got %d characters", len(artifact["html"]))
	}
}

func TestResult_HTMLExpansionReceiptReopens(t *testing.T) {
	dir := t.TempDir()
	store, request, digest := receiptFixture(t, dir)
	receipt := admitFixture(t, store, request, digest)
	record := resultRecord()
	record.Owner = "principal"
	record.TaskID = receipt.TaskID
	record.ExecutionID = receipt.ExecutionID
	record.Result = json.RawMessage(`{"html":"` + strings.Repeat("<", 3000) + `"}`)
	result, err := BuildTaskResult(context.Background(), record, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Finalize(context.Background(), "principal", receipt.ExecutionID, result); err != nil {
		t.Fatalf("finalize HTML-expanded result: %v", err)
	}
	artifactID := result.ResultArtifactID
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenReceiptStore(dir, 32<<20)
	if err != nil {
		t.Fatalf("reopen finalized HTML-expanded receipt: %v", err)
	}
	defer reopened.Close()
	loaded, err := reopened.Get(context.Background(), "principal", receipt.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateTaskResult(*loaded.Result); err != nil {
		t.Fatalf("reopened result failed envelope validation: %v", err)
	}
	artifact, err := reopened.ReadArtifact(context.Background(), "principal", receipt.ExecutionID, artifactID, 0, loaded.Result.Artifacts[0].Size)
	if err != nil {
		t.Fatal(err)
	}
	var got, want map[string]string
	if err := json.Unmarshal(artifact, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(record.Result, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("reopened artifact changed HTML output value")
	}
}

func int64PtrResult(value int64) *int64 { return &value }
