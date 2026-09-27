package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirerun/ferro/internal/core"
)

func receiptFixture(t *testing.T, dir string) (ReceiptStore, RunTaskRequest, string) {
	t.Helper()
	s, err := OpenReceiptStore(dir, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := RunTaskRequest{Schema: "ferro.task/v2", TaskID: "task_one", Goal: "inspect", ModelProfile: "profile", Policy: &TaskPolicy{Mode: "read_only", Origins: []string{"https://example.com"}}, OutputSchema: []byte(`{"type":"object"}`)}
	digest, err := canonicalRequestDigest(r)
	if err != nil {
		t.Fatal(err)
	}
	return s, r, digest
}
func admitFixture(t *testing.T, s ReceiptStore, r RunTaskRequest, digest string) Receipt {
	t.Helper()
	got, created, err := s.Admit(context.Background(), "principal", r, digest)
	if err != nil || !created {
		t.Fatalf("Admit() = (%+v,%v,%v)", got, created, err)
	}
	return got
}

func TestReceipts_OwnerDenied(t *testing.T) {
	s, r, d := receiptFixture(t, t.TempDir())
	got := admitFixture(t, s, r, d)
	if _, err := s.Get(context.Background(), "someone-else", got.ExecutionID); !errors.Is(err, ErrReceiptNotFound) && !errors.Is(err, ErrReceiptOwnerDenied) {
		t.Fatalf("cross-owner lookup disclosed receipt: %v", err)
	}
	if _, err := s.Lookup(context.Background(), "someone-else", r.TaskID); !errors.Is(err, ErrReceiptNotFound) {
		t.Fatalf("cross-owner caller-key lookup = %v", err)
	}
}

func TestReceipts_ExclusiveLockAndClose(t *testing.T) {
	dir := t.TempDir()
	s, _, _ := receiptFixture(t, dir)
	if _, err := OpenReceiptStore(dir, 32<<20); err == nil {
		t.Fatal("second process-equivalent opener acquired the exclusive lock")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(context.Background(), "principal", "task_one"); err == nil {
		t.Fatal("lookup succeeded after Close")
	}
	reopened, err := OpenReceiptStore(dir, 32<<20)
	if err != nil {
		t.Fatalf("lock was not released by Close: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReceipts_PathTraversal(t *testing.T) {
	s, r, d := receiptFixture(t, t.TempDir())
	if _, _, err := s.Admit(context.Background(), "principal", r, "../"+d); err == nil {
		t.Fatal("accepted malformed digest")
	}
	got := admitFixture(t, s, r, d)
	if _, err := s.ReadArtifact(context.Background(), "principal", got.ExecutionID, "../../receipts-v2.json", 0, 12); err == nil {
		t.Fatal("accepted traversal artifact ID")
	}
}

func TestReceipts_ReopenUncertain(t *testing.T) {
	dir := t.TempDir()
	s, r, d := receiptFixture(t, dir)
	first := admitFixture(t, s, r, d)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenReceiptStore(dir, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	got, err := s2.Lookup(context.Background(), "principal", r.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ReceiptUncertain || got.ExecutionID != first.ExecutionID {
		t.Fatalf("recovered receipt = %+v", got)
	}
}

func TestReceipts_RequestConflict(t *testing.T) {
	s, r, d := receiptFixture(t, t.TempDir())
	admitFixture(t, s, r, d)
	changed := r
	changed.Goal = "different"
	changedDigest, err := canonicalRequestDigest(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := s.Admit(context.Background(), "principal", changed, changedDigest); !errors.Is(err, ErrReceiptConflict) || created {
		t.Fatalf("changed request = created %v, err %v", created, err)
	}
	if _, created, err := s.Admit(context.Background(), "principal", r, d); err != nil || created {
		t.Fatalf("duplicate admission = created %v, err %v", created, err)
	}
}

func TestReceipts_BoundedRead(t *testing.T) {
	s, r, d := receiptFixture(t, t.TempDir())
	rec := admitFixture(t, s, r, d)
	data := bytes.Repeat([]byte("x"), int(receiptReadChunk+20))
	a, err := s.PutArtifact(context.Background(), "principal", rec.ExecutionID, data, "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := s.ReadArtifact(context.Background(), "principal", rec.ExecutionID, a.ID, 10, receiptReadChunk)
	if err != nil || len(chunk) != int(receiptReadChunk) {
		t.Fatalf("bounded read len=%d err=%v", len(chunk), err)
	}
	if _, err = s.ReadArtifact(context.Background(), "principal", rec.ExecutionID, a.ID, 0, receiptReadChunk+1); err == nil {
		t.Fatal("oversized chunk accepted")
	}
	chunk[0] = 'z'
	again, err := s.ReadArtifact(context.Background(), "principal", rec.ExecutionID, a.ID, 10, 1)
	if err != nil || again[0] != 'x' {
		t.Fatalf("read did not return a copy: %q %v", again, err)
	}
}

func TestReceipts_WriteFailure(t *testing.T) {
	s, r, d := receiptFixture(t, t.TempDir())
	internal := s.(*receiptStore)
	internal.writeFile = func(string, []byte) error { return errors.New("injected fsync/rename failure") }
	if _, created, err := s.Admit(context.Background(), "principal", r, d); err == nil || created {
		t.Fatalf("failed durable write = created %v err %v", created, err)
	}
	if _, err := s.Lookup(context.Background(), "principal", r.TaskID); err == nil {
		t.Fatal("store continued serving after an uncertain durable write")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenReceiptStore(internal.dir, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err := reopened.Lookup(context.Background(), "principal", r.TaskID); !errors.Is(err, ErrReceiptNotFound) {
		t.Fatalf("failed admission leaked into durable state: %v", err)
	}
}

func TestReceipts_CallerKeyRecovery(t *testing.T) {
	dir := t.TempDir()
	s, r, d := receiptFixture(t, dir)
	first := admitFixture(t, s, r, d)
	_ = s.Close()
	s2, err := OpenReceiptStore(dir, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	got, err := s2.Lookup(context.Background(), "principal", r.TaskID)
	if err != nil || got.ExecutionID != first.ExecutionID || got.State != ReceiptUncertain {
		t.Fatalf("caller recovery = %+v, %v", got, err)
	}
}

func TestReceipts_ConcurrentDuplicate(t *testing.T) {
	s, r, d := receiptFixture(t, t.TempDir())
	const n = 12
	var wg sync.WaitGroup
	wg.Add(n)
	ids := make(chan string, n)
	created := make(chan bool, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			got, c, err := s.Admit(context.Background(), "principal", r, d)
			if err != nil {
				errs <- err
				return
			}
			ids <- got.ExecutionID
			created <- c
		}()
	}
	wg.Wait()
	close(ids)
	close(created)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var id string
	newCount := 0
	for value := range ids {
		if id != "" && value != id {
			t.Fatalf("duplicate executions %q and %q", id, value)
		}
		id = value
	}
	for c := range created {
		if c {
			newCount++
		}
	}
	if newCount != 1 {
		t.Fatalf("new admissions=%d, want 1", newCount)
	}
}

func TestReceipts_RecoveryRetention(t *testing.T) {
	s, r, d := receiptFixture(t, t.TempDir())
	got := admitFixture(t, s, r, d)
	now := time.Now().UTC()
	billed := int64(0)
	result := TaskResult{Schema: "ferro.result/v2", TaskID: r.TaskID, ExecutionID: got.ExecutionID, Status: TaskFailed, StartedAt: now.Add(-time.Minute), EndedAt: now, ModelProfile: r.ModelProfile, ProfileRevision: "revision", EffectiveLimits: core.DefaultLimits(), Validation: "not_run", Usage: core.RequestUsage{BilledMicroUSD: &billed}, SideEffectState: SideEffectNone}
	if err := s.Finalize(context.Background(), "principal", got.ExecutionID, result); err != nil {
		t.Fatalf("Finalize(): %v", err)
	}
	internal := s.(*receiptStore)
	key := receiptKey("principal", r.TaskID)
	entry := internal.disk.Receipts[key]
	entry.Receipt.CreatedAt = now.Add(-31 * 24 * time.Hour)
	internal.disk.Receipts[key] = entry
	if err := internal.persist(internal.disk); err != nil {
		t.Fatal(err)
	}
	if err := s.Cleanup(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(context.Background(), "principal", r.TaskID); !errors.Is(err, ErrReceiptExpired) {
		t.Fatalf("expired caller key did not resolve to tombstone: %v", err)
	}
	if _, _, err := s.Admit(context.Background(), "principal", r, d); !errors.Is(err, ErrReceiptExpired) {
		t.Fatalf("expired key was admitted again: %v", err)
	}
	// An active entry is never eligible for cleanup, regardless of its age.
	r2 := r
	r2.TaskID = "active_task"
	d2, err := canonicalRequestDigest(r2)
	if err != nil {
		t.Fatal(err)
	}
	active := admitFixture(t, s, r2, d2)
	activeEntry := internal.disk.Receipts[receiptKey("principal", r2.TaskID)]
	activeEntry.Receipt.CreatedAt = now.Add(-365 * 24 * time.Hour)
	internal.disk.Receipts[receiptKey("principal", r2.TaskID)] = activeEntry
	if err := internal.persist(internal.disk); err != nil {
		t.Fatal(err)
	}
	if err := s.Cleanup(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if recovered, err := s.Lookup(context.Background(), "principal", r2.TaskID); err != nil || recovered.ExecutionID != active.ExecutionID {
		t.Fatalf("active receipt removed: %+v %v", recovered, err)
	}
	if _, err := os.Stat(filepath.Join(internal.dir, receiptStoreFile)); err != nil {
		t.Fatal(err)
	}
	// Missing spend information keeps a terminal receipt unreconciled.
	r3 := r
	r3.TaskID = "unknown_spend"
	d3, err := canonicalRequestDigest(r3)
	if err != nil {
		t.Fatal(err)
	}
	unknown := admitFixture(t, s, r3, d3)
	unknownResult := result
	unknownResult.TaskID = r3.TaskID
	unknownResult.ExecutionID = unknown.ExecutionID
	unknownResult.Usage = core.RequestUsage{}
	if err := s.Finalize(context.Background(), "principal", unknown.ExecutionID, unknownResult); err != nil {
		t.Fatal(err)
	}
	unknownEntry := internal.disk.Receipts[receiptKey("principal", r3.TaskID)]
	unknownEntry.Receipt.CreatedAt = now.Add(-365 * 24 * time.Hour)
	internal.disk.Receipts[receiptKey("principal", r3.TaskID)] = unknownEntry
	if err := internal.persist(internal.disk); err != nil {
		t.Fatal(err)
	}
	if err := s.Cleanup(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(context.Background(), "principal", r3.TaskID); err != nil {
		t.Fatalf("unknown spend record removed: %v", err)
	}
}

func TestReceipts_UnknownSpendCannotBeCleanedAfterFinalize(t *testing.T) {
	dir := t.TempDir()
	s, r, d := receiptFixture(t, dir)
	rec := admitFixture(t, s, r, d)
	now := time.Now().UTC()
	knownCost := int64(0)
	result := TaskResult{
		Schema: "ferro.result/v2", TaskID: r.TaskID, ExecutionID: rec.ExecutionID,
		Status: TaskFailed, StartedAt: now.Add(-time.Minute), EndedAt: now,
		ModelProfile: r.ModelProfile, ProfileRevision: "revision",
		EffectiveLimits: core.DefaultLimits(), Validation: "not_run",
		Usage: core.RequestUsage{BilledMicroUSD: &knownCost},
		Budget: core.BudgetSnapshot{
			Requests: 2, UncertainRequests: 1,
			ReportedUsage: core.RequestUsage{BilledMicroUSD: &knownCost}, Currency: "USD",
		},
		SideEffectState: SideEffectNone,
	}
	if err := s.Finalize(context.Background(), "principal", rec.ExecutionID, result); err != nil {
		t.Fatalf("Finalize(): %v", err)
	}
	internal := s.(*receiptStore)
	key := receiptKey("principal", r.TaskID)
	entry := internal.disk.Receipts[key]
	finalizeMarkedReconciled := entry.Reconciled
	// Simulate a previously persisted flag from the older, incomplete rule.
	entry.Reconciled = true
	entry.Receipt.CreatedAt = now.Add(-31 * 24 * time.Hour)
	internal.disk.Receipts[key] = entry
	if err := internal.persist(internal.disk); err != nil {
		t.Fatal(err)
	}
	if err := s.Cleanup(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(context.Background(), "principal", r.TaskID); err != nil {
		t.Fatalf("cleanup removed receipt with uncertain spend: %v", err)
	}
	if finalizeMarkedReconciled {
		t.Fatal("Finalize marked a receipt reconciled despite uncertain requests")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenReceiptStore(dir, 32<<20)
	if err != nil {
		t.Fatalf("reopen after uncertain-spend cleanup: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if got, err := reopened.Lookup(context.Background(), "principal", r.TaskID); err != nil || got.Result == nil || got.Result.Budget.UncertainRequests != 1 {
		t.Fatalf("uncertain-spend receipt was not preserved: %+v %v", got, err)
	}
}

func TestReceipts_NearLimitFinalizeRemainsReopenable(t *testing.T) {
	dir := t.TempDir()
	s, r, d := receiptFixture(t, dir)
	rec := admitFixture(t, s, r, d)
	now := time.Now().UTC()
	result := TaskResult{
		Schema: "ferro.result/v2", TaskID: r.TaskID, ExecutionID: rec.ExecutionID,
		Status: TaskFailed, StartedAt: now.Add(-time.Minute), EndedAt: now,
		ModelProfile: r.ModelProfile, ProfileRevision: "revision",
		EffectiveLimits: core.DefaultLimits(), Validation: "not_run",
		SideEffectState: SideEffectNone,
	}
	result.Error = &TaskError{Category: "provider_error", Stage: "planning", Retry: "never"}
	result.Error.Stage = nearLimitErrorStage(t, result)
	encodedResult, err := json.Marshal(result)
	if err != nil || int64(len(encodedResult)) > receiptMaxRecord {
		t.Fatalf("test result exceeds result boundary: bytes=%d err=%v", len(encodedResult), err)
	}
	receipt := Receipt{
		Owner: "principal", ExecutionID: rec.ExecutionID, TaskID: rec.TaskID,
		RequestDigest: d, State: ReceiptFailed, Result: &result, CreatedAt: rec.CreatedAt,
	}
	encodedReceipt, err := json.Marshal(receipt)
	if err != nil || int64(len(encodedReceipt)) <= receiptMaxRecord {
		t.Fatalf("test receipt does not exceed full receipt boundary: bytes=%d err=%v", len(encodedReceipt), err)
	}

	finalizeErr := s.Finalize(context.Background(), "principal", rec.ExecutionID, result)
	if finalizeErr == nil {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if reopened, err := OpenReceiptStore(dir, 32<<20); err != nil {
			t.Fatalf("Finalize accepted a result that made the store unreopenable: %v", err)
		} else {
			_ = reopened.Close()
		}
		t.Fatal("Finalize accepted a full receipt above its persisted record limit")
	}
	if got, err := s.Get(context.Background(), "principal", rec.ExecutionID); err != nil || got.Result != nil || got.State != ReceiptAdmitted {
		t.Fatalf("rejected finalization corrupted the existing receipt: %+v %v", got, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenReceiptStore(dir, 32<<20)
	if err != nil {
		t.Fatalf("store failed to reopen after rejected finalization: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if got, err := reopened.Lookup(context.Background(), "principal", r.TaskID); err != nil || got.Result != nil {
		t.Fatalf("existing receipt was lost after rejected finalization: %+v %v", got, err)
	}
}

func nearLimitErrorStage(t *testing.T, result TaskResult) string {
	t.Helper()
	if result.Error == nil {
		t.Fatal("near-limit result needs an error")
	}
	low, high := 0, int(receiptMaxRecord)
	for low < high {
		mid := low + (high-low+1)/2
		result.Error.Stage = strings.Repeat("s", mid)
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal candidate result: %v", err)
		}
		if int64(len(encoded)) <= receiptMaxRecord {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return strings.Repeat("s", low)
}

func TestReceipts_CanonicalSchemaDigest(t *testing.T) {
	s, base, _ := receiptFixture(t, t.TempDir())
	r1 := base
	r1.OutputSchema = []byte(`{"type":"object","properties":{"title":{"type":"string","description":"title"},"count":{"type":"integer","minimum":1}},"required":["title"]}`)
	d1, err := canonicalRequestDigest(r1)
	if err != nil {
		t.Fatal(err)
	}
	r2 := base
	r2.OutputSchema = []byte(`{"required":["title"],"properties":{"count":{"minimum":1,"type":"integer"},"title":{"description":"title","type":"string"}},"type":"object"}`)
	d2, err := canonicalRequestDigest(r2)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Fatalf("reordered nested schema keys changed request identity: %s != %s", d1, d2)
	}
	first, created, err := s.Admit(context.Background(), "principal", r1, d1)
	if err != nil || !created {
		t.Fatalf("first schema admission = %+v, created=%v err=%v", first, created, err)
	}
	duplicate, created, err := s.Admit(context.Background(), "principal", r2, d2)
	if err != nil || created || duplicate.ExecutionID != first.ExecutionID {
		t.Fatalf("canonical duplicate admission = %+v, created=%v err=%v", duplicate, created, err)
	}
	r3 := base
	r3.OutputSchema = []byte(`{"required":["title"],"properties":{"count":{"minimum":1,"type":"integer"},"title":{"description":"title","type":"number"}},"type":"object"}`)
	d3, err := canonicalRequestDigest(r3)
	if err != nil {
		t.Fatal(err)
	}
	if d3 == d1 {
		t.Fatal("actual schema change retained the same request identity")
	}
	if _, created, err := s.Admit(context.Background(), "principal", r3, d3); !errors.Is(err, ErrReceiptConflict) || created {
		t.Fatalf("changed output schema admission = created %v err %v", created, err)
	}

	precise1 := base
	precise1.OutputSchema = []byte(`{"type":"integer","minimum":9007199254740992}`)
	precise2 := base
	precise2.OutputSchema = []byte(`{"minimum":9007199254740993,"type":"integer"}`)
	p1, err := canonicalRequestDigest(precise1)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := canonicalRequestDigest(precise2)
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatal("schema canonicalization collapsed distinct integer precision")
	}
}

func TestReceipts_FinalizeHTMLRawResultForms(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     TaskStatus
		validation string
		setOutput  func(*TaskResult, json.RawMessage)
	}{
		{
			name:   "accepted result",
			status: TaskSucceeded, validation: "valid",
			setOutput: func(result *TaskResult, raw json.RawMessage) { result.Result = raw },
		},
		{
			name:   "partial result",
			status: TaskFailed, validation: "invalid",
			setOutput: func(result *TaskResult, raw json.RawMessage) { result.PartialResult = raw },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			s, r, d := receiptFixture(t, dir)
			rec := admitFixture(t, s, r, d)
			now := time.Now().UTC()
			result := TaskResult{
				Schema: "ferro.result/v2", TaskID: r.TaskID, ExecutionID: rec.ExecutionID,
				Status: tc.status, StartedAt: now.Add(-time.Minute), EndedAt: now,
				ModelProfile: r.ModelProfile, ProfileRevision: "revision",
				EffectiveLimits: core.DefaultLimits(), Validation: tc.validation,
				SideEffectState: SideEffectNone,
			}
			raw := json.RawMessage(`{"html":"` + strings.Repeat("<", 3000) + `"}`)
			tc.setOutput(&result, raw)
			if len(raw) >= 16<<10 {
				t.Fatalf("fixture raw JSON is not below inline limit: %d", len(raw))
			}
			if err := ValidateTaskResult(result); err != nil {
				t.Fatalf("raw result should pass envelope validation before serialization: %v", err)
			}
			finalizeErr := s.Finalize(context.Background(), "principal", rec.ExecutionID, result)
			if finalizeErr == nil {
				got, err := s.Get(context.Background(), "principal", rec.ExecutionID)
				if err != nil {
					t.Fatal(err)
				}
				if got.Result == nil || ValidateTaskResult(*got.Result) == nil {
					t.Fatal("Finalize persisted an expanded raw result that still passed validation")
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				if reopened, err := OpenReceiptStore(dir, 32<<20); err != nil {
					t.Fatalf("Finalize accepted a result that made the store unreopenable: %v", err)
				} else {
					_ = reopened.Close()
				}
				t.Fatal("Finalize accepted an HTML-expanded raw result above the inline limit")
			}
			if got, err := s.Get(context.Background(), "principal", rec.ExecutionID); err != nil || got.Result != nil || got.State != ReceiptAdmitted {
				t.Fatalf("rejected finalization corrupted the existing receipt: %+v %v", got, err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenReceiptStore(dir, 32<<20)
			if err != nil {
				t.Fatalf("store failed to reopen after rejected finalization: %v", err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			if got, err := reopened.Lookup(context.Background(), "principal", r.TaskID); err != nil || got.Result != nil {
				t.Fatalf("existing receipt was lost after rejected finalization: %+v %v", got, err)
			}
		})
	}
}

func TestReceipts_HTMLSchemaDigestAndRequestBounds(t *testing.T) {
	s, request, _ := receiptFixture(t, t.TempDir())
	largeText := strings.Repeat("<", 6000)
	request.OutputSchema = json.RawMessage(`{"type":"object","properties":{"x":{"description":"` + largeText + `","type":"string"}}}`)
	rawRequest := receiptRequestJSON(request)
	validated, err := ValidateTaskRequest(rawRequest)
	if err != nil {
		t.Fatalf("raw HTML schema inside wire limits rejected: %v", err)
	}
	digest, err := canonicalRequestDigest(validated)
	if err != nil {
		t.Fatalf("digest valid raw HTML schema: %v", err)
	}
	first, created, err := s.Admit(context.Background(), "principal", validated, digest)
	if err != nil || !created {
		t.Fatalf("admit valid raw HTML schema = %+v created=%v err=%v", first, created, err)
	}
	request.OutputSchema = json.RawMessage(`{"properties":{"x":{"type":"string","description":"` + largeText + `"}},"type":"object"}`)
	reordered, err := canonicalRequestDigest(request)
	if err != nil || reordered != digest {
		t.Fatalf("reordered HTML schema digest=%s err=%v; want %s", reordered, err, digest)
	}
	duplicate, created, err := s.Admit(context.Background(), "principal", request, reordered)
	if err != nil || created || duplicate.ExecutionID != first.ExecutionID {
		t.Fatalf("admit reordered HTML schema = %+v created=%v err=%v", duplicate, created, err)
	}

	maxSchema := json.RawMessage(`{"description":"` + strings.Repeat("x", 32768-len(`{"description":"`)-len(`"}`)) + `"}`)
	if len(maxSchema) != 32768 {
		t.Fatalf("max schema fixture length=%d", len(maxSchema))
	}
	request.OutputSchema = maxSchema
	if _, err := ValidateTaskRequest(receiptRequestJSON(request)); err != nil {
		t.Fatalf("schema exactly at byte limit rejected: %v", err)
	}
	request.OutputSchema = json.RawMessage(`{"description":"` + strings.Repeat("x", 32769-len(`{"description":"`)-len(`"}`)) + `"}`)
	if len(request.OutputSchema) != 32769 {
		t.Fatalf("oversized schema fixture length=%d", len(request.OutputSchema))
	}
	if _, err := ValidateTaskRequest(receiptRequestJSON(request)); err == nil {
		t.Fatal("schema above byte limit was accepted")
	}

	request.OutputSchema = []byte(`{"type":"object"}`)
	maxRequest := receiptRequestJSON(request)
	if len(maxRequest) > 65536 {
		t.Fatalf("base request exceeds wire limit: %d", len(maxRequest))
	}
	maxRequest = append(maxRequest, bytes.Repeat([]byte{' '}, 65536-len(maxRequest))...)
	if _, err := ValidateTaskRequest(maxRequest); err != nil {
		t.Fatalf("request exactly at byte limit rejected: %v", err)
	}
	maxRequest = append(maxRequest, ' ')
	if _, err := ValidateTaskRequest(maxRequest); err == nil {
		t.Fatal("request above byte limit was accepted")
	}
}

func TestReceipts_RecoveryReservesCapacity(t *testing.T) {
	request := RunTaskRequest{
		Schema: "ferro.task/v2", TaskID: "task_capacity", Goal: "inspect", ModelProfile: "profile",
		Policy:       &TaskPolicy{Mode: "read_only", Origins: []string{"https://example.com"}},
		OutputSchema: []byte(`{"type":"object"}`),
	}
	digest, err := canonicalRequestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("rejects write without recovery headroom", func(t *testing.T) {
		second := request
		second.TaskID = "task_capacity2"
		secondDigest, err := canonicalRequestDigest(second)
		if err != nil {
			t.Fatal(err)
		}
		// Calibrate to the exact active representation, including multiple rows
		// and a running row with artifact data.
		calibrationDir := t.TempDir()
		calibration, err := OpenReceiptStore(calibrationDir, 4096)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := calibration.Admit(context.Background(), "principal", request, digest); err != nil {
			t.Fatal(err)
		}
		secondReceipt, _, err := calibration.Admit(context.Background(), "principal", second, secondDigest)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := calibration.PutArtifact(context.Background(), "principal", secondReceipt.ExecutionID, []byte("artifact"), "text/plain"); err != nil {
			t.Fatal(err)
		}
		if err := calibration.Close(); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(calibrationDir, receiptStoreFile))
		if err != nil {
			t.Fatal(err)
		}
		capacity := info.Size()
		dir := t.TempDir()
		s, err := OpenReceiptStore(dir, capacity)
		if err != nil {
			t.Fatal(err)
		}
		firstReceipt, created, err := s.Admit(context.Background(), "principal", request, digest)
		if err != nil || !created {
			t.Fatalf("first capacity admission = %+v created=%v err=%v", firstReceipt, created, err)
		}
		secondReceipt, created, err = s.Admit(context.Background(), "principal", second, secondDigest)
		if err != nil || !created {
			t.Fatalf("second capacity admission = %+v created=%v err=%v", secondReceipt, created, err)
		}
		if _, err := s.PutArtifact(context.Background(), "principal", secondReceipt.ExecutionID, []byte("artifact"), "text/plain"); !errors.Is(err, ErrReceiptCapacity) {
			t.Fatalf("artifact update without recovery headroom = %v", err)
		}
		if got, err := s.Lookup(context.Background(), "principal", request.TaskID); err != nil || got.State != ReceiptAdmitted {
			t.Fatalf("capacity rejection changed first active receipt: %+v %v", got, err)
		}
		if got, err := s.Lookup(context.Background(), "principal", second.TaskID); err != nil || got.State != ReceiptAdmitted || len(got.Artifacts) != 0 {
			t.Fatalf("capacity rejection changed second active receipt: %+v %v", got, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := OpenReceiptStore(dir, capacity)
		if err != nil {
			t.Fatalf("capacity rejection poisoned empty store: %v", err)
		}
		defer reopened.Close()
		for _, taskID := range []string{request.TaskID, second.TaskID} {
			got, err := reopened.Lookup(context.Background(), "principal", taskID)
			if err != nil || got.State != ReceiptUncertain {
				t.Fatalf("recovered receipt %s = %+v err=%v", taskID, got, err)
			}
		}
	})
	t.Run("accepted active receipt recovers", func(t *testing.T) {
		dir := t.TempDir()
		s, err := OpenReceiptStore(dir, 4096)
		if err != nil {
			t.Fatal(err)
		}
		first, created, err := s.Admit(context.Background(), "principal", request, digest)
		if err != nil || !created {
			t.Fatalf("admission = %+v created=%v err=%v", first, created, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := OpenReceiptStore(dir, 4096)
		if err != nil {
			t.Fatalf("accepted active receipt could not recover: %v", err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		got, err := reopened.Lookup(context.Background(), "principal", request.TaskID)
		if err != nil || got.ExecutionID != first.ExecutionID || got.State != ReceiptUncertain {
			t.Fatalf("recovered receipt = %+v err=%v", got, err)
		}
	})
}

func TestReceipts_UnicodeLineSeparatorDigestWithinWireBounds(t *testing.T) {
	store, request, _ := receiptFixture(t, t.TempDir())
	request.TaskID = "task_unicode"
	request.Goal = strings.Repeat("\u2028", 5461)
	prefix, suffix := `{"description":"`, `"}`
	count := (32768 - len(prefix) - len(suffix)) / len("\u2028")
	request.OutputSchema = json.RawMessage(prefix + strings.Repeat("\u2028", count-1) + "\u2029" + suffix)
	raw := receiptRequestJSON(request)
	if len(raw) > 65536 || len(request.Goal) > 16384 || len(request.OutputSchema) > 32768 {
		t.Fatalf("test request does not fit raw limits: request=%d goal=%d schema=%d", len(raw), len(request.Goal), len(request.OutputSchema))
	}
	validated, err := ValidateTaskRequest(raw)
	if err != nil {
		t.Fatalf("valid U+2028 request rejected before digest: %v", err)
	}
	if _, err := canonicalRequestDigest(validated); err != nil {
		t.Fatalf("valid U+2028 request digest failed: %v", err)
	}
	digest, err := canonicalRequestDigest(validated)
	if err != nil {
		t.Fatal(err)
	}
	compact := request
	compact.Goal = "line\u2028line\u2029"
	compact.OutputSchema = json.RawMessage(`{"description":"\u2028"}`)
	literalValidated, err := ValidateTaskRequest(receiptRequestJSON(compact))
	if err != nil {
		t.Fatalf("literal equivalent request rejected: %v", err)
	}
	literalDigest, err := canonicalRequestDigest(literalValidated)
	if err != nil {
		t.Fatal(err)
	}
	escapedRaw, err := json.Marshal(compact)
	if err != nil {
		t.Fatal(err)
	}
	escapedValidated, err := ValidateTaskRequest(escapedRaw)
	if err != nil {
		t.Fatalf("escaped equivalent request rejected: %v", err)
	}
	escapedDigest, err := canonicalRequestDigest(escapedValidated)
	if err != nil || escapedDigest != literalDigest {
		t.Fatalf("equivalent escaped request digest = %q err=%v want %q", escapedDigest, err, literalDigest)
	}
	if _, created, err := store.Admit(context.Background(), "principal", validated, digest); err != nil || !created {
		t.Fatalf("valid U+2028 request admission created=%v err=%v", created, err)
	}
}

func TestReceipts_WireSizeAndTypedSemanticLimits(t *testing.T) {
	goal := strings.Repeat("g", 16384)
	schemaPrefix, schemaSuffix := `{"type":"object","description":"`, `"}`
	requestPrefix := `{"schema":"ferro.task/v2","task_id":"task_default_cap","goal":"` + goal + `","start_url":"https://example.com/start","model_profile":"profile","policy":{"mode":"read_only","origins":["https://example.com"]},"output_schema":`
	buildRaw := func(description string) []byte {
		return []byte(requestPrefix + schemaPrefix + description + schemaSuffix + `}`)
	}
	description := strings.Repeat("d", 32018-len(schemaPrefix)-len(schemaSuffix))
	raw := buildRaw(description)
	padding := 65536 - len(raw) + len("https://example.com/start") - len("https://example.com/")
	if padding <= 0 {
		t.Fatalf("fixture base unexpectedly exceeds wire limit: %d", len(raw))
	}
	raw = bytes.Replace(raw, []byte("https://example.com/start"), []byte("https://example.com/"+strings.Repeat("x", padding)), 1)
	if len(raw) != 65536 || len(goal) != 16384 || len(schemaPrefix)+len(description)+len(schemaSuffix) > 32768 {
		t.Fatalf("bad boundary fixture: raw=%d goal=%d schema=%d", len(raw), len(goal), len(schemaPrefix)+len(description)+len(schemaSuffix))
	}
	validated, err := ValidateTaskRequest(raw)
	if err != nil {
		t.Fatalf("exact-limit request rejected: %v", err)
	}
	digest, err := canonicalRequestDigest(validated)
	if err != nil {
		t.Fatalf("normalizing valid exact-limit request rejected it: %v", err)
	}
	store, err := OpenReceiptStore(t.TempDir(), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, created, err := store.Admit(context.Background(), "principal", validated, digest); err != nil || !created {
		t.Fatalf("exact-limit admission created=%v err=%v", created, err)
	}

	if _, err := ValidateTaskRequest(append(append([]byte(nil), raw...), ' ')); err == nil {
		t.Fatal("request one byte over the wire limit was accepted")
	}
	oversizedField := validated
	oversizedField.Goal += "x"
	if _, err := canonicalRequestDigest(oversizedField); err == nil {
		t.Fatal("direct typed request bypassed the goal field limit")
	}

	withExplicitDefaults := validated
	withExplicitDefaults.Limits = core.LimitOverrides{}
	withExplicitDefaults.Evidence = EvidenceCompact
	if got, err := canonicalRequestDigest(withExplicitDefaults); err != nil || got != digest {
		t.Fatalf("explicit defaults digest = %q err=%v, want %q", got, err, digest)
	}
	omittedDefaults := validated
	omittedDefaults.Evidence = ""
	if got, err := canonicalRequestDigest(omittedDefaults); err != nil || got != digest {
		t.Fatalf("omitted defaults digest = %q err=%v, want %q", got, err, digest)
	}
	invalidEvidence := validated
	invalidEvidence.Evidence = "unsupported"
	if _, err := canonicalRequestDigest(invalidEvidence); err == nil {
		t.Fatal("invalid nonempty evidence bypassed request validation")
	}
	negativeLimit := int64(-1)
	invalidLimits := validated
	invalidLimits.Limits.Actions = &negativeLimit
	if _, err := canonicalRequestDigest(invalidLimits); err == nil {
		t.Fatal("invalid nonempty limits bypassed request validation")
	}
}

func TestReceipts_UnicodeOriginNormalizationAtWireLimit(t *testing.T) {
	goal := strings.Repeat("g", 16384)
	origin := "https://Ⱥ.example"
	schemaPrefix, schemaSuffix := `{"type":"object","description":"`, `"}`
	description := strings.Repeat("d", 32018-len(schemaPrefix)-len(schemaSuffix))
	startURL := origin + "/start"
	buildRaw := func(start string) []byte {
		return []byte(`{"schema":"ferro.task/v2","task_id":"task_unicode_origin","goal":"` + goal + `","start_url":"` + start + `","model_profile":"profile","policy":{"mode":"read_only","origins":["` + origin + `"]},"output_schema":` + schemaPrefix + description + schemaSuffix + `}`)
	}
	raw := buildRaw(startURL)
	padding := 65536 - len(raw) + len(startURL) - len(origin+"/")
	raw = buildRaw(origin + "/" + strings.Repeat("x", padding))
	if len(raw) != 65536 {
		t.Fatalf("Unicode-origin fixture is %d bytes, want 65536", len(raw))
	}
	validated, err := ValidateTaskRequest(raw)
	if err != nil {
		t.Fatalf("exact-limit Unicode-origin request rejected: %v", err)
	}
	if validated.Policy.Origins[0] != "https://ⱥ.example" {
		t.Fatalf("origin was not normalized as expected: %q", validated.Policy.Origins[0])
	}
	typedUnnormalized := validated
	typedUnnormalized.Policy = &TaskPolicy{Mode: "read_only", Origins: []string{origin}}
	digest, err := canonicalRequestDigest(validated)
	if err != nil {
		t.Fatalf("normalized origin expansion invalidated request: %v", err)
	}
	unnormalizedDigest, err := canonicalRequestDigest(typedUnnormalized)
	if err != nil || unnormalizedDigest != digest {
		t.Fatalf("typed unnormalized origin digest=%q err=%v want %q", unnormalizedDigest, err, digest)
	}
	if typedUnnormalized.Policy.Origins[0] != origin {
		t.Fatalf("digest mutated caller-owned policy origin to %q", typedUnnormalized.Policy.Origins[0])
	}
	store, err := OpenReceiptStore(t.TempDir(), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, created, err := store.Admit(context.Background(), "principal", validated, digest); err != nil || !created {
		t.Fatalf("Unicode-origin admission created=%v err=%v", created, err)
	}
}

func TestMarshalJSONNoHTMLEscapeLineSeparatorSemantics(t *testing.T) {
	for _, separator := range []string{"\u2028", "\u2029"} {
		value := map[string]string{"text": "left\\" + separator + "right", "literal": "\\u2028"}
		encoded, err := marshalJSONNoHTMLEscape(value)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]string
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("encoded JSON invalid: %v", err)
		}
		if decoded["text"] != value["text"] || decoded["literal"] != value["literal"] {
			t.Fatalf("round trip changed line separator or literal escape: got %#v want %#v", decoded, value)
		}
	}
}

func receiptRequestJSON(request RunTaskRequest) []byte {
	return []byte(`{"schema":"ferro.task/v2","task_id":"` + request.TaskID + `","goal":"` + request.Goal + `","model_profile":"` + request.ModelProfile + `","policy":{"mode":"` + request.Policy.Mode + `","origins":["https://example.com"]},"output_schema":` + string(request.OutputSchema) + `}`)
}
