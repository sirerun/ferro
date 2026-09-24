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
