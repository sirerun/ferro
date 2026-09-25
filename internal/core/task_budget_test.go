package core

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"
)

func budgetTestClock() (*taskBudget, *time.Time, error) {
	now := time.Now()
	clock := now
	budget, err := newTaskBudget(DefaultLimits(), now, func() time.Time { return clock })
	return budget, &clock, err
}

func usagePtr(value int64) *int64 { return &value }

func TestBudget_LastAdmission(t *testing.T) {
	limits := DefaultLimits()
	limits.ModelRequests = 1
	limits.PlanningPasses = 1
	budget, err := newTaskBudget(limits, time.Now(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Admit(context.Background(), budgetKindPlanning, 0, 10)
	if err != nil {
		t.Fatalf("last allowed request rejected: %v", err)
	}
	if reservation.ID == "" || reservation.Kind != budgetKindPlanning {
		t.Fatalf("bad reservation: %+v", reservation)
	}
	if _, err := budget.Admit(context.Background(), budgetKindExtraction, 0, 1); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("request after ceiling: got %v, want ErrBudgetExhausted", err)
	}
	got := budget.Snapshot()
	if got.Requests != 1 || got.PlanningPasses != 1 || got.ReservedTokens != 10 {
		t.Fatalf("unexpected admitted snapshot: %+v", got)
	}
}

func TestBudget_ConcurrentReservation(t *testing.T) {
	budget, _, err := budgetTestClock()
	if err != nil {
		t.Fatal(err)
	}
	const attempts = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := make(map[string]bool)
	successes := 0
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reservation, err := budget.Admit(context.Background(), budgetKindExtraction, 1, 1)
			if err != nil {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			successes++
			if ids[reservation.ID] {
				t.Errorf("duplicate reservation ID %q", reservation.ID)
			}
			ids[reservation.ID] = true
		}()
	}
	wg.Wait()
	if successes != int(DefaultLimits().ModelRequests) {
		t.Fatalf("successful admissions = %d, want %d", successes, DefaultLimits().ModelRequests)
	}
	if got := budget.Snapshot().Requests; got != int64(successes) {
		t.Fatalf("request count = %d, want %d", got, successes)
	}
}

func TestBudget_DoubleReconcile(t *testing.T) {
	budget, _, err := budgetTestClock()
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Admit(context.Background(), budgetKindRepair, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	completion := Completion{Transmission: TransmissionResponseReceived, Usage: RequestUsage{InputTokens: usagePtr(2), OutputTokens: usagePtr(3)}}
	if err := budget.Reconcile(reservation.ID, completion); err != nil {
		t.Fatal(err)
	}
	before := budget.Snapshot()
	if err := budget.Reconcile(reservation.ID, completion); !errors.Is(err, ErrDuplicateReconcile) {
		t.Fatalf("duplicate reconciliation: got %v, want ErrDuplicateReconcile", err)
	}
	if after := budget.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("duplicate reconciliation mutated snapshot: before=%+v after=%+v", before, after)
	}
}

