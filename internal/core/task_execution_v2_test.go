package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type metadataClientSpyV2 struct {
	calls          int
	completion     CompletionV2
	err            error
	systems, users []string
	texts          []string
}

func (s *metadataClientSpyV2) CompleteWithUsage(_ context.Context, system, user string) (CompletionV2, error) {
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

type budgetSpyV2 struct {
	kinds           []string
	inputs, outputs []int64
	reconciled      []CompletionV2
	admitErr        error
}

func (s *budgetSpyV2) Admit(_ context.Context, kind string, input, output int64) (ReservationV2, error) {
	s.kinds = append(s.kinds, kind)
	s.inputs = append(s.inputs, input)
	s.outputs = append(s.outputs, output)
	if s.admitErr != nil {
		return ReservationV2{}, s.admitErr
	}
	return ReservationV2{ID: "r", Kind: kind, EstimatedInput: input, MaxOutput: output}, nil
}
func (s *budgetSpyV2) Reconcile(_ string, c CompletionV2) error {
	s.reconciled = append(s.reconciled, c)
	return nil
}
func (*budgetSpyV2) AdmitAction(context.Context) error { return nil }
func (*budgetSpyV2) Snapshot() BudgetSnapshotV2        { return BudgetSnapshotV2{} }

func TestBudgetedClientV2ChargesKindsAndPromptRestrictions(t *testing.T) {
	provider := &metadataClientSpyV2{completion: CompletionV2{Text: "ok", Transmission: TransmissionResponseReceivedV2}}
	budget := &budgetSpyV2{}
	client, err := NewBudgetedClientV2(provider, budget, DefaultLimitsV2())
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{budgetKindPlanningV2, budgetKindParseRetryV2, budgetKindRepairV2, budgetKindExtractionV2} {
		if _, err = client.Complete(withBudgetRequestKindV2(context.Background(), kind), "sys", "page"); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(budget.kinds, ",") != "planning,parse_retry,repair,extraction" {
		t.Fatalf("kinds: %v", budget.kinds)
	}
	if provider.calls != 4 || len(budget.reconciled) != 4 {
		t.Fatalf("provider=%d reconciles=%d", provider.calls, len(budget.reconciled))
	}
	if budget.outputs[0] != DefaultLimitsV2().MaxOutputTokens || budget.inputs[0] < int64((len(provider.systems[0])+len(provider.users[0])+3)/4+32) {
		t.Fatalf("limits estimate mismatch: inputs=%v outputs=%v", budget.inputs, budget.outputs)
	}
	if !strings.Contains(provider.systems[0], "read_only") || !strings.Contains(provider.users[0], "untrusted") {
		t.Fatalf("missing restrictions: system=%q user=%q", provider.systems[0], provider.users[0])
	}
	if _, ok := client.(SchemaCompleter); ok {
		t.Fatal("budgeted adapter unexpectedly enables schema fallback")
	}
}

func TestBudgetedClientV2RejectsBeforeProviderAndReconcilesUnknown(t *testing.T) {
	provider := &metadataClientSpyV2{}
	budget := &budgetSpyV2{admitErr: ErrBudgetExhaustedV2}
	client, err := NewBudgetedClientV2(provider, budget, DefaultLimitsV2())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Complete(context.Background(), "s", "u")
	if !errors.Is(err, ErrBudgetExhaustedV2) || provider.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, provider.calls)
	}
	budget.admitErr = nil
	provider.err = errors.New("secret provider detail")
	provider.completion.Transmission = TransmissionSentUnknownV2
	_, err = client.Complete(context.Background(), "s", "u")
	var stop *StopError
	if !errors.As(err, &stop) || stop.Code != "provider_error" || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe provider error: %v", err)
	}
	if len(budget.reconciled) != 1 || budget.reconciled[0].Transmission != TransmissionSentUnknownV2 {
		t.Fatalf("reconcile=%+v", budget.reconciled)
	}
}

type blockedMetadataClientV2 struct {
	started chan struct{}
	release chan struct{}
	usage   RequestUsageV2
	err     error
}

