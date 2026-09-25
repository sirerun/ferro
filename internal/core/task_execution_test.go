package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type metadataClientSpy struct {
	calls          int
	completion     Completion
	err            error
	systems, users []string
	texts          []string
}

func (s *metadataClientSpy) CompleteWithUsage(_ context.Context, system, user string) (Completion, error) {
	s.calls++
	s.systems = append(s.systems, system)
	s.users = append(s.users, user)
	completion := s.completion
	if len(s.texts) > 0 {
		completion.Text = s.texts[0]
		s.texts = s.texts[1:]
	}
	return completion, s.err
}

type budgetSpy struct {
	kinds           []string
	inputs, outputs []int64
	reconciled      []Completion
	admitErr        error
}

func (s *budgetSpy) Admit(_ context.Context, kind string, input, output int64) (Reservation, error) {
	s.kinds = append(s.kinds, kind)
	s.inputs = append(s.inputs, input)
	s.outputs = append(s.outputs, output)
	if s.admitErr != nil {
		return Reservation{}, s.admitErr
	}
	return Reservation{ID: "r", Kind: kind, EstimatedInput: input, MaxOutput: output}, nil
}
func (s *budgetSpy) Reconcile(_ string, c Completion) error {
	s.reconciled = append(s.reconciled, c)
	return nil
}
func (*budgetSpy) AdmitAction(context.Context) error { return nil }
func (*budgetSpy) Snapshot() BudgetSnapshot          { return BudgetSnapshot{} }

func TestBudgetedClientChargesKindsAndPromptRestrictions(t *testing.T) {
	provider := &metadataClientSpy{completion: Completion{Text: "ok", Transmission: TransmissionResponseReceived}}
	budget := &budgetSpy{}
	client, err := NewBudgetedClient(provider, budget, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{budgetKindPlanning, budgetKindParseRetry, budgetKindRepair, budgetKindExtraction} {
		if _, err = client.Complete(withBudgetRequestKind(context.Background(), kind), "sys", "page"); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(budget.kinds, ",") != "planning,parse_retry,repair,extraction" {
		t.Fatalf("kinds: %v", budget.kinds)
	}
	if provider.calls != 4 || len(budget.reconciled) != 4 {
		t.Fatalf("provider=%d reconciles=%d", provider.calls, len(budget.reconciled))
	}
	if budget.outputs[0] != DefaultLimits().MaxOutputTokens || budget.inputs[0] < int64((len(provider.systems[0])+len(provider.users[0])+3)/4+32) {
		t.Fatalf("limits estimate mismatch: inputs=%v outputs=%v", budget.inputs, budget.outputs)
	}
	if !strings.Contains(provider.systems[0], "read_only") || !strings.Contains(provider.users[0], "untrusted") {
		t.Fatalf("missing restrictions: system=%q user=%q", provider.systems[0], provider.users[0])
	}
	if _, ok := client.(SchemaCompleter); ok {
		t.Fatal("budgeted adapter unexpectedly enables schema fallback")
	}
}

func TestBudgetedClientRejectsBeforeProviderAndReconcilesUnknown(t *testing.T) {
	provider := &metadataClientSpy{}
	budget := &budgetSpy{admitErr: ErrBudgetExhausted}
	client, err := NewBudgetedClient(provider, budget, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Complete(context.Background(), "s", "u")
	if !errors.Is(err, ErrBudgetExhausted) || provider.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, provider.calls)
	}
	budget.admitErr = nil
	provider.err = errors.New("secret provider detail")
	provider.completion.Transmission = TransmissionSentUnknown
	_, err = client.Complete(context.Background(), "s", "u")
	var stop *StopError
	if !errors.As(err, &stop) || stop.Code != "provider_error" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe provider error: %v", err)
	}
	if len(budget.reconciled) != 1 || budget.reconciled[0].Transmission != TransmissionSentUnknown {
		t.Fatalf("reconcile=%+v", budget.reconciled)
	}
}

type blockedMetadataClient struct {
	started chan struct{}
	release chan struct{}
	usage   RequestUsage
	err     error
}

func (c *blockedMetadataClient) CompleteWithUsage(ctx context.Context, _, _ string) (Completion, error) {
	close(c.started)
	providerErr := c.err
	if providerErr == nil {
		<-ctx.Done()
		providerErr = ctx.Err()
	} else {
		<-c.release
	}
	return Completion{
		Usage: c.usage, Transmission: TransmissionSentUnknown,
	}, fmt.Errorf("provider-secret-do-not-leak: %w", providerErr)
}

