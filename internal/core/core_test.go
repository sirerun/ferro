package core

import (
	"context"
	"testing"
)

// TestPlanValidation is a pure unit test, no browser required.
func TestPlanValidation(t *testing.T) {
	good := &Plan{Steps: []Action{
		{Kind: KindGoto, URL: "http://x"},
		{Kind: KindDone},
	}}
	if err := good.Validate(); err != nil {
		t.Errorf("good plan rejected: %v", err)
	}

	bad := []struct {
		name string
		p    *Plan
	}{
		{"empty", &Plan{}},
		{"done not last", &Plan{Steps: []Action{
			{Kind: KindDone}, {Kind: KindGoto, URL: "x"}}}},
		{"click no ref", &Plan{Steps: []Action{
			{Kind: KindClick}, {Kind: KindDone}}}},
		{"unknown kind", &Plan{Steps: []Action{
			{Kind: "teleport"}, {Kind: KindDone}}}},
	}
	for _, tc := range bad {
		if err := tc.p.Validate(); err == nil {
			t.Errorf("%s: expected error", tc.name)
		}
	}
}

// TestExecuteOne_RejectsControlSignals is a pure unit test (no browser):
// ExecuteOne is the entrypoint MCP primitive tools drive a single step
// through outside a full Plan, and done/plan_again are meaningless there.
func TestExecuteOne_RejectsControlSignals(t *testing.T) {
	x := NewExecutor(WaitStrategy{})
	for _, kind := range []ActionKind{KindDone, KindPlanAgain} {
		if _, err := x.ExecuteOne(context.Background(), nil, Action{Kind: kind}, map[string]any{}); err == nil {
			t.Errorf("ExecuteOne(%s) succeeded, want rejected as a control signal", kind)
		}
	}
}

// TestExecuteOne_ValidatesAction rejects a malformed action (missing
// required fields) the same way Plan.Validate would, before ever touching
// the browser.
func TestExecuteOne_ValidatesAction(t *testing.T) {
	x := NewExecutor(WaitStrategy{})
	if _, err := x.ExecuteOne(context.Background(), nil, Action{Kind: KindClick}, map[string]any{}); err == nil {
		t.Error("ExecuteOne(click with no ref) succeeded, want validation error")
	}
}

func TestParsePlan_Fences(t *testing.T) {
	raw := "```json\n{\"steps\":[{\"kind\":\"done\"}]}\n```"
	p, err := parsePlan(raw)
	if err != nil {
		t.Fatalf("fenced plan: %v", err)
	}
	if len(p.Steps) != 1 {
		t.Fatalf("want 1 step, got %d", len(p.Steps))
	}
}