func TestBudget_UnknownUsage(t *testing.T) {
	budget, _, err := budgetTestClock()
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Admit(context.Background(), budgetKindPlanning, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(reservation.ID, Completion{Transmission: TransmissionSentUnknown}); err != nil {
		t.Fatal(err)
	}
	got := budget.Snapshot()
	if got.ReservedTokens != 5 || got.UncertainRequests != 1 || got.ReportedUsage.InputTokens != nil {
		t.Fatalf("unknown send not retained distinctly: %+v", got)
	}
	reservation, err = budget.Admit(context.Background(), budgetKindExtraction, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(reservation.ID, Completion{Transmission: TransmissionResponseReceived, Usage: RequestUsage{InputTokens: usagePtr(1)}}); err != nil {
		t.Fatal(err)
	}
	got = budget.Snapshot()
	if got.ReservedTokens != 7 || got.UncertainRequests != 2 || got.ReportedUsage.InputTokens == nil || *got.ReportedUsage.InputTokens != 1 {
		t.Fatalf("partial response did not preserve reservation and known subtotal: %+v", got)
	}
}

func TestBudget_Overflow(t *testing.T) {
	budget, _, err := budgetTestClock()
	if err != nil {
		t.Fatal(err)
	}
	first, err := budget.Admit(context.Background(), budgetKindExtraction, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(first.ID, Completion{Transmission: TransmissionResponseReceived, Usage: RequestUsage{
		BilledMicroUSD: usagePtr(math.MaxInt64), TotalTokens: usagePtr(0),
	}}); err != nil {
		t.Fatal(err)
	}
	second, err := budget.Admit(context.Background(), budgetKindExtraction, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	before := budget.Snapshot()
	err = budget.Reconcile(second.ID, Completion{Transmission: TransmissionResponseReceived, Usage: RequestUsage{
		BilledMicroUSD: usagePtr(1), TotalTokens: usagePtr(0),
	}})
	if err == nil {
		t.Fatal("overflowing reported input subtotal accepted")
	}
	if after := budget.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("overflowing reconciliation mutated accounting: before=%+v after=%+v", before, after)
	}
	if err := budget.Reconcile(second.ID, Completion{Transmission: TransmissionResponseReceived, Usage: RequestUsage{TotalTokens: usagePtr(0)}}); err != nil {
		t.Fatalf("valid reconciliation after rejected overflow failed: %v", err)
	}
}

func TestBudget_UnsupportedHardCost(t *testing.T) {
	limits := DefaultLimits()
	reserve := int64(1)
	limits.ReserveMicroUSD = &reserve
	if _, err := NewTaskBudget(limits, time.Now()); !errors.Is(err, ErrUnsupportedCostReserve) {
		t.Fatalf("monetary reserve: got %v, want ErrUnsupportedCostReserve", err)
	}
	limits = DefaultLimits()
	limits.HardDollar = true
	if _, err := NewTaskBudget(limits, time.Now()); !errors.Is(err, ErrUnsupportedHardDollar) {
		t.Fatalf("hard-dollar mode: got %v, want ErrUnsupportedHardDollar", err)
	}
}

func TestBudget_DeadlineAndContext(t *testing.T) {
	limits := DefaultLimits()
	start := time.Now()
	clock := start
	budget, err := newTaskBudget(limits, start, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := budget.Admit(ctx, budgetKindPlanning, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context admission: %v", err)
	}
	if got := budget.Snapshot().Requests; got != 0 {
		t.Fatalf("cancelled context consumed request: %d", got)
	}
	clock = start.Add(time.Duration(limits.RuntimeMS) * time.Millisecond)
	if _, err := budget.Admit(context.Background(), budgetKindPlanning, 0, 1); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("expired budget admission: %v", err)
	}
	if err := budget.AdmitAction(context.Background()); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("expired action admission: %v", err)
	}
}

func TestBudget_MalformedCompletionIsNonmutating(t *testing.T) {
	budget, _, err := budgetTestClock()
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Admit(context.Background(), budgetKindPlanning, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	before := budget.Snapshot()
	if err := budget.Reconcile(reservation.ID, Completion{Transmission: TransmissionNotSent, Usage: RequestUsage{InputTokens: usagePtr(1)}}); err == nil {
		t.Fatal("usage attached to a not-sent completion was accepted")
	}
	if after := budget.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("malformed completion mutated accounting: before=%+v after=%+v", before, after)
	}
	if err := budget.Reconcile(reservation.ID, Completion{Transmission: TransmissionResponseReceived, Usage: RequestUsage{InputTokens: usagePtr(-1)}}); err == nil {
		t.Fatal("negative reported usage was accepted")
	}
	if after := budget.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("negative usage mutated accounting: before=%+v after=%+v", before, after)
	}
}

func TestBudget_InconsistentTotalIsNonmutating(t *testing.T) {
	budget, _, err := budgetTestClock()
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Admit(context.Background(), budgetKindPlanning, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	before := budget.Snapshot()
	underreported := Completion{Transmission: TransmissionResponseReceived, Usage: RequestUsage{
		InputTokens: usagePtr(1), TotalTokens: usagePtr(0),
	}}
	if err := budget.Reconcile(reservation.ID, underreported); err == nil {
		t.Fatal("zero total below reported input was accepted")
	}
	if after := budget.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("underreported total mutated accounting: before=%+v after=%+v", before, after)
	}
	corrected := Completion{Transmission: TransmissionResponseReceived, Usage: RequestUsage{
		InputTokens: usagePtr(1), TotalTokens: usagePtr(1),
	}}
	if err := budget.Reconcile(reservation.ID, corrected); err != nil {
		t.Fatalf("reservation was marked reconciled after invalid total: %v", err)
	}
}

func TestBudget_ReportedTokenSumOverflowIsNonmutating(t *testing.T) {
	budget, _, err := budgetTestClock()
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Admit(context.Background(), budgetKindPlanning, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	before := budget.Snapshot()
	overflow := Completion{Transmission: TransmissionResponseReceived, Usage: RequestUsage{
		InputTokens: usagePtr(math.MaxInt64), OutputTokens: usagePtr(1), TotalTokens: usagePtr(math.MaxInt64),
	}}
	if err := budget.Reconcile(reservation.ID, overflow); err == nil {
		t.Fatal("overflowing reported input/output sum was accepted")
	}
	if after := budget.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("overflowing reported token sum mutated accounting: before=%+v after=%+v", before, after)
	}
	corrected := Completion{Transmission: TransmissionResponseReceived, Usage: RequestUsage{
		InputTokens: usagePtr(math.MaxInt64), TotalTokens: usagePtr(math.MaxInt64),
	}}
	if err := budget.Reconcile(reservation.ID, corrected); err != nil {
		t.Fatalf("reservation was marked reconciled after overflow: %v", err)
	}
}

func TestBudget_NotSentReleasesReserveUnknownRetainsIt(t *testing.T) {
	budget, _, err := budgetTestClock()
	if err != nil {
		t.Fatal(err)
	}
	notSent, err := budget.Admit(context.Background(), budgetKindExtraction, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(notSent.ID, Completion{Transmission: TransmissionNotSent}); err != nil {
		t.Fatal(err)
	}
	if got := budget.Snapshot().ReservedTokens; got != 0 {
		t.Fatalf("not-sent reservation retained %d tokens", got)
	}
	unknown, err := budget.Admit(context.Background(), budgetKindExtraction, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(unknown.ID, Completion{Transmission: TransmissionSentUnknown}); err != nil {
		t.Fatal(err)
	}
	got := budget.Snapshot()
	if got.ReservedTokens != 5 || got.UncertainRequests != 1 || got.Requests != 2 {
		t.Fatalf("not-sent vs sent-unknown accounting: %+v", got)
	}
}

func TestBudget_ReportedOverEstimateBlocksLaterAdmission(t *testing.T) {
	limits := DefaultLimits()
	limits.TotalReservedTokens = 100
	budget, err := newTaskBudget(limits, time.Now(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Admit(context.Background(), budgetKindPlanning, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(reservation.ID, Completion{Transmission: TransmissionResponseReceived, Usage: RequestUsage{TotalTokens: usagePtr(6)}}); err != nil {
		t.Fatal(err)
	}
	if got := budget.Snapshot().ReservedTokens; got != 6 {
		t.Fatalf("truthful over-estimate usage = %d, want 6", got)
	}
	if _, err := budget.Admit(context.Background(), budgetKindExtraction, 0, 1); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("admission after actual overrun: got %v, want exhausted", err)
	}
	if err := budget.AdmitAction(context.Background()); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("action admission after actual overrun: got %v, want exhausted", err)
	}
	if got := budget.Snapshot().Actions; got != 0 {
		t.Fatalf("overrun action admission changed action count to %d", got)
	}
}

func TestBudget_SnapshotDeepCopy(t *testing.T) {
	budget, _, err := budgetTestClock()
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Admit(context.Background(), budgetKindPlanning, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(reservation.ID, Completion{Transmission: TransmissionResponseReceived, Usage: RequestUsage{InputTokens: usagePtr(1), OutputTokens: usagePtr(0)}}); err != nil {
		t.Fatal(err)
	}
	snapshot := budget.Snapshot()
	*snapshot.ReportedUsage.InputTokens = 500
	if got := *budget.Snapshot().ReportedUsage.InputTokens; got != 1 {
		t.Fatalf("snapshot mutation escaped into controller: %d", got)
	}
}

func TestBudget_ReportedCostCurrencyAndUnknownCost(t *testing.T) {
	budget, _, err := budgetTestClock()
	if err != nil {
		t.Fatal(err)
	}
	knownTokens, err := budget.Admit(context.Background(), budgetKindPlanning, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(knownTokens.ID, Completion{Transmission: TransmissionResponseReceived, Usage: RequestUsage{
		InputTokens: usagePtr(2), OutputTokens: usagePtr(3),
	}}); err != nil {
		t.Fatal(err)
	}
	got := budget.Snapshot()
	if got.UncertainRequests != 1 || got.ReportedUsage.BilledMicroUSD != nil || got.Currency != "" {
		t.Fatalf("unknown cost was fabricated or not marked uncertain: %+v", got)
	}

	knownCost, err := budget.Admit(context.Background(), budgetKindExtraction, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(knownCost.ID, Completion{Transmission: TransmissionResponseReceived, Usage: RequestUsage{
		TotalTokens: usagePtr(1), BilledMicroUSD: usagePtr(7),
	}}); err != nil {
		t.Fatal(err)
	}
	got = budget.Snapshot()
	if got.Currency != "USD" || got.ReportedUsage.BilledMicroUSD == nil || *got.ReportedUsage.BilledMicroUSD != 7 {
		t.Fatalf("reported cost missing explicit currency: %+v", got)
	}
}