func TestBudgetedClientPropagatesCancellationAfterReconcile(t *testing.T) {
	tests := []struct {
		name          string
		providerErr   error
		cancelContext bool
		want          error
	}{
		{
			name:          "caller cancellation",
			cancelContext: true,
			want:          context.Canceled,
		},
		{
			name:        "provider deadline exceeded",
			providerErr: context.DeadlineExceeded,
			want:        context.DeadlineExceeded,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			input, billed := int64(27), int64(0)
			provider := &blockedMetadataClient{started: make(chan struct{}), release: make(chan struct{}), usage: RequestUsage{InputTokens: &input, BilledMicroUSD: &billed}, err: tc.providerErr}
			budget := &budgetSpy{}
			client, err := NewBudgetedClient(provider, budget, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				_, callErr := client.Complete(ctx, "system", "user")
				result <- callErr
			}()
			<-provider.started
			if tc.cancelContext {
				cancel()
			} else {
				close(provider.release)
			}
			err = <-result
			if !errors.Is(err, tc.want) || strings.Contains(fmt.Sprint(err), "provider-secret-do-not-leak") {
				t.Fatalf("cancellation classification/error disclosure: err=%v", err)
			}
			if len(budget.reconciled) != 1 {
				t.Fatalf("reconciliations=%d, want exactly one", len(budget.reconciled))
			}
			usage := budget.reconciled[0].Usage
			if usage.InputTokens == nil || *usage.InputTokens != 27 || usage.BilledMicroUSD == nil || *usage.BilledMicroUSD != 0 || budget.reconciled[0].Transmission != TransmissionSentUnknown {
				t.Fatalf("cancellation usage was lost: %+v", budget.reconciled[0])
			}
		})
	}
}

