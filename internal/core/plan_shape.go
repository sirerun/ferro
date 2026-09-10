package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ErrPlanShape identifies malformed model output and the offending JSON path.
type ErrPlanShape struct{ Path, Reason string }

func (e *ErrPlanShape) Error() string { return "plan shape " + e.Path + ": " + e.Reason }

// SchemaCompleter is optional. Plain Complete clients still get local validation.
type SchemaCompleter interface {
	CompleteSchema(ctx context.Context, system, user string, schema json.RawMessage) (string, error)
}

var actionFields = map[ActionKind][]string{
	KindGoto: {"url"}, KindClick: {"ref"}, KindFill: {"ref", "text", "secret"}, KindSelect: {"ref", "value"},
	KindKey: {"text"}, KindScroll: {"to"}, KindWait: {"for"}, KindExtract: {"schema", "fields"},
	KindPlanAgain: {"reason"}, KindDone: {"result"},
}

// PlanSchema returns the JSON schema constraining planner and repair output,
// for callers that want to inspect it or pass it to their own SchemaCompleter.
func PlanSchema() json.RawMessage { return planSchema() }

// planSchema is generated from the same field vocabulary the local validator uses.
// Local action validation adds required-field and kind-specific checks.
func planSchema() json.RawMessage {
	variants := []any{}
	for _, kind := range []ActionKind{KindGoto, KindClick, KindFill, KindSelect, KindKey, KindScroll, KindWait, KindExtract, KindPlanAgain, KindDone} {
		props := map[string]any{"kind": map[string]any{"type": "string", "enum": []ActionKind{kind}}}
		for _, field := range actionFields[kind] {
			def := map[string]any{}
			switch field {
			case "ref":
				def["type"] = "integer"
			case "secret":
				def["type"] = "boolean"
			case "schema", "fields":
				def["type"] = "object"
			case "result":
			default:
				def["type"] = "string"
			}
			props[field] = def
		}
		variants = append(variants, map[string]any{"type": "object", "properties": props, "required": []string{"kind"}, "additionalProperties": false})
	}
	b, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{"steps": map[string]any{"type": "array", "minItems": 1, "maxItems": MaxPlanSteps, "items": map[string]any{"anyOf": variants}}, "reasoning": map[string]any{"type": "string"}}, "required": []string{"steps"}, "additionalProperties": false})
	return b
}
func stripFence(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
	}
	return s
}
func cleanJSON(raw string) string {
	s := stripFence(raw)
	if i := strings.Index(s, "{"); i > 0 {
		s = s[i:]
	}
	return s
}
func decodeStrict(raw string, v any) error {
	d := json.NewDecoder(bytes.NewBufferString(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}
func parseAction(raw string, allowAbort bool) (Action, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(cleanJSON(raw)), &fields); err != nil {
		return Action{}, &ErrPlanShape{"$", err.Error()}
	}
	var a Action
	if err := decodeStrict(cleanJSON(raw), &a); err != nil {
		return a, &ErrPlanShape{"$", err.Error()}
	}
	allowed, ok := actionFields[a.Kind]
	if allowAbort && a.Kind == "abort" {
		allowed = []string{"reason"}
		ok = true
	}
	if !ok {
		return a, &ErrPlanShape{"$.kind", "unknown or missing kind"}
	}
	for field := range fields {
		found := field == "kind"
		for _, key := range allowed {
			if field == key {
				found = true
			}
		}
		if !found {
			return a, &ErrPlanShape{"$." + field, "not allowed for " + string(a.Kind)}
		}
	}
	if a.Kind != "abort" {
		if err := validateAction(a); err != nil {
			return a, &ErrPlanShape{"$", err.Error()}
		}
	}
	return a, nil
}
func parsePlan(raw string) (*Plan, error) {
	var envelope struct {
		Steps     []json.RawMessage `json:"steps"`
		Reasoning string            `json:"reasoning,omitempty"`
	}
	if err := decodeStrict(cleanJSON(raw), &envelope); err != nil {
		return nil, &ErrPlanShape{"$", err.Error()}
	}
	if len(envelope.Steps) == 0 {
		return nil, &ErrPlanShape{"$.steps", "expected nonempty steps array"}
	}
	p := &Plan{Reasoning: envelope.Reasoning}
	for i, raw := range envelope.Steps {
		a, err := parseAction(string(raw), false)
		if err != nil {
			return nil, &ErrPlanShape{fmt.Sprintf("$.steps[%d]", i), err.Error()}
		}
		p.Steps = append(p.Steps, a)
	}
	if err := p.Validate(); err != nil {
		return nil, &ErrPlanShape{"$.steps", err.Error()}
	}
	return p, nil
}