func (c *blockedMetadataClientV2) CompleteWithUsage(ctx context.Context, _, _ string) (CompletionV2, error) {
	close(c.started)
	providerErr := c.err
	if providerErr == nil {
		<-ctx.Done()
		providerErr = ctx.Err()
	} else {
		<-c.release
	}
	return CompletionV2{
		Usage: c.usage, Transmission: TransmissionSentUnknownV2,
	}, fmt.Errorf("provider-secret-do-not-leak: %w", providerErr)
}

func TestBudgetedClientV2PropagatesCancellationAfterReconcile(t *testing.T) {
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
			provider := &blockedMetadataClientV2{started: make(chan struct{}), release: make(chan struct{}), usage: RequestUsageV2{InputTokens: &input, BilledMicroUSD: &billed}, err: tc.providerErr}
			budget := &budgetSpyV2{}
			client, err := NewBudgetedClientV2(provider, budget, DefaultLimitsV2())
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
			if usage.InputTokens == nil || *usage.InputTokens != 27 || usage.BilledMicroUSD == nil || *usage.BilledMicroUSD != 0 || budget.reconciled[0].Transmission != TransmissionSentUnknownV2 {
				t.Fatalf("cancellation usage was lost: %+v", budget.reconciled[0])
			}
		})
	}
}

func TestRunnerTagsPlanningRetryRepairAndExtractionRequests(t *testing.T) {
	provider := &metadataClientSpyV2{completion: CompletionV2{Transmission: TransmissionResponseReceivedV2}, texts: []string{
		"invalid", `{"steps":[{"kind":"done","result":"ok"}]}`,
		`{"kind":"wait","for":"dom_settle"}`, `{"result":"ok"}`,
	}}
	budget := &budgetSpyV2{}
	client, err := NewBudgetedClientV2(provider, budget, DefaultLimitsV2())
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
	provider := &metadataClientSpyV2{}
	budget := &budgetSpyV2{admitErr: ErrBudgetExhaustedV2}
	client, err := NewBudgetedClientV2(provider, budget, DefaultLimitsV2())
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{LLM: client, Executor: NewExecutor(WaitStrategy{}).WithDriver(driver), MaxRepairs: 1}
	plan := &Plan{Steps: []Action{{Kind: KindFill, Ref: 1, Text: "x"}, {Kind: KindDone, Result: "ok"}}}
	_, _, runErr := r.executeWithRepairs(context.Background(), context.Background(), plan, &Snapshot{}, 20, nil)
	if runErr == nil || !errors.Is(runErr, ErrBudgetExhaustedV2) || provider.calls != 0 {
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
			if got := freshReplayPlanEligibleV2(p); got != tc.want {
				t.Fatalf("eligible=%v want %v", got, tc.want)
			}
		})
	}
}

type freshReplayDriverV2 struct {
	PageDriver
	value string
}

func (*freshReplayDriverV2) Snapshot(context.Context, int) (*Snapshot, error) {
	return &Snapshot{URL: "https://fresh.test"}, nil
}
func (d *freshReplayDriverV2) ExtractField(context.Context, string) (string, error) {
	return d.value, nil
}

type replayPlanLLMV2 struct{ calls int }

func (l *replayPlanLLMV2) Complete(context.Context, string, string) (string, error) {
	l.calls++
	return `{"steps":[{"kind":"extract","fields":{"value":"#value"}},{"kind":"done","result":"{{extract.last.value}}"}]}`, nil
}

type literalPlanLLMV2 struct{ calls int }

func (l *literalPlanLLMV2) Complete(context.Context, string, string) (string, error) {
	l.calls++
	return `{"steps":[{"kind":"done","result":"literal-` + []string{"one", "two"}[l.calls-1] + `"}]}`, nil
}

func TestFreshReplayOnlyReplaysExtractionPlanWithCurrentValue(t *testing.T) {
	cache := NewResolutionCache("")
	driver := &freshReplayDriverV2{value: "first"}
	llm := &replayPlanLLMV2{}
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
	driver := &freshReplayDriverV2{}
	llm := &literalPlanLLMV2{}
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
