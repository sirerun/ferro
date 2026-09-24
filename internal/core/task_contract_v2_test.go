package core

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestLimitsV2DefaultsAndBounds(t *testing.T) {
	defaults := DefaultLimitsV2()
	if defaults.RuntimeMS != 90000 || defaults.ModelRequests != 3 || defaults.TotalReservedTokens != 24000 {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}

	dimensions := []struct {
		name string
		set  func(*LimitsV2, int64)
	}{
		{"runtime_ms", func(l *LimitsV2, v int64) { l.RuntimeMS = v }},
		{"actions", func(l *LimitsV2, v int64) { l.Actions = v }},
		{"model_requests", func(l *LimitsV2, v int64) { l.ModelRequests = v }},
		{"repairs", func(l *LimitsV2, v int64) { l.Repairs = v }},
		{"planning_passes", func(l *LimitsV2, v int64) { l.PlanningPasses = v }},
		{"max_output_tokens", func(l *LimitsV2, v int64) { l.MaxOutputTokens = v }},
		{"max_input_tokens", func(l *LimitsV2, v int64) { l.MaxInputTokens = v }},
		{"total_reserved_tokens", func(l *LimitsV2, v int64) { l.TotalReservedTokens = v }},
	}
	for _, dimension := range dimensions {
		t.Run(dimension.name, func(t *testing.T) {
			for _, value := range []int64{0, -1, math.MaxInt64} {
				limits := defaults
				dimension.set(&limits, value)
				if err := limits.ValidateV2(); err == nil {
					t.Errorf("ValidateV2 accepted %s=%d", dimension.name, value)
				}
			}
			limits := defaults
			dimension.set(&limits, 1)
			if err := limits.ValidateV2(); err != nil {
				t.Errorf("ValidateV2 rejected positive %s: %v", dimension.name, err)
			}
		})
	}
}

func TestLimitOverridesV2RequirePositiveTighteningAndValidService(t *testing.T) {
	defaults := DefaultLimitsV2()
	dimensions := []struct {
		name         string
		service      func(*LimitsV2, int64)
		request      func(*LimitOverridesV2, *int64)
		defaultValue int64
	}{
		{"runtime_ms", func(l *LimitsV2, v int64) { l.RuntimeMS = v }, func(o *LimitOverridesV2, v *int64) { o.RuntimeMS = v }, defaults.RuntimeMS},
		{"actions", func(l *LimitsV2, v int64) { l.Actions = v }, func(o *LimitOverridesV2, v *int64) { o.Actions = v }, defaults.Actions},
		{"model_requests", func(l *LimitsV2, v int64) { l.ModelRequests = v }, func(o *LimitOverridesV2, v *int64) { o.ModelRequests = v }, defaults.ModelRequests},
		{"repairs", func(l *LimitsV2, v int64) { l.Repairs = v }, func(o *LimitOverridesV2, v *int64) { o.Repairs = v }, defaults.Repairs},
		{"planning_passes", func(l *LimitsV2, v int64) { l.PlanningPasses = v }, func(o *LimitOverridesV2, v *int64) { o.PlanningPasses = v }, defaults.PlanningPasses},
		{"max_output_tokens", func(l *LimitsV2, v int64) { l.MaxOutputTokens = v }, func(o *LimitOverridesV2, v *int64) { o.MaxOutputTokens = v }, defaults.MaxOutputTokens},
		{"max_input_tokens", func(l *LimitsV2, v int64) { l.MaxInputTokens = v }, func(o *LimitOverridesV2, v *int64) { o.MaxInputTokens = v }, defaults.MaxInputTokens},
		{"total_reserved_tokens", func(l *LimitsV2, v int64) { l.TotalReservedTokens = v }, func(o *LimitOverridesV2, v *int64) { o.TotalReservedTokens = v }, defaults.TotalReservedTokens},
	}
	for _, dimension := range dimensions {
		t.Run(dimension.name, func(t *testing.T) {
			service := defaults
			serviceLimit := dimension.defaultValue - 1
			if serviceLimit < 1 {
				serviceLimit = dimension.defaultValue
			}
			dimension.service(&service, serviceLimit)
			for _, value := range []int64{0, -1, serviceLimit + 1, math.MaxInt64} {
				override := LimitOverridesV2{}
				candidate := value
				dimension.request(&override, &candidate)
				if _, err := override.ApplyV2(service); err == nil {
					t.Errorf("accepted invalid %s override %d", dimension.name, value)
				}
			}
			positive := serviceLimit - 1
			if positive < 1 {
				positive = 1
			}
			override := LimitOverridesV2{}
			dimension.request(&override, &positive)
			out, err := override.ApplyV2(service)
			if err != nil {
				t.Fatalf("valid tightening rejected: %v", err)
			}
			got := defaults
			dimension.service(&got, positive)
			if out != got {
				t.Fatalf("override/inherited result = %+v, want %+v", out, got)
			}
			tooHighService := defaults
			dimension.service(&tooHighService, dimension.defaultValue+1)
			if _, err := override.ApplyV2(tooHighService); err == nil {
				t.Fatal("service value above default maximum accepted")
			}
		})
	}
	invalidService := defaults
	invalidService.RuntimeMS = 0
	valid := int64(5)
	if _, err := (LimitOverridesV2{Actions: &valid}).ApplyV2(invalidService); err == nil {
		t.Fatal("invalid service limit was hidden by override")
	}
}

