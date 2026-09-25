package core

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

const (
	budgetKindPlanning   = "planning"
	budgetKindRepair     = "repair"
	budgetKindExtraction = "extraction"
	budgetKindParseRetry = "parse_retry"
)

type taskBudget struct {
	mu         sync.Mutex
	limits     Limits
	deadline   time.Time
	now        func() time.Time
	snapshot   BudgetSnapshot
	overrun    bool
	sequence   uint64
	pending    map[string]budgetReservation
	reconciled map[string]struct{}
}

type budgetReservation struct {
	kind     string
	reserved int64
}

// NewTaskBudget creates a request and action budget with a fixed deadline.
func NewTaskBudget(limits Limits, started time.Time) (BudgetController, error) {
	return newTaskBudget(limits, started, time.Now)
}

func newTaskBudget(limits Limits, started time.Time, now func() time.Time) (*taskBudget, error) {
	if limits.ReserveMicroUSD != nil {
		return nil, ErrUnsupportedCostReserve
	}
	if limits.HardDollar {
		return nil, ErrUnsupportedHardDollar
	}
	if err := limits.Validate(); err != nil {
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
	return &taskBudget{
		limits:     cloneLimits(limits),
		deadline:   deadline,
		now:        now,
		pending:    make(map[string]budgetReservation),
		reconciled: make(map[string]struct{}),
	}, nil
}

func (b *taskBudget) Admit(ctx context.Context, kind string, estimatedInput, maxOutput int64) (Reservation, error) {
	if ctx == nil {
		return Reservation{}, fmt.Errorf("admit model request: nil context")
	}
	if err := b.contextError(ctx); err != nil {
		return Reservation{}, err
	}
	if !validBudgetKind(kind) {
		return Reservation{}, fmt.Errorf("invalid model request kind %q", kind)
	}
	if estimatedInput < 0 {
		return Reservation{}, fmt.Errorf("estimated input tokens cannot be negative")
	}
	if estimatedInput > b.limits.MaxInputTokens {
		return Reservation{}, fmt.Errorf("%w: estimated input exceeds the per-request token ceiling", ErrBudgetExhausted)
	}
	if maxOutput <= 0 || maxOutput > b.limits.MaxOutputTokens {
		return Reservation{}, fmt.Errorf("maximum output tokens outside [1,%d]", b.limits.MaxOutputTokens)
	}
	reserve, ok := addNonnegative(estimatedInput, maxOutput)
	if !ok {
		return Reservation{}, fmt.Errorf("request token reservation overflow")
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.contextError(ctx); err != nil {
		return Reservation{}, err
	}
	if !b.now().Before(b.deadline) {
		return Reservation{}, ErrBudgetExhausted
	}
	if b.overrun {
		return Reservation{}, ErrBudgetExhausted
	}
	if b.snapshot.Requests >= b.limits.ModelRequests {
		return Reservation{}, ErrBudgetExhausted
	}
	if kind == budgetKindPlanning && b.snapshot.PlanningPasses >= b.limits.PlanningPasses {
		return Reservation{}, ErrBudgetExhausted
	}
	if kind == budgetKindRepair && b.snapshot.Repairs >= b.limits.Repairs {
		return Reservation{}, ErrBudgetExhausted
	}
	newReserved, ok := addNonnegative(b.snapshot.ReservedTokens, reserve)
	if !ok {
		return Reservation{}, fmt.Errorf("cumulative token reservation overflow")
	}
	if newReserved > b.limits.TotalReservedTokens {
		return Reservation{}, ErrBudgetExhausted
	}
	newEstimatedInput, ok := addNonnegative(b.snapshot.EstimatedInputTokens, estimatedInput)
	if !ok {
		return Reservation{}, fmt.Errorf("cumulative input estimate overflow")
	}
	newReservedOutput, ok := addNonnegative(b.snapshot.ReservedOutputTokens, maxOutput)
	if !ok {
		return Reservation{}, fmt.Errorf("cumulative output reservation overflow")
	}
	newRequests, ok := addNonnegative(b.snapshot.Requests, 1)
	if !ok {
		return Reservation{}, fmt.Errorf("model request counter overflow")
	}
	newPlanningPasses := b.snapshot.PlanningPasses
	newRepairs := b.snapshot.Repairs
	if kind == budgetKindPlanning {
		newPlanningPasses, ok = addNonnegative(newPlanningPasses, 1)
		if !ok {
			return Reservation{}, fmt.Errorf("planning pass counter overflow")
		}
	}
	if kind == budgetKindRepair {
		newRepairs, ok = addNonnegative(newRepairs, 1)
		if !ok {
			return Reservation{}, fmt.Errorf("repair counter overflow")
		}
	}
	if b.sequence == math.MaxUint64 {
		return Reservation{}, fmt.Errorf("reservation identifier counter overflow")
	}
	id := fmt.Sprintf("budget-v2-%d", b.sequence+1)
	b.sequence++
	b.snapshot.Requests = newRequests
	b.snapshot.PlanningPasses = newPlanningPasses
	b.snapshot.Repairs = newRepairs
	b.snapshot.ReservedTokens = newReserved
	b.snapshot.EstimatedInputTokens = newEstimatedInput
	b.snapshot.ReservedOutputTokens = newReservedOutput
	b.pending[id] = budgetReservation{kind: kind, reserved: reserve}
	return Reservation{ID: id, Kind: kind, EstimatedInput: estimatedInput, MaxOutput: maxOutput}, nil
}

func (b *taskBudget) Reconcile(id string, completion Completion) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.reconciled[id]; ok {
		return ErrDuplicateReconcile
	}
	reservation, ok := b.pending[id]
	if !ok || id == "" {
		return ErrInvalidReservation
	}
	if err := validateCompletionUsage(completion); err != nil {
		return err
	}
	usageTotals, err := sumUsage(b.snapshot.ReportedUsage, completion.Usage)
	if err != nil {
		return err
	}

	newReserved := b.snapshot.ReservedTokens
	newUncertain := b.snapshot.UncertainRequests
	newOverrun := b.overrun
	switch completion.Transmission {
	case TransmissionNotSent:
		newReserved, ok = subtractNonnegative(newReserved, reservation.reserved)
		if !ok {
			return fmt.Errorf("token reservation accounting underflow")
		}
	case TransmissionSentUnknown:
		newUncertain, ok = addNonnegative(newUncertain, 1)
		if !ok {
			return fmt.Errorf("uncertain request counter overflow")
		}
	case TransmissionResponseReceived:
		actual, known, totalErr := actualTokenUsage(completion.Usage)
		if totalErr != nil {
			return totalErr
		}
		if known {
			if actual > reservation.reserved {
				newOverrun = true
			}
			newReserved, ok = subtractNonnegative(newReserved, reservation.reserved)
			if !ok {
				return fmt.Errorf("token reservation accounting underflow")
			}
			newReserved, ok = addNonnegative(newReserved, actual)
			if !ok {
				return fmt.Errorf("reported token usage overflow")
			}
		}
		if !known || completion.Usage.BilledMicroUSD == nil {
			newUncertain, ok = addNonnegative(newUncertain, 1)
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

func (b *taskBudget) AdmitAction(ctx context.Context) error {
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
	if !b.now().Before(b.deadline) || b.overrun || b.snapshot.Actions >= b.limits.Actions {
		return ErrBudgetExhausted
	}
	next, ok := addNonnegative(b.snapshot.Actions, 1)
	if !ok {
		return fmt.Errorf("browser action counter overflow")
	}
	b.snapshot.Actions = next
	return nil
}

func (b *taskBudget) contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !b.now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

func (b *taskBudget) Snapshot() BudgetSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	snapshot := b.snapshot
	snapshot.ReportedUsage = cloneUsage(snapshot.ReportedUsage)
	snapshot.ReservedMicroUSD = cloneInt64(snapshot.ReservedMicroUSD)
	snapshot.UnresolvedMicroUSD = cloneInt64(snapshot.UnresolvedMicroUSD)
	return snapshot
}

func validBudgetKind(kind string) bool {
	switch kind {
	case budgetKindPlanning, budgetKindRepair, budgetKindExtraction, budgetKindParseRetry:
		return true
	default:
		return false
	}
}

func addNonnegative(a, c int64) (int64, bool) {
	if a < 0 || c < 0 || a > math.MaxInt64-c {
		return 0, false
	}
	return a + c, true
}

func subtractNonnegative(a, c int64) (int64, bool) {
	if a < 0 || c < 0 || c > a {
		return 0, false
	}
	return a - c, true
}

func validateCompletionUsage(completion Completion) error {
	switch completion.Transmission {
	case TransmissionNotSent, TransmissionSentUnknown, TransmissionResponseReceived:
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
		if value != nil && completion.Transmission != TransmissionResponseReceived {
			return fmt.Errorf("reported usage requires a received response")
		}
	}
	if completion.Transmission == TransmissionResponseReceived && completion.Usage.TotalTokens != nil {
		input, output := completion.Usage.InputTokens, completion.Usage.OutputTokens
		var minimum int64
		if input != nil {
			minimum = *input
		}
		if output != nil {
			if *output > minimum {
				minimum = *output
			}
		}
		if input != nil && output != nil {
			sum, ok := addNonnegative(*input, *output)
			if !ok {
				return fmt.Errorf("reported input and output token sum overflow")
			}
			minimum = sum
		}
		if *completion.Usage.TotalTokens < minimum {
			return fmt.Errorf("reported total tokens are below reported input/output")
		}
	}
	return nil
}

func actualTokenUsage(usage RequestUsage) (int64, bool, error) {
	if usage.TotalTokens != nil {
		return *usage.TotalTokens, true, nil
	}
	if usage.InputTokens == nil || usage.OutputTokens == nil {
		return 0, false, nil
	}
	total, ok := addNonnegative(*usage.InputTokens, *usage.OutputTokens)
	if !ok {
		return 0, false, fmt.Errorf("reported input and output token sum overflow")
	}
	return total, true, nil
}

func sumUsage(current, added RequestUsage) (RequestUsage, error) {
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
		total, ok := addNonnegative(**field.dst, *field.a)
		if !ok {
			return RequestUsage{}, fmt.Errorf("reported %s total overflow", field.name)
		}
		value := total
		*field.dst = &value
	}
	return current, nil
}

func cloneUsage(usage RequestUsage) RequestUsage {
	usage.InputTokens = cloneInt64(usage.InputTokens)
	usage.OutputTokens = cloneInt64(usage.OutputTokens)
	usage.TotalTokens = cloneInt64(usage.TotalTokens)
	usage.ReasoningTokens = cloneInt64(usage.ReasoningTokens)
	usage.CacheReadTokens = cloneInt64(usage.CacheReadTokens)
	usage.CacheWriteTokens = cloneInt64(usage.CacheWriteTokens)
	usage.BilledMicroUSD = cloneInt64(usage.BilledMicroUSD)
	return usage
}
