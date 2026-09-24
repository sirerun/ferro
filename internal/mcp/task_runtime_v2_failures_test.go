package mcp

import (
	"encoding/json"
	"testing"
)

func TestRuntimeV2_PartialExtractionCannotHideBudgetExhaustion(t *testing.T) {
	o, d, calls := runtimeFixtureV2(t, `{"steps":[{"kind":"extract","fields":{"a":"#value","b":"#value"}},{"kind":"done","result":"{{extract.last}}"}]}`)
	in := runtimeRequestV2("partial-budget")
	in.OutputSchema = json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"required":["a","b"],"additionalProperties":false}`)
	two := int64(2)
	in.Limits.Actions = &two
	result := callRuntimeV2(t, o, in)
	if result.Status != TaskBudgetExhaustedV2 || len(result.Result) != 0 || d.calls["extract_field"] != 1 || calls.Load() != 1 {
		t.Fatalf("budget exhaustion hidden: %+v driver=%v provider=%d", result, d.calls, calls.Load())
	}
}

func TestRuntimeV2_InputTokenCeilingIsBudgetExhaustion(t *testing.T) {
	o, _, calls := runtimeFixtureV2(t, runtimePlanV2)
	in := runtimeRequestV2("input-ceiling")
	one := int64(1)
	in.Limits.MaxInputTokens = &one
	result := callRuntimeV2(t, o, in)
	if result.Status != TaskBudgetExhaustedV2 || calls.Load() != 0 || result.Budget.Requests != 0 {
		t.Fatalf("wrong input ceiling outcome: %+v calls=%d", result, calls.Load())
	}
}

func TestRuntimeV2_PlanningCeilingIsBudgetExhaustion(t *testing.T) {
	o, _, calls := runtimeFixtureV2(t, `{"steps":[{"kind":"plan_again","reason":"refresh"}]}`)
	in := runtimeRequestV2("planning-ceiling")
	one := int64(1)
	in.Limits.PlanningPasses = &one
	result := callRuntimeV2(t, o, in)
	if result.Status != TaskBudgetExhaustedV2 || calls.Load() != 1 || result.Budget.PlanningPasses != 1 {
		t.Fatalf("wrong planning ceiling outcome: %+v calls=%d", result, calls.Load())
	}
}
