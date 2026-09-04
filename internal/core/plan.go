// Package core is ferro's engine: plan types, the page compiler, the
// deterministic executor, the planner/repair runner, and the resolution
// cache. It is unexported to users of the ferro module — the public surface
// lives in the top-level ferro.go facade.
package core

import (
	"encoding/json"
	"fmt"
)

// ActionKind enumerates the action set v0.1 supports.
type ActionKind string

const (
	KindGoto      ActionKind = "goto"
	KindClick     ActionKind = "click"
	KindFill      ActionKind = "fill"
	KindSelect    ActionKind = "select"
	KindKey       ActionKind = "key"
	KindScroll    ActionKind = "scroll"
	KindWait      ActionKind = "wait"
	KindExtract   ActionKind = "extract"
	KindPlanAgain ActionKind = "plan_again"
	KindDone      ActionKind = "done"
)

// Action is a single typed step in a Plan. The wire format is a tagged
// union: {"kind": "click", "ref": 3}.
type Action struct {
	Kind ActionKind `json:"kind"`

	// Navigation
	URL string `json:"url,omitempty"`

	// Element targeting (Ref is a page-snapshot element ID).
	Ref int `json:"ref,omitempty"`

	// Fill / Select / Key
	Text   string `json:"text,omitempty"`
	Value  string `json:"value,omitempty"`
	Secret bool   `json:"secret,omitempty"` // masked in logs

	// Scroll: "top" | "bottom" | a ref rendered as string
	To string `json:"to,omitempty"`

	// Wait: a CSS selector, "dom_settle", or a Go duration string ("2s")
	For string `json:"for,omitempty"`

	// Extract: JSON Schema describing the data to pull from the page.
	Schema json.RawMessage `json:"schema,omitempty"`

	// Fields: optional CSS selector per-field paths (pure JS, no LLM).
	// Schema is used as fallback when selectors aren't provided.
	Fields map[string]string `json:"fields,omitempty"`

	// Done: final result (matches the task's Schema, if any).
	Result any `json:"result,omitempty"`

	// PlanAgain: why the plan needs to be regenerated.
	Reason string `json:"reason,omitempty"`
}

// Plan is the LLM's compiled output. ExecutorRules constrain the plan at
// validation time (e.g. how far ahead the model may schedule after a
// navigation, which we cannot pre-validate).
type Plan struct {
	Reasoning string   `json:"reasoning,omitempty"`
	Steps     []Action `json:"steps"`
}

// Validate performs structural checks that don't require a live page.
// Ref resolution happens at execution time.
func (p *Plan) Validate() error {
	if len(p.Steps) == 0 {
		return fmt.Errorf("plan has no steps")
	}
	if len(p.Steps) > MaxPlanSteps {
		return fmt.Errorf("plan has %d steps, max is %d", len(p.Steps), MaxPlanSteps)
	}
	sawDone := false
	for i, a := range p.Steps {
		if err := validateAction(a); err != nil {
			return fmt.Errorf("step %d (%s): %w", i, a.Kind, err)
		}
		if a.Kind == KindDone {
			sawDone = true
			if i != len(p.Steps)-1 {
				return fmt.Errorf("step %d: done must be the last step", i)
			}
		}
		if sawDone && i != len(p.Steps)-1 {
			return fmt.Errorf("step %d: no steps may follow done", i)
		}
	}
	return nil
}

func validateAction(a Action) error {
	switch a.Kind {
	case KindGoto:
		if a.URL == "" {
			return fmt.Errorf("missing url")
		}
	case KindClick:
		if a.Ref <= 0 {
			return fmt.Errorf("missing ref")
		}
	case KindFill:
		if a.Ref <= 0 {
			return fmt.Errorf("missing ref")
		}
	case KindSelect:
		if a.Ref <= 0 || a.Value == "" {
			return fmt.Errorf("select needs ref and value")
		}
	case KindKey:
		if a.Text == "" {
			return fmt.Errorf("missing key sequence")
		}
	case KindScroll:
		if a.To == "" {
			return fmt.Errorf("scroll needs target (top|bottom|ref)")
		}
	case KindWait:
		if a.For == "" {
			return fmt.Errorf("wait needs a condition")
		}
	case KindExtract:
		if len(a.Schema) == 0 && len(a.Fields) == 0 {
			return fmt.Errorf("extract needs a schema or fields")
		}
		if len(a.Schema) > 0 && !json.Valid(a.Schema) {
			return fmt.Errorf("extract schema is not valid JSON")
		}
	case KindPlanAgain:
		if a.Reason == "" {
			return fmt.Errorf("plan_again needs a reason")
		}
	case KindDone:
		// Result is optional.
	default:
		return fmt.Errorf("unknown kind %q", a.Kind)
	}
	return nil
}

const MaxPlanSteps = 30
