package core

import "testing"

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