func TestRunnerTagsPlanningRetryRepairAndExtractionRequests(t *testing.T) {
	provider := &metadataClientSpy{completion: Completion{Transmission: TransmissionResponseReceived}, texts: []string{
		"invalid", `{"steps":[{"kind":"done","result":"ok"}]}`,
		`{"kind":"wait","for":"dom_settle"}`, `{"result":"ok"}`,
	}}
	budget := &budgetSpy{}
	client, err := NewBudgetedClient(provider, budget, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{LLM: client}
	if _, err = r.plan(context.Background(), "goal", &Snapshot{}, nil); err != nil {
		t.Fatal(err)
	}
	rerr := &RunError{Action: Action{Kind: KindClick, Ref: 1}, Err: errors.New("stale ref")}
	if _, ok, err := r.repairStep(context.Background(), rerr, &Snapshot{}, &Snapshot{}, nil); err != nil || !ok {
		t.Fatalf("repair ok=%v err=%v", ok, err)
	}
	if _, err := r.structure(context.Background(), &structureRequest{Text: "page", Schema: []byte(`{"type":"object"}`)}, &RunMetrics{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"planning", "parse_retry", "repair", "extraction"}
	if strings.Join(budget.kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("request kinds=%v want=%v", budget.kinds, want)
	}
}

func TestRunnerPreservesBudgetFailureFromRepair(t *testing.T) {
	driver := &driverFixture{fail: errors.New("element vanished")}
	provider := &metadataClientSpy{}
	budget := &budgetSpy{admitErr: ErrBudgetExhausted}
	client, err := NewBudgetedClient(provider, budget, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{LLM: client, Executor: NewExecutor(WaitStrategy{}).WithDriver(driver), MaxRepairs: 1}
	plan := &Plan{Steps: []Action{{Kind: KindFill, Ref: 1, Text: "x"}, {Kind: KindDone, Result: "ok"}}}
	_, _, runErr := r.executeWithRepairs(context.Background(), context.Background(), plan, &Snapshot{}, 20, nil)
	if runErr == nil || !errors.Is(runErr, ErrBudgetExhausted) || provider.calls != 0 {
		t.Fatalf("runErr=%v calls=%d", runErr, provider.calls)
	}
}

func TestFreshReplayOnlyRequiresExtractTemplates(t *testing.T) {
	cases := []struct {
		name    string
		result  any
		extract bool
		want    bool
	}{
		{"fresh extract template", "{{extract.last.value}}", true, true},
		{"literal fact", "old page fact", true, false},
		{"mixed literal", map[string]any{"v": "{{extract.last.value}}", "old": "stale"}, true, false},
		{"missing extraction", "{{extract.last}}", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			steps := []Action{{Kind: KindDone, Result: tc.result}}
			if tc.extract {
				steps = append([]Action{{Kind: KindExtract, Fields: map[string]string{"value": "#v"}}}, steps...)
			}
			p := &Plan{Steps: steps}
			if got := freshReplayPlanEligible(p); got != tc.want {
				t.Fatalf("eligible=%v want %v", got, tc.want)
			}
		})
	}
}

type freshReplayDriver struct {
	PageDriver
	value string
}

func (*freshReplayDriver) Snapshot(context.Context, int) (*Snapshot, error) {
	return &Snapshot{URL: "https://fresh.test"}, nil
}
func (d *freshReplayDriver) ExtractField(context.Context, string) (string, error) {
	return d.value, nil
}

type replayPlanLLM struct{ calls int }

func (l *replayPlanLLM) Complete(context.Context, string, string) (string, error) {
	l.calls++
	return `{"steps":[{"kind":"extract","fields":{"value":"#value"}},{"kind":"done","result":"{{extract.last.value}}"}]}`, nil
}

type literalPlanLLM struct{ calls int }

func (l *literalPlanLLM) Complete(context.Context, string, string) (string, error) {
	l.calls++
	return `{"steps":[{"kind":"done","result":"literal-` + []string{"one", "two"}[l.calls-1] + `"}]}`, nil
}

func TestFreshReplayOnlyReplaysExtractionPlanWithCurrentValue(t *testing.T) {
	cache := NewResolutionCache("")
	driver := &freshReplayDriver{value: "first"}
	llm := &replayPlanLLM{}
	r := &Runner{LLM: llm, Executor: NewExecutor(WaitStrategy{}).WithDriver(driver).WithCache(cache)}
	task := Task{Goal: "report current value", ReplayKey: "stable", FreshReplayOnly: true}
	first, _, err := r.RunDriver(context.Background(), driver, 20, task)
	if err != nil || first != "first" {
		t.Fatalf("first=%v err=%v", first, err)
	}
	driver.value = "second"
	second, metrics, err := r.RunDriver(context.Background(), driver, 20, task)
	if err != nil || second != "second" || metrics.ReplayHits != 1 || llm.calls != 1 {
		t.Fatalf("second=%v metrics=%+v calls=%d err=%v", second, metrics, llm.calls, err)
	}
}

func TestFreshReplayOnlyDoesNotCacheLiteralDoneData(t *testing.T) {
	cache := NewResolutionCache("")
	driver := &freshReplayDriver{}
	llm := &literalPlanLLM{}
	r := &Runner{LLM: llm, Executor: NewExecutor(WaitStrategy{}).WithDriver(driver).WithCache(cache)}
	task := Task{Goal: "literal", ReplayKey: "literal", FreshReplayOnly: true}
	first, _, err := r.RunDriver(context.Background(), driver, 20, task)
	if err != nil || first != "literal-one" {
		t.Fatalf("first=%v err=%v", first, err)
	}
	second, metrics, err := r.RunDriver(context.Background(), driver, 20, task)
	if err != nil || second != "literal-two" || metrics.ReplayHits != 0 || llm.calls != 2 {
		t.Fatalf("second=%v metrics=%+v calls=%d err=%v", second, metrics, llm.calls, err)
	}
}

func TestRunnerPreservesProviderDeadlineFromRepair(t *testing.T) {
	driver := &driverFixture{fail: errors.New("element vanished")}
	provider := &metadataClientSpy{err: fmt.Errorf("provider-private-detail: %w", context.DeadlineExceeded)}
	client, err := NewBudgetedClient(provider, &budgetSpy{}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{LLM: client, Executor: NewExecutor(WaitStrategy{}).WithDriver(driver), MaxRepairs: 2}
	plan := &Plan{Steps: []Action{{Kind: KindFill, Ref: 1, Text: "x"}, {Kind: KindDone, Result: "ok"}}}
	_, _, runErr := r.executeWithRepairs(context.Background(), context.Background(), plan, &Snapshot{URL: "https://fixture.test", Elements: []Element{{Ref: 1, Tag: "input", Name: "q"}}}, 20, nil)
	if runErr == nil || !errors.Is(runErr.Err, context.DeadlineExceeded) {
		t.Fatalf("provider deadline classification lost: runErr=%v", runErr)
	}
	if provider.calls != 1 || driver.fills != 1 {
		t.Fatalf("terminal provider deadline caused retry: provider calls=%d browser fills=%d", provider.calls, driver.fills)
	}
	if strings.Contains(runErr.Error(), "provider-private-detail") {
		t.Fatalf("provider detail leaked: %v", runErr)
	}
}

type cancellingRepairProvider struct{ cancel context.CancelFunc }

func (p cancellingRepairProvider) CompleteWithUsage(context.Context, string, string) (Completion, error) {
	p.cancel()
	return Completion{Transmission: TransmissionResponseReceived}, context.Canceled
}
func TestRunnerPreservesTaskCancellationDuringRepair(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	driver := &driverFixture{fail: errors.New("element vanished")}
	budget := &budgetSpy{}
	client, err := NewBudgetedClient(cancellingRepairProvider{cancel}, budget, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{LLM: client, Executor: NewExecutor(WaitStrategy{}).WithDriver(driver), MaxRepairs: 2}
	plan := &Plan{Steps: []Action{{Kind: KindFill, Ref: 1, Text: "x"}, {Kind: KindDone, Result: "ok"}}}
	_, _, runErr := r.executeWithRepairs(ctx, ctx, plan, &Snapshot{URL: "https://fixture.test", Elements: []Element{{Ref: 1, Tag: "input", Name: "q"}}}, 20, nil)
	if runErr == nil || !errors.Is(runErr.Err, context.Canceled) {
		t.Fatalf("task cancellation hidden by old browser failure: %v", runErr)
	}
	if driver.fills != 1 || len(budget.reconciled) != 1 {
		t.Fatalf("retry or missing reconciliation: fills=%d reconciled=%d", driver.fills, len(budget.reconciled))
	}
}
