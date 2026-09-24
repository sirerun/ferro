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

	"github.com/dndungu/ferro/internal/core"
)

func receiptFixtureV2(t *testing.T, dir string) (ReceiptStoreV2, RunTaskV2Request, string) {
	t.Helper()
	s, err := OpenReceiptStoreV2(dir, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := RunTaskV2Request{Schema: "ferro.task/v2", TaskID: "task_one", Goal: "inspect", ModelProfile: "profile", Policy: &TaskPolicyV2{Mode: "read_only", Origins: []string{"https://example.com"}}, OutputSchema: []byte(`{"type":"object"}`)}
	digest, err := canonicalRequestDigestV2(r)
	if err != nil {
		t.Fatal(err)
	}
	return s, r, digest
}
func admitFixtureV2(t *testing.T, s ReceiptStoreV2, r RunTaskV2Request, digest string) ReceiptV2 {
	t.Helper()
	got, created, err := s.Admit(context.Background(), "principal", r, digest)
	if err != nil || !created {
		t.Fatalf("Admit() = (%+v,%v,%v)", got, created, err)
	}
	return got
}

func TestReceiptsV2_OwnerDenied(t *testing.T) {
	s, r, d := receiptFixtureV2(t, t.TempDir())
	got := admitFixtureV2(t, s, r, d)
	if _, err := s.Get(context.Background(), "someone-else", got.ExecutionID); !errors.Is(err, ErrReceiptNotFoundV2) && !errors.Is(err, ErrReceiptOwnerDeniedV2) {
		t.Fatalf("cross-owner lookup disclosed receipt: %v", err)
	}
	if _, err := s.Lookup(context.Background(), "someone-else", r.TaskID); !errors.Is(err, ErrReceiptNotFoundV2) {
		t.Fatalf("cross-owner caller-key lookup = %v", err)
	}
}

func TestReceiptsV2_ExclusiveLockAndClose(t *testing.T) {
	dir := t.TempDir()
	s, _, _ := receiptFixtureV2(t, dir)
	if _, err := OpenReceiptStoreV2(dir, 32<<20); err == nil {
		t.Fatal("second process-equivalent opener acquired the exclusive lock")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(context.Background(), "principal", "task_one"); err == nil {
		t.Fatal("lookup succeeded after Close")
	}
	reopened, err := OpenReceiptStoreV2(dir, 32<<20)
	if err != nil {
		t.Fatalf("lock was not released by Close: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReceiptsV2_PathTraversal(t *testing.T) {
	s, r, d := receiptFixtureV2(t, t.TempDir())
	if _, _, err := s.Admit(context.Background(), "principal", r, "../"+d); err == nil {
		t.Fatal("accepted malformed digest")
	}
	got := admitFixtureV2(t, s, r, d)
	if _, err := s.ReadArtifact(context.Background(), "principal", got.ExecutionID, "../../receipts-v2.json", 0, 12); err == nil {
		t.Fatal("accepted traversal artifact ID")
	}
}

func TestReceiptsV2_ReopenUncertain(t *testing.T) {
	dir := t.TempDir()
	s, r, d := receiptFixtureV2(t, dir)
	first := admitFixtureV2(t, s, r, d)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := OpenReceiptStoreV2(dir, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	got, err := s2.Lookup(context.Background(), "principal", r.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != ReceiptUncertainV2 || got.ExecutionID != first.ExecutionID {
		t.Fatalf("recovered receipt = %+v", got)
	}
}

func TestReceiptsV2_RequestConflict(t *testing.T) {
	s, r, d := receiptFixtureV2(t, t.TempDir())
	admitFixtureV2(t, s, r, d)
	changed := r
	changed.Goal = "different"
	changedDigest, err := canonicalRequestDigestV2(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := s.Admit(context.Background(), "principal", changed, changedDigest); !errors.Is(err, ErrReceiptConflictV2) || created {
		t.Fatalf("changed request = created %v, err %v", created, err)
	}
	if _, created, err := s.Admit(context.Background(), "principal", r, d); err != nil || created {
		t.Fatalf("duplicate admission = created %v, err %v", created, err)
	}
}

func TestReceiptsV2_BoundedRead(t *testing.T) {
	s, r, d := receiptFixtureV2(t, t.TempDir())
	rec := admitFixtureV2(t, s, r, d)
	data := bytes.Repeat([]byte("x"), int(receiptReadChunkV2+20))
	a, err := s.PutArtifact(context.Background(), "principal", rec.ExecutionID, data, "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := s.ReadArtifact(context.Background(), "principal", rec.ExecutionID, a.ID, 10, receiptReadChunkV2)
	if err != nil || len(chunk) != int(receiptReadChunkV2) {
		t.Fatalf("bounded read len=%d err=%v", len(chunk), err)
	}
	if _, err = s.ReadArtifact(context.Background(), "principal", rec.ExecutionID, a.ID, 0, receiptReadChunkV2+1); err == nil {
		t.Fatal("oversized chunk accepted")
	}
	chunk[0] = 'z'
	again, err := s.ReadArtifact(context.Background(), "principal", rec.ExecutionID, a.ID, 10, 1)
	if err != nil || again[0] != 'x' {
		t.Fatalf("read did not return a copy: %q %v", again, err)
	}
}

func TestReceiptsV2_WriteFailure(t *testing.T) {
	s, r, d := receiptFixtureV2(t, t.TempDir())
	internal := s.(*receiptStoreV2)
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
	reopened, err := OpenReceiptStoreV2(internal.dir, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err := reopened.Lookup(context.Background(), "principal", r.TaskID); !errors.Is(err, ErrReceiptNotFoundV2) {
		t.Fatalf("failed admission leaked into durable state: %v", err)
	}
}

func TestReceiptsV2_CallerKeyRecovery(t *testing.T) {
	dir := t.TempDir()
	s, r, d := receiptFixtureV2(t, dir)
	first := admitFixtureV2(t, s, r, d)
	_ = s.Close()
	s2, err := OpenReceiptStoreV2(dir, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	got, err := s2.Lookup(context.Background(), "principal", r.TaskID)
	if err != nil || got.ExecutionID != first.ExecutionID || got.State != ReceiptUncertainV2 {
		t.Fatalf("caller recovery = %+v, %v", got, err)
	}
}

func TestReceiptsV2_ConcurrentDuplicate(t *testing.T) {
	s, r, d := receiptFixtureV2(t, t.TempDir())
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

func TestReceiptsV2_RecoveryRetention(t *testing.T) {
	s, r, d := receiptFixtureV2(t, t.TempDir())
	got := admitFixtureV2(t, s, r, d)
	now := time.Now().UTC()
	billed := int64(0)
	result := TaskResultV2{Schema: "ferro.result/v2", TaskID: r.TaskID, ExecutionID: got.ExecutionID, Status: TaskFailedV2, StartedAt: now.Add(-time.Minute), EndedAt: now, ModelProfile: r.ModelProfile, ProfileRevision: "revision", EffectiveLimits: core.DefaultLimitsV2(), Validation: "not_run", Usage: core.RequestUsageV2{BilledMicroUSD: &billed}, SideEffectState: SideEffectNoneV2}
	if err := s.Finalize(context.Background(), "principal", got.ExecutionID, result); err != nil {
		t.Fatalf("Finalize(): %v", err)
	}
	internal := s.(*receiptStoreV2)
	key := receiptKeyV2("principal", r.TaskID)
	entry := internal.disk.Receipts[key]
	entry.Receipt.CreatedAt = now.Add(-31 * 24 * time.Hour)
	internal.disk.Receipts[key] = entry
	if err := internal.persist(internal.disk); err != nil {
		t.Fatal(err)
	}
	if err := s.Cleanup(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(context.Background(), "principal", r.TaskID); !errors.Is(err, ErrReceiptExpiredV2) {
		t.Fatalf("expired caller key did not resolve to tombstone: %v", err)
	}
	if _, _, err := s.Admit(context.Background(), "principal", r, d); !errors.Is(err, ErrReceiptExpiredV2) {
		t.Fatalf("expired key was admitted again: %v", err)
	}
	// An active entry is never eligible for cleanup, regardless of its age.
	r2 := r
	r2.TaskID = "active_task"
	d2, err := canonicalRequestDigestV2(r2)
	if err != nil {
		t.Fatal(err)
	}
	active := admitFixtureV2(t, s, r2, d2)
	activeEntry := internal.disk.Receipts[receiptKeyV2("principal", r2.TaskID)]
	activeEntry.Receipt.CreatedAt = now.Add(-365 * 24 * time.Hour)
	internal.disk.Receipts[receiptKeyV2("principal", r2.TaskID)] = activeEntry
	if err := internal.persist(internal.disk); err != nil {
		t.Fatal(err)
	}
	if err := s.Cleanup(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if recovered, err := s.Lookup(context.Background(), "principal", r2.TaskID); err != nil || recovered.ExecutionID != active.ExecutionID {
		t.Fatalf("active receipt removed: %+v %v", recovered, err)
	}
	if _, err := os.Stat(filepath.Join(internal.dir, receiptStoreFileV2)); err != nil {
		t.Fatal(err)
	}
	// Missing spend information keeps a terminal receipt unreconciled.
	r3 := r
	r3.TaskID = "unknown_spend"
	d3, err := canonicalRequestDigestV2(r3)
	if err != nil {
		t.Fatal(err)
	}
	unknown := admitFixtureV2(t, s, r3, d3)
	unknownResult := result
	unknownResult.TaskID = r3.TaskID
	unknownResult.ExecutionID = unknown.ExecutionID
	unknownResult.Usage = core.RequestUsageV2{}
	if err := s.Finalize(context.Background(), "principal", unknown.ExecutionID, unknownResult); err != nil {
		t.Fatal(err)
	}
	unknownEntry := internal.disk.Receipts[receiptKeyV2("principal", r3.TaskID)]
	unknownEntry.Receipt.CreatedAt = now.Add(-365 * 24 * time.Hour)
	internal.disk.Receipts[receiptKeyV2("principal", r3.TaskID)] = unknownEntry
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

func TestReceiptsV2_UnknownSpendCannotBeCleanedAfterFinalize(t *testing.T) {
	dir := t.TempDir()
	s, r, d := receiptFixtureV2(t, dir)
	rec := admitFixtureV2(t, s, r, d)
	now := time.Now().UTC()
	knownCost := int64(0)
	result := TaskResultV2{
		Schema: "ferro.result/v2", TaskID: r.TaskID, ExecutionID: rec.ExecutionID,
		Status: TaskFailedV2, StartedAt: now.Add(-time.Minute), EndedAt: now,
		ModelProfile: r.ModelProfile, ProfileRevision: "revision",
		EffectiveLimits: core.DefaultLimitsV2(), Validation: "not_run",
		Usage: core.RequestUsageV2{BilledMicroUSD: &knownCost},
		Budget: core.BudgetSnapshotV2{
			Requests: 2, UncertainRequests: 1,
			ReportedUsage: core.RequestUsageV2{BilledMicroUSD: &knownCost}, Currency: "USD",
		},
		SideEffectState: SideEffectNoneV2,
	}
	if err := s.Finalize(context.Background(), "principal", rec.ExecutionID, result); err != nil {
		t.Fatalf("Finalize(): %v", err)
	}
	internal := s.(*receiptStoreV2)
	key := receiptKeyV2("principal", r.TaskID)
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
	reopened, err := OpenReceiptStoreV2(dir, 32<<20)
	if err != nil {
		t.Fatalf("reopen after uncertain-spend cleanup: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if got, err := reopened.Lookup(context.Background(), "principal", r.TaskID); err != nil || got.Result == nil || got.Result.Budget.UncertainRequests != 1 {
		t.Fatalf("uncertain-spend receipt was not preserved: %+v %v", got, err)
	}
}

func TestReceiptsV2_NearLimitFinalizeRemainsReopenable(t *testing.T) {
	dir := t.TempDir()
	s, r, d := receiptFixtureV2(t, dir)
	rec := admitFixtureV2(t, s, r, d)
	now := time.Now().UTC()
	result := TaskResultV2{
		Schema: "ferro.result/v2", TaskID: r.TaskID, ExecutionID: rec.ExecutionID,
		Status: TaskFailedV2, StartedAt: now.Add(-time.Minute), EndedAt: now,
		ModelProfile: r.ModelProfile, ProfileRevision: "revision",
		EffectiveLimits: core.DefaultLimitsV2(), Validation: "not_run",
		SideEffectState: SideEffectNoneV2,
	}
	result.Error = &TaskErrorV2{Category: "provider_error", Stage: "planning", Retry: "never"}
	result.Error.Stage = nearLimitErrorStageV2(t, result)
	encodedResult, err := json.Marshal(result)
	if err != nil || int64(len(encodedResult)) > receiptMaxRecordV2 {
		t.Fatalf("test result exceeds result boundary: bytes=%d err=%v", len(encodedResult), err)
	}
	receipt := ReceiptV2{
		Owner: "principal", ExecutionID: rec.ExecutionID, TaskID: rec.TaskID,
		RequestDigest: d, State: ReceiptFailedV2, Result: &result, CreatedAt: rec.CreatedAt,
	}
	encodedReceipt, err := json.Marshal(receipt)
	if err != nil || int64(len(encodedReceipt)) <= receiptMaxRecordV2 {
		t.Fatalf("test receipt does not exceed full receipt boundary: bytes=%d err=%v", len(encodedReceipt), err)
	}

	finalizeErr := s.Finalize(context.Background(), "principal", rec.ExecutionID, result)
	if finalizeErr == nil {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if reopened, err := OpenReceiptStoreV2(dir, 32<<20); err != nil {
			t.Fatalf("Finalize accepted a result that made the store unreopenable: %v", err)
		} else {
			_ = reopened.Close()
		}
		t.Fatal("Finalize accepted a full receipt above its persisted record limit")
	}
	if got, err := s.Get(context.Background(), "principal", rec.ExecutionID); err != nil || got.Result != nil || got.State != ReceiptAdmittedV2 {
		t.Fatalf("rejected finalization corrupted the existing receipt: %+v %v", got, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenReceiptStoreV2(dir, 32<<20)
	if err != nil {
		t.Fatalf("store failed to reopen after rejected finalization: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if got, err := reopened.Lookup(context.Background(), "principal", r.TaskID); err != nil || got.Result != nil {
		t.Fatalf("existing receipt was lost after rejected finalization: %+v %v", got, err)
	}
}

func nearLimitErrorStageV2(t *testing.T, result TaskResultV2) string {
	t.Helper()
	if result.Error == nil {
		t.Fatal("near-limit result needs an error")
	}
	low, high := 0, int(receiptMaxRecordV2)
	for low < high {
		mid := low + (high-low+1)/2
		result.Error.Stage = strings.Repeat("s", mid)
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal candidate result: %v", err)
		}
		if int64(len(encoded)) <= receiptMaxRecordV2 {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return strings.Repeat("s", low)
}

func TestReceiptsV2_CanonicalSchemaDigest(t *testing.T) {
	s, base, _ := receiptFixtureV2(t, t.TempDir())
	r1 := base
	r1.OutputSchema = []byte(`{"type":"object","properties":{"title":{"type":"string","description":"title"},"count":{"type":"integer","minimum":1}},"required":["title"]}`)
	d1, err := canonicalRequestDigestV2(r1)
	if err != nil {
		t.Fatal(err)
	}
	r2 := base
	r2.OutputSchema = []byte(`{"required":["title"],"properties":{"count":{"minimum":1,"type":"integer"},"title":{"description":"title","type":"string"}},"type":"object"}`)
	d2, err := canonicalRequestDigestV2(r2)
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
	d3, err := canonicalRequestDigestV2(r3)
	if err != nil {
		t.Fatal(err)
	}
	if d3 == d1 {
		t.Fatal("actual schema change retained the same request identity")
	}
	if _, created, err := s.Admit(context.Background(), "principal", r3, d3); !errors.Is(err, ErrReceiptConflictV2) || created {
		t.Fatalf("changed output schema admission = created %v err %v", created, err)
	}

	precise1 := base
	precise1.OutputSchema = []byte(`{"type":"integer","minimum":9007199254740992}`)
	precise2 := base
	precise2.OutputSchema = []byte(`{"minimum":9007199254740993,"type":"integer"}`)
	p1, err := canonicalRequestDigestV2(precise1)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := canonicalRequestDigestV2(precise2)
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatal("schema canonicalization collapsed distinct integer precision")
	}
}