func TestLimitOverridesV2ReserveCopyAndNoWidening(t *testing.T) {
	serviceReserve := int64(100)
	service := DefaultLimitsV2()
	service.ReserveMicroUSD = &serviceReserve
	inherited, err := (LimitOverridesV2{}).ApplyV2(service)
	if err != nil {
		t.Fatal(err)
	}
	serviceReserve = 90
	if *inherited.ReserveMicroUSD != 100 {
		t.Fatal("inherited reserve aliases service pointer")
	}
	serviceReserve = 100
	requestReserve := int64(120)
	if _, err := (LimitOverridesV2{ReserveMicroUSD: &requestReserve}).ApplyV2(service); err == nil {
		t.Fatal("request widened service reserve")
	}

	requestReserve = 80
	out, err := (LimitOverridesV2{ReserveMicroUSD: &requestReserve}).ApplyV2(service)
	if err != nil {
		t.Fatal(err)
	}
	requestReserve = 60
	if *out.ReserveMicroUSD != 80 {
		t.Fatal("result reserve aliases request pointer")
	}
	serviceReserve = 50
	if *out.ReserveMicroUSD != 80 {
		t.Fatal("result reserve aliases service pointer")
	}
}

func TestLimitOverridesV2CannotDisableHardDollarRejection(t *testing.T) {
	service := DefaultLimitsV2()
	service.HardDollar = true
	falseValue := false
	if _, err := (LimitOverridesV2{HardDollar: &falseValue}).ApplyV2(service); !errors.Is(err, ErrUnsupportedHardDollarV2) {
		t.Fatalf("hard-dollar rejection bypassed: %v", err)
	}
}

func TestUsageV2UnknownDiffersFromZeroAndSnapshotSerialization(t *testing.T) {
	var usage RequestUsageV2
	encoded, err := json.Marshal(usage)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "input_tokens") {
		t.Fatalf("unknown usage serialized as a value: %s", encoded)
	}
	zero := int64(0)
	usage.InputTokens = &zero
	encoded, err = json.Marshal(usage)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if value, ok := decoded["input_tokens"]; !ok || value != float64(0) {
		t.Fatalf("explicit zero was omitted: %s", encoded)
	}

	snapshotJSON, err := json.Marshal(BudgetSnapshotV2{})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot["reported_usage"]; !ok {
		t.Fatalf("snapshot must distinguish missing reported usage: %s", snapshotJSON)
	}
	reported, ok := snapshot["reported_usage"].(map[string]any)
	if !ok {
		t.Fatalf("reported usage must remain a typed usage object: %s", snapshotJSON)
	}
	if billed, exists := reported["billed_micro_usd"]; exists && billed != nil {
		t.Fatalf("missing reported money must remain nil: %s", snapshotJSON)
	}
}

func TestValidateSchemaShapeV2(t *testing.T) {
	valid := json.RawMessage(`{"type":"object","properties":{"x":{"type":"integer","minimum":1}},"required":["x"]}`)
	if err := ValidateSchemaShapeV2(valid); err != nil {
		t.Fatalf("valid schema rejected: %v", err)
	}

	tooDeep := `{"properties":{"a":{"properties":{"b":{"properties":{"c":{"properties":{"d":{"properties":{"e":{"properties":{"f":{"properties":{"g":{"properties":{"h":{"properties":{"i":{"properties":{"j":{"properties":{"k":{"properties":{"l":{"properties":{"m":{"properties":{"n":{"properties":{"o":{"properties":{"p":{"type":"string"}}}}}}}}}}}}}}}}}}}}}}}}}`
	tooLarge := `{"description":"` + strings.Repeat("x", 32768) + `"}`
	for _, tc := range []struct {
		name string
		raw  json.RawMessage
	}{
		{"empty", json.RawMessage(`{}`)},
		{"array", json.RawMessage(`[]`)},
		{"null", json.RawMessage(`null`)},
		{"invalid utf8", json.RawMessage([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'})},
		{"trailing value", json.RawMessage(`{"type":"string"} {}`)},
		{"malformed", json.RawMessage(`{"type":`)},
		{"unknown keyword", json.RawMessage(`{"$ref":"x"}`)},
		{"malformed keyword type", json.RawMessage(`{"required":"x"}`)},
		{"schema child array", json.RawMessage(`{"properties":{"x":[]}}`)},
		{"depth overflow", json.RawMessage(tooDeep)},
		{"size overflow", json.RawMessage(tooLarge)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateSchemaShapeV2(tc.raw); err == nil {
				t.Fatal("invalid schema shape accepted")
			}
		})
	}
}
