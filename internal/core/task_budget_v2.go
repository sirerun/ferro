package core

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

const (
	budgetKindPlanningV2   = "planning"
	budgetKindRepairV2     = "repair"
	budgetKindExtractionV2 = "extraction"
	budgetKindParseRetryV2 = "parse_retry"
)

type taskBudgetV2 struct {
	mu         sync.Mutex
	limits     LimitsV2
	deadline   time.Time
	now        func() time.Time
	snapshot   BudgetSnapshotV2
	overrun    bool
	sequence   uint64
	pending    map[string]budgetReservationV2
	reconciled map[string]struct{}
}

type budgetReservationV2 struct {
	kind     string
	reserved int64
}

// NewTaskBudgetV2 creates a request and action budget with a fixed deadline.
func NewTaskBudgetV2(limits LimitsV2, started time.Time) (BudgetControllerV2, error) {
	return newTaskBudgetV2(limits, started, time.Now)
}

func newTaskBudgetV2(limits LimitsV2, started time.Time, now func() time.Time) (*taskBudgetV2, error) {
	if limits.ReserveMicroUSD != nil {
		return nil, ErrUnsupportedCostReserveV2
	}
	if limits.HardDollar {
		return nil, ErrUnsupportedHardDollarV2
	}
	if err := limits.ValidateV2(); err != nil {
		return nil, fmt.Errorf("invalid task budget limits: %w", err)
	}
	if started.IsZero() {
		return nil, fmt.Errorf("task budget start time is zero")
	}
	if now == nil {
		return nil, fmt.Errorf("task budget clock is nil")
	}
	current := now()
	if started.After(current) {
		return nil, fmt.Errorf("task budget start time is in the future")
	}
	deadline := started.Add(time.Duration(limits.RuntimeMS) * time.Millisecond)
	return &taskBudgetV2{
		limits:     cloneLimitsV2(limits),
		deadline:   deadline,
		now:        now,
		pending:    make(map[string]budgetReservationV2),
		reconciled: make(map[string]struct{}),
	}, nil
}

