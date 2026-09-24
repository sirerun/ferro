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

func budgetTestClockV2() (*taskBudgetV2, *time.Time, error) {
	now := time.Now()
	clock := now
	budget, err := newTaskBudgetV2(DefaultLimitsV2(), now, func() time.Time { return clock })
	return budget, &clock, err
}

func usagePtrV2(value int64) *int64 { return &value }

func TestBudgetV2_LastAdmission(t *testing.T) {
	limits := DefaultLimitsV2()
	limits.ModelRequests = 1
	limits.PlanningPasses = 1
	budget, err := newTaskBudgetV2(limits, time.Now(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Admit(context.Background(), budgetKindPlanningV2, 0, 10)
	if err != nil {
		t.Fatalf("last allowed request rejected: %v", err)
	}
	if reservation.ID == "" || reservation.Kind != budgetKindPlanningV2 {
		t.Fatalf("bad reservation: %+v", reservation)
	}
	if _, err := budget.Admit(context.Background(), budgetKindExtractionV2, 0, 1); !errors.Is(err, ErrBudgetExhaustedV2) {
		t.Fatalf("request after ceiling: got %v, want ErrBudgetExhaustedV2", err)
	}
	got := budget.Snapshot()
	if got.Requests != 1 || got.PlanningPasses != 1 || got.ReservedTokens != 10 {
		t.Fatalf("unexpected admitted snapshot: %+v", got)
	}
}

func TestBudgetV2_ConcurrentReservation(t *testing.T) {
	budget, _, err := budgetTestClockV2()
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
			reservation, err := budget.Admit(context.Background(), budgetKindExtractionV2, 1, 1)
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
	if successes != int(DefaultLimitsV2().ModelRequests) {
		t.Fatalf("successful admissions = %d, want %d", successes, DefaultLimitsV2().ModelRequests)
	}
	if got := budget.Snapshot().Requests; got != int64(successes) {
		t.Fatalf("request count = %d, want %d", got, successes)
	}
}

func TestBudgetV2_DoubleReconcile(t *testing.T) {
	budget, _, err := budgetTestClockV2()
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Admit(context.Background(), budgetKindRepairV2, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	completion := CompletionV2{Transmission: TransmissionResponseReceivedV2, Usage: RequestUsageV2{InputTokens: usagePtrV2(2), OutputTokens: usagePtrV2(3)}}
	if err := budget.Reconcile(reservation.ID, completion); err != nil {
		t.Fatal(err)
	}
	before := budget.Snapshot()
	if err := budget.Reconcile(reservation.ID, completion); !errors.Is(err, ErrDuplicateReconcileV2) {
		t.Fatalf("duplicate reconciliation: got %v, want ErrDuplicateReconcileV2", err)
	}
	if after := budget.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("duplicate reconciliation mutated snapshot: before=%+v after=%+v", before, after)
	}
}

func TestBudgetV2_UnknownUsage(t *testing.T) {
	budget, _, err := budgetTestClockV2()
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Admit(context.Background(), budgetKindPlanningV2, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(reservation.ID, CompletionV2{Transmission: TransmissionSentUnknownV2}); err != nil {
		t.Fatal(err)
	}
	got := budget.Snapshot()
	if got.ReservedTokens != 5 || got.UncertainRequests != 1 || got.ReportedUsage.InputTokens != nil {
		t.Fatalf("unknown send not retained distinctly: %+v", got)
	}
	reservation, err = budget.Admit(context.Background(), budgetKindExtractionV2, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(reservation.ID, CompletionV2{Transmission: TransmissionResponseReceivedV2, Usage: RequestUsageV2{InputTokens: usagePtrV2(1)}}); err != nil {
		t.Fatal(err)
	}
	got = budget.Snapshot()
	if got.ReservedTokens != 7 || got.UncertainRequests != 2 || got.ReportedUsage.InputTokens == nil || *got.ReportedUsage.InputTokens != 1 {
		t.Fatalf("partial response did not preserve reservation and known subtotal: %+v", got)
	}
}

func TestBudgetV2_Overflow(t *testing.T) {
	budget, _, err := budgetTestClockV2()
	if err != nil {
		t.Fatal(err)
	}
	first, err := budget.Admit(context.Background(), budgetKindExtractionV2, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(first.ID, CompletionV2{Transmission: TransmissionResponseReceivedV2, Usage: RequestUsageV2{
		InputTokens: usagePtrV2(math.MaxInt64), TotalTokens: usagePtrV2(0),
	}}); err != nil {
		t.Fatal(err)
	}
	second, err := budget.Admit(context.Background(), budgetKindExtractionV2, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	before := budget.Snapshot()
	err = budget.Reconcile(second.ID, CompletionV2{Transmission: TransmissionResponseReceivedV2, Usage: RequestUsageV2{
		InputTokens: usagePtrV2(1), TotalTokens: usagePtrV2(0),
	}})
	if err == nil {
		t.Fatal("overflowing reported input subtotal accepted")
	}
	if after := budget.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("overflowing reconciliation mutated accounting: before=%+v after=%+v", before, after)
	}
	if err := budget.Reconcile(second.ID, CompletionV2{Transmission: TransmissionResponseReceivedV2, Usage: RequestUsageV2{TotalTokens: usagePtrV2(math.MaxInt64)}}); err != nil {
		t.Fatalf("valid reconciliation after rejected overflow failed: %v", err)
	}
}

func TestBudgetV2_UnsupportedHardCost(t *testing.T) {
	limits := DefaultLimitsV2()
	reserve := int64(1)
	limits.ReserveMicroUSD = &reserve
	if _, err := NewTaskBudgetV2(limits, time.Now()); !errors.Is(err, ErrUnsupportedCostReserveV2) {
		t.Fatalf("monetary reserve: got %v, want ErrUnsupportedCostReserveV2", err)
	}
	limits = DefaultLimitsV2()
	limits.HardDollar = true
	if _, err := NewTaskBudgetV2(limits, time.Now()); !errors.Is(err, ErrUnsupportedHardDollarV2) {
		t.Fatalf("hard-dollar mode: got %v, want ErrUnsupportedHardDollarV2", err)
	}
}

func TestBudgetV2_DeadlineAndContext(t *testing.T) {
	limits := DefaultLimitsV2()
	start := time.Now()
	clock := start
	budget, err := newTaskBudgetV2(limits, start, func() time.Time { return clock })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := budget.Admit(ctx, budgetKindPlanningV2, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context admission: %v", err)
	}
	if got := budget.Snapshot().Requests; got != 0 {
		t.Fatalf("cancelled context consumed request: %d", got)
	}
	clock = start.Add(time.Duration(limits.RuntimeMS) * time.Millisecond)
	if _, err := budget.Admit(context.Background(), budgetKindPlanningV2, 0, 1); !errors.Is(err, ErrBudgetExhaustedV2) {
		t.Fatalf("expired budget admission: %v", err)
	}
	if err := budget.AdmitAction(context.Background()); !errors.Is(err, ErrBudgetExhaustedV2) {
		t.Fatalf("expired action admission: %v", err)
	}
}

func TestBudgetV2_MalformedCompletionIsNonmutating(t *testing.T) {
	budget, _, err := budgetTestClockV2()
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Admit(context.Background(), budgetKindPlanningV2, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	before := budget.Snapshot()
	if err := budget.Reconcile(reservation.ID, CompletionV2{Transmission: TransmissionNotSentV2, Usage: RequestUsageV2{InputTokens: usagePtrV2(1)}}); err == nil {
		t.Fatal("usage attached to a not-sent completion was accepted")
	}
	if after := budget.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("malformed completion mutated accounting: before=%+v after=%+v", before, after)
	}
	if err := budget.Reconcile(reservation.ID, CompletionV2{Transmission: TransmissionResponseReceivedV2, Usage: RequestUsageV2{InputTokens: usagePtrV2(-1)}}); err == nil {
		t.Fatal("negative reported usage was accepted")
	}
	if after := budget.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("negative usage mutated accounting: before=%+v after=%+v", before, after)
	}
}

func TestBudgetV2_NotSentReleasesReserveUnknownRetainsIt(t *testing.T) {
	budget, _, err := budgetTestClockV2()
	if err != nil {
		t.Fatal(err)
	}
	notSent, err := budget.Admit(context.Background(), budgetKindExtractionV2, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(notSent.ID, CompletionV2{Transmission: TransmissionNotSentV2}); err != nil {
		t.Fatal(err)
	}
	if got := budget.Snapshot().ReservedTokens; got != 0 {
		t.Fatalf("not-sent reservation retained %d tokens", got)
	}
	unknown, err := budget.Admit(context.Background(), budgetKindExtractionV2, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(unknown.ID, CompletionV2{Transmission: TransmissionSentUnknownV2}); err != nil {
		t.Fatal(err)
	}
	got := budget.Snapshot()
	if got.ReservedTokens != 5 || got.UncertainRequests != 1 || got.Requests != 2 {
		t.Fatalf("not-sent vs sent-unknown accounting: %+v", got)
	}
}

func TestBudgetV2_ReportedOverEstimateBlocksLaterAdmission(t *testing.T) {
	limits := DefaultLimitsV2()
	limits.TotalReservedTokens = 100
	budget, err := newTaskBudgetV2(limits, time.Now(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Admit(context.Background(), budgetKindPlanningV2, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(reservation.ID, CompletionV2{Transmission: TransmissionResponseReceivedV2, Usage: RequestUsageV2{TotalTokens: usagePtrV2(6)}}); err != nil {
		t.Fatal(err)
	}
	if got := budget.Snapshot().ReservedTokens; got != 6 {
		t.Fatalf("truthful over-estimate usage = %d, want 6", got)
	}
	if _, err := budget.Admit(context.Background(), budgetKindExtractionV2, 0, 1); !errors.Is(err, ErrBudgetExhaustedV2) {
		t.Fatalf("admission after actual overrun: got %v, want exhausted", err)
	}
}

func TestBudgetV2_SnapshotDeepCopy(t *testing.T) {
	budget, _, err := budgetTestClockV2()
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := budget.Admit(context.Background(), budgetKindPlanningV2, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(reservation.ID, CompletionV2{Transmission: TransmissionResponseReceivedV2, Usage: RequestUsageV2{InputTokens: usagePtrV2(1), OutputTokens: usagePtrV2(0)}}); err != nil {
		t.Fatal(err)
	}
	snapshot := budget.Snapshot()
	*snapshot.ReportedUsage.InputTokens = 500
	if got := *budget.Snapshot().ReportedUsage.InputTokens; got != 1 {
		t.Fatalf("snapshot mutation escaped into controller: %d", got)
	}
}

func TestBudgetV2_ReportedCostCurrencyAndUnknownCost(t *testing.T) {
	budget, _, err := budgetTestClockV2()
	if err != nil {
		t.Fatal(err)
	}
	knownTokens, err := budget.Admit(context.Background(), budgetKindPlanningV2, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(knownTokens.ID, CompletionV2{Transmission: TransmissionResponseReceivedV2, Usage: RequestUsageV2{
		InputTokens: usagePtrV2(2), OutputTokens: usagePtrV2(3),
	}}); err != nil {
		t.Fatal(err)
	}
	got := budget.Snapshot()
	if got.UncertainRequests != 1 || got.ReportedUsage.BilledMicroUSD != nil || got.Currency != "" {
		t.Fatalf("unknown cost was fabricated or not marked uncertain: %+v", got)
	}

	knownCost, err := budget.Admit(context.Background(), budgetKindExtractionV2, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := budget.Reconcile(knownCost.ID, CompletionV2{Transmission: TransmissionResponseReceivedV2, Usage: RequestUsageV2{
		TotalTokens: usagePtrV2(1), BilledMicroUSD: usagePtrV2(7),
	}}); err != nil {
		t.Fatal(err)
	}
	got = budget.Snapshot()
	if got.Currency != "USD" || got.ReportedUsage.BilledMicroUSD == nil || *got.ReportedUsage.BilledMicroUSD != 7 {
		t.Fatalf("reported cost missing explicit currency: %+v", got)
	}
}
