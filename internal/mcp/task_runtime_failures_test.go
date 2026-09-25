package mcp

import (
	"encoding/json"
	"testing"
)

func TestRuntime_PartialExtractionCannotHideBudgetExhaustion(t *testing.T) {
	o, d, calls := runtimeFixture(t, `{"steps":[{"kind":"extract","fields":{"a":"#value","b":"#value"}},{"kind":"done","result":"{{extract.last}}"}]}`)
	in := runtimeRequest("partial-budget")
	in.OutputSchema = json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"required":["a","b"],"additionalProperties":false}`)
	two := int64(2)
	in.Limits.Actions = &two
	result := callRuntime(t, o, in)
	if result.Status != TaskBudgetExhausted || len(result.Result) != 0 || d.calls["extract_field"] != 1 || calls.Load() != 1 {
		t.Fatalf("budget exhaustion hidden: %+v driver=%v provider=%d", result, d.calls, calls.Load())
	}
}

func TestRuntime_InputTokenCeilingIsBudgetExhaustion(t *testing.T) {
	o, _, calls := runtimeFixture(t, runtimePlan)
	in := runtimeRequest("input-ceiling")
	one := int64(1)
	in.Limits.MaxInputTokens = &one
	result := callRuntime(t, o, in)
	if result.Status != TaskBudgetExhausted || calls.Load() != 0 || result.Budget.Requests != 0 {
		t.Fatalf("wrong input ceiling outcome: %+v calls=%d", result, calls.Load())
	}
}

func TestRuntime_PlanningCeilingIsBudgetExhaustion(t *testing.T) {
	o, _, calls := runtimeFixture(t, `{"steps":[{"kind":"plan_again","reason":"refresh"}]}`)
	in := runtimeRequest("planning-ceiling")
	one := int64(1)
	in.Limits.PlanningPasses = &one
	result := callRuntime(t, o, in)
	if result.Status != TaskBudgetExhausted || calls.Load() != 1 || result.Budget.PlanningPasses != 1 {
		t.Fatalf("wrong planning ceiling outcome: %+v calls=%d", result, calls.Load())
	}
}