func (b *taskBudgetV2) Admit(ctx context.Context, kind string, estimatedInput, maxOutput int64) (ReservationV2, error) {
	if ctx == nil {
		return ReservationV2{}, fmt.Errorf("admit model request: nil context")
	}
	if err := b.contextError(ctx); err != nil {
		return ReservationV2{}, err
	}
	if !validBudgetKindV2(kind) {
		return ReservationV2{}, fmt.Errorf("invalid model request kind %q", kind)
	}
	if estimatedInput < 0 || estimatedInput > b.limits.MaxInputTokens {
		return ReservationV2{}, fmt.Errorf("estimated input tokens outside [0,%d]", b.limits.MaxInputTokens)
	}
	if maxOutput <= 0 || maxOutput > b.limits.MaxOutputTokens {
		return ReservationV2{}, fmt.Errorf("maximum output tokens outside [1,%d]", b.limits.MaxOutputTokens)
	}
	reserve, ok := addNonnegativeV2(estimatedInput, maxOutput)
	if !ok {
		return ReservationV2{}, fmt.Errorf("request token reservation overflow")
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.contextError(ctx); err != nil {
		return ReservationV2{}, err
	}
	if !b.now().Before(b.deadline) {
		return ReservationV2{}, ErrBudgetExhaustedV2
	}
	if b.overrun {
		return ReservationV2{}, ErrBudgetExhaustedV2
	}
	if b.snapshot.Requests >= b.limits.ModelRequests {
		return ReservationV2{}, ErrBudgetExhaustedV2
	}
	if kind == budgetKindPlanningV2 && b.snapshot.PlanningPasses >= b.limits.PlanningPasses {
		return ReservationV2{}, ErrBudgetExhaustedV2
	}
	if kind == budgetKindRepairV2 && b.snapshot.Repairs >= b.limits.Repairs {
		return ReservationV2{}, ErrBudgetExhaustedV2
	}
	newReserved, ok := addNonnegativeV2(b.snapshot.ReservedTokens, reserve)
	if !ok {
		return ReservationV2{}, fmt.Errorf("cumulative token reservation overflow")
	}
	if newReserved > b.limits.TotalReservedTokens {
		return ReservationV2{}, ErrBudgetExhaustedV2
	}
	newEstimatedInput, ok := addNonnegativeV2(b.snapshot.EstimatedInputTokens, estimatedInput)
	if !ok {
		return ReservationV2{}, fmt.Errorf("cumulative input estimate overflow")
	}
	newReservedOutput, ok := addNonnegativeV2(b.snapshot.ReservedOutputTokens, maxOutput)
	if !ok {
		return ReservationV2{}, fmt.Errorf("cumulative output reservation overflow")
	}
	newRequests, ok := addNonnegativeV2(b.snapshot.Requests, 1)
	if !ok {
		return ReservationV2{}, fmt.Errorf("model request counter overflow")
	}
	newPlanningPasses := b.snapshot.PlanningPasses
	newRepairs := b.snapshot.Repairs
	if kind == budgetKindPlanningV2 {
		newPlanningPasses, ok = addNonnegativeV2(newPlanningPasses, 1)
		if !ok {
			return ReservationV2{}, fmt.Errorf("planning pass counter overflow")
		}
	}
	if kind == budgetKindRepairV2 {
		newRepairs, ok = addNonnegativeV2(newRepairs, 1)
		if !ok {
			return ReservationV2{}, fmt.Errorf("repair counter overflow")
		}
	}
	if b.sequence == math.MaxUint64 {
		return ReservationV2{}, fmt.Errorf("reservation identifier counter overflow")
	}
	id := fmt.Sprintf("budget-v2-%d", b.sequence+1)
	b.sequence++
	b.snapshot.Requests = newRequests
	b.snapshot.PlanningPasses = newPlanningPasses
	b.snapshot.Repairs = newRepairs
	b.snapshot.ReservedTokens = newReserved
	b.snapshot.EstimatedInputTokens = newEstimatedInput
	b.snapshot.ReservedOutputTokens = newReservedOutput
	b.pending[id] = budgetReservationV2{kind: kind, reserved: reserve}
	return ReservationV2{ID: id, Kind: kind, EstimatedInput: estimatedInput, MaxOutput: maxOutput}, nil
}

func (b *taskBudgetV2) Reconcile(id string, completion CompletionV2) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.reconciled[id]; ok {
		return ErrDuplicateReconcileV2
	}
	reservation, ok := b.pending[id]
	if !ok || id == "" {
		return ErrInvalidReservationV2
	}
	if err := validateCompletionUsageV2(completion); err != nil {
		return err
	}
	usageTotals, err := sumUsageV2(b.snapshot.ReportedUsage, completion.Usage)
	if err != nil {
		return err
	}

	newReserved := b.snapshot.ReservedTokens
	newUncertain := b.snapshot.UncertainRequests
	newOverrun := b.overrun
	switch completion.Transmission {
	case TransmissionNotSentV2:
		newReserved, ok = subtractNonnegativeV2(newReserved, reservation.reserved)
		if !ok {
			return fmt.Errorf("token reservation accounting underflow")
		}
	case TransmissionSentUnknownV2:
		newUncertain, ok = addNonnegativeV2(newUncertain, 1)
		if !ok {
			return fmt.Errorf("uncertain request counter overflow")
		}
	case TransmissionResponseReceivedV2:
		actual, known, totalErr := actualTokenUsageV2(completion.Usage)
		if totalErr != nil {
			return totalErr
		}
		if known {
			if actual > reservation.reserved {
				newOverrun = true
			}
			newReserved, ok = subtractNonnegativeV2(newReserved, reservation.reserved)
			if !ok {
				return fmt.Errorf("token reservation accounting underflow")
			}
			newReserved, ok = addNonnegativeV2(newReserved, actual)
			if !ok {
				return fmt.Errorf("reported token usage overflow")
			}
		}
		if !known || completion.Usage.BilledMicroUSD == nil {
			newUncertain, ok = addNonnegativeV2(newUncertain, 1)
			if !ok {
				return fmt.Errorf("uncertain request counter overflow")
			}
		}
	default:
		return fmt.Errorf("invalid transmission state %q", completion.Transmission)
	}

	b.snapshot.ReportedUsage = usageTotals
	b.overrun = newOverrun
	if usageTotals.BilledMicroUSD != nil {
		b.snapshot.Currency = "USD"
	}
	b.snapshot.ReservedTokens = newReserved
	b.snapshot.UncertainRequests = newUncertain
	delete(b.pending, id)
	b.reconciled[id] = struct{}{}
	return nil
}

func (b *taskBudgetV2) AdmitAction(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("admit browser action: nil context")
	}
	if err := b.contextError(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.contextError(ctx); err != nil {
		return err
	}
	if !b.now().Before(b.deadline) || b.snapshot.Actions >= b.limits.Actions {
		return ErrBudgetExhaustedV2
	}
	next, ok := addNonnegativeV2(b.snapshot.Actions, 1)
	if !ok {
		return fmt.Errorf("browser action counter overflow")
	}
	b.snapshot.Actions = next
	return nil
}

