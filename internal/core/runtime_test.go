package core

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type replyLLM string

func (r replyLLM) Complete(context.Context, string, string) (string, error) { return string(r), nil }

func TestCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	key := CacheKey{Host: "example.test", Kind: "fill", Signature: "search"}
	c := NewResolutionCache(path)
	c.Put(key, "input[name=q]")
	if err := c.Flush(); err != nil {
		t.Fatal(err)
	}
	reloaded := NewResolutionCache(path)
	reloaded.Load()
	if got, ok := reloaded.Get(key); !ok || got != "input[name=q]" {
		t.Fatalf("cache lost: %q, %v", got, ok)
	}
}

func TestRepairBareAction(t *testing.T) {
	r := Runner{LLM: replyLLM("```json\n{\"kind\":\"click\",\"ref\":2}\n```")}
	s := &Snapshot{Elements: []Element{{Ref: 2, Tag: "button"}}}
	a, ok, err := r.repairStep(context.Background(), &RunError{Action: Action{Kind: KindClick, Ref: 1}}, s, s, nil)
	if err != nil || !ok || a.Ref != 2 {
		t.Fatalf("bare repair failed: %+v %v %v", a, ok, err)
	}
}

func TestParsePlanRejectsEnvelopeDrift(t *testing.T) {
	for _, raw := range []string{`{"actions":[]}`, `{"plan":{"steps":[]}}`, `{"steps":[{"kind":"click","ref":1,"unexpected":true}]}`} {
		if _, err := parsePlan(raw); err == nil {
			t.Errorf("accepted invalid shape %s", raw)
		}
	}
}

func TestSchemaValidation(t *testing.T) {
	cases := []struct {
		name, schema, value string
		valid               bool
	}{
		{"object", `{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"],"additionalProperties":false}`, `{"n":2}`, true},
		{"wrong type", `{"type":"integer"}`, `2.5`, false},
		{"required", `{"required":["n"]}`, `{}`, false},
		{"extra", `{"additionalProperties":false}`, `{"x":1}`, false},
		{"array", `{"type":"array","items":{"type":"boolean"},"minItems":1}`, `[true]`, true},
		{"nested", `{"type":"array","items":{"type":"boolean"}}`, `[1]`, false},
		{"enum", `{"enum":["a","b"]}`, `"c"`, false},
		{"unsupported", `{"$ref":"#/foo"}`, `{}`, false},
		{"malformed required", `{"required":[42]}`, `{}`, false},
		{"invalid type", `{"type":[]}`, `{}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(tc.value), &value); err != nil {
				t.Fatal(err)
			}
			err := validateSchema(json.RawMessage(tc.schema), value)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}

func TestReplayIsolationAndExpiry(t *testing.T) {
	c := NewResolutionCache("")
	p := &Plan{Steps: []Action{{Kind: KindDone, Result: "ok"}}}
	s := &Snapshot{URL: "https://example.test", Elements: []Element{{Ref: 1, Tag: "input", Name: "q"}}}
	task := Task{ReplayKey: "same", Goal: "one"}
	key := replayID(task, s)
	c.putPlan(key, p)
	got := c.getPlan(key)
	if got == nil {
		t.Fatal("missing plan")
	}
	got.Steps[0].Result = "changed"
	if c.getPlan(key).Steps[0].Result != "ok" {
		t.Fatal("plan aliases shared memory")
	}
	task.Goal = "two"
	if replayID(task, s) == key {
		t.Fatal("goal collision")
	}
	task.Goal = "one"
	s.Elements[0].Ref = 2
	if replayID(task, s) == key {
		t.Fatal("ref drift not detected")
	}
	c.plans[key] = replayEntry{Plan: *p, LastUsed: time.Now().Add(-31 * 24 * time.Hour)}
	if c.getPlan(key) != nil {
		t.Fatal("expired plan replayed")
	}
}

func TestSecretPlanNeverCached(t *testing.T) {
	c := NewResolutionCache("")
	c.putPlan("secret", &Plan{Steps: []Action{{Kind: KindFill, Ref: 1, Text: "password", Secret: true}, {Kind: KindDone}}})
	if c.getPlan("secret") != nil {
		t.Fatal("secret persisted")
	}
}

func TestTemplatesPreserveData(t *testing.T) {
	a := Action{Text: "value={{extract.last}}"}
	if err := expandTemplates(&a, extractStore{"last": "{{literal}}"}); err != nil {
		t.Fatal(err)
	}
	if a.Text != "value={{literal}}" {
		t.Fatalf("rewrote data: %q", a.Text)
	}
	if _, err := shapeResult("{{extract.missing}}", extractStore{}); err == nil {
		t.Fatal("missing template silently accepted")
	}
}

// TestShapeResult_ExpandsExtractLastField is a regression test for the
// planner prompt fix in runner.go's plan(): the prompt's only worked example
// used to show a hardcoded done.result literal and never demonstrated
// {{extract.last.<field>}}, so the model never learned to relay extracted
// data through done — it wrote descriptive prose instead. This proves the
// underlying mechanism the corrected prompt now teaches actually works.
func TestShapeResult_ExpandsExtractLastField(t *testing.T) {
	ex := extractStore{"last": map[string]any{"price": "$9.00"}}
	got, err := shapeResult("{{extract.last.price}}", ex)
	if err != nil {
		t.Fatal(err)
	}
	if got != "$9.00" {
		t.Errorf("shapeResult = %v, want %q", got, "$9.00")
	}
}

// TestShapeResult_ExpandsExtractLastWhole covers the no-field form
// {{extract.last}}, which relays the entire prior extract result.
func TestShapeResult_ExpandsExtractLastWhole(t *testing.T) {
	ex := extractStore{"last": "reply text pulled from the page"}
	got, err := shapeResult("{{extract.last}}", ex)
	if err != nil {
		t.Fatal(err)
	}
	if got != "reply text pulled from the page" {
		t.Errorf("shapeResult = %v, want the raw extract value", got)
	}
}

// TestShapeResult_NoTemplateLeavesResultUntouched guards the common case: a
// literal done.result with no template syntax passes through unchanged.
func TestShapeResult_NoTemplateLeavesResultUntouched(t *testing.T) {
	got, err := shapeResult("searched", extractStore{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "searched" {
		t.Errorf("shapeResult = %v, want %q", got, "searched")
	}
}

func TestStructureArray(t *testing.T) {
	r := Runner{LLM: replyLLM("```json\n{\"result\":[{\"n\":1}]}\n```")}
	m := RunMetrics{}
	result, err := r.structure(context.Background(), &structureRequest{Text: "one", Schema: json.RawMessage(`{"type":"array","items":{"type":"object"}}`)}, &m)
	if err != nil || len(result.([]any)) != 1 || m.LLMCalls != 1 {
		t.Fatalf("%v %+v %v", result, m, err)
	}
}

func TestPlannerRetryBounded(t *testing.T) {
	r := Runner{LLM: replyLLM(`{"actions":[]}`)}
	m := RunMetrics{}
	_, err := r.plan(context.Background(), "goal", &Snapshot{}, &m)
	var shape *ErrPlanShape
	if !errors.As(err, &shape) || m.LLMCalls != 2 || m.PlannerRetries != 1 {
		t.Fatalf("unbounded or untyped: %+v %v", m, err)
	}
}