func (b *taskBudgetV2) contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !b.now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

func (b *taskBudgetV2) Snapshot() BudgetSnapshotV2 {
	b.mu.Lock()
	defer b.mu.Unlock()
	snapshot := b.snapshot
	snapshot.ReportedUsage = cloneUsageV2(snapshot.ReportedUsage)
	snapshot.ReservedMicroUSD = cloneInt64V2(snapshot.ReservedMicroUSD)
	snapshot.UnresolvedMicroUSD = cloneInt64V2(snapshot.UnresolvedMicroUSD)
	return snapshot
}

func validBudgetKindV2(kind string) bool {
	switch kind {
	case budgetKindPlanningV2, budgetKindRepairV2, budgetKindExtractionV2, budgetKindParseRetryV2:
		return true
	default:
		return false
	}
}

func addNonnegativeV2(a, c int64) (int64, bool) {
	if a < 0 || c < 0 || a > math.MaxInt64-c {
		return 0, false
	}
	return a + c, true
}

func subtractNonnegativeV2(a, c int64) (int64, bool) {
	if a < 0 || c < 0 || c > a {
		return 0, false
	}
	return a - c, true
}

func validateCompletionUsageV2(completion CompletionV2) error {
	switch completion.Transmission {
	case TransmissionNotSentV2, TransmissionSentUnknownV2, TransmissionResponseReceivedV2:
	default:
		return fmt.Errorf("invalid transmission state %q", completion.Transmission)
	}
	values := []*int64{
		completion.Usage.InputTokens,
		completion.Usage.OutputTokens,
		completion.Usage.TotalTokens,
		completion.Usage.ReasoningTokens,
		completion.Usage.CacheReadTokens,
		completion.Usage.CacheWriteTokens,
		completion.Usage.BilledMicroUSD,
	}
	for _, value := range values {
		if value != nil && *value < 0 {
			return fmt.Errorf("reported usage cannot be negative")
		}
		if value != nil && completion.Transmission != TransmissionResponseReceivedV2 {
			return fmt.Errorf("reported usage requires a received response")
		}
	}
	return nil
}

func actualTokenUsageV2(usage RequestUsageV2) (int64, bool, error) {
	if usage.TotalTokens != nil {
		return *usage.TotalTokens, true, nil
	}
	if usage.InputTokens == nil || usage.OutputTokens == nil {
		return 0, false, nil
	}
	total, ok := addNonnegativeV2(*usage.InputTokens, *usage.OutputTokens)
	if !ok {
		return 0, false, fmt.Errorf("reported input and output token sum overflow")
	}
	return total, true, nil
}

func sumUsageV2(current, added RequestUsageV2) (RequestUsageV2, error) {
	fields := []struct {
		name string
		dst  **int64
		a    *int64
	}{
		{"input_tokens", &current.InputTokens, added.InputTokens},
		{"output_tokens", &current.OutputTokens, added.OutputTokens},
		{"total_tokens", &current.TotalTokens, added.TotalTokens},
		{"reasoning_tokens", &current.ReasoningTokens, added.ReasoningTokens},
		{"cache_read_tokens", &current.CacheReadTokens, added.CacheReadTokens},
		{"cache_write_tokens", &current.CacheWriteTokens, added.CacheWriteTokens},
		{"billed_micro_usd", &current.BilledMicroUSD, added.BilledMicroUSD},
	}
	for _, field := range fields {
		if field.a == nil {
			continue
		}
		if *field.dst == nil {
			value := *field.a
			*field.dst = &value
			continue
		}
		total, ok := addNonnegativeV2(**field.dst, *field.a)
		if !ok {
			return RequestUsageV2{}, fmt.Errorf("reported %s total overflow", field.name)
		}
		value := total
		*field.dst = &value
	}
	return current, nil
}

func cloneUsageV2(usage RequestUsageV2) RequestUsageV2 {
	usage.InputTokens = cloneInt64V2(usage.InputTokens)
	usage.OutputTokens = cloneInt64V2(usage.OutputTokens)
	usage.TotalTokens = cloneInt64V2(usage.TotalTokens)
	usage.ReasoningTokens = cloneInt64V2(usage.ReasoningTokens)
	usage.CacheReadTokens = cloneInt64V2(usage.CacheReadTokens)
	usage.CacheWriteTokens = cloneInt64V2(usage.CacheWriteTokens)
	usage.BilledMicroUSD = cloneInt64V2(usage.BilledMicroUSD)
	return usage
}
