package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// LLMClient is the only model interface ferro needs. Any OpenAI-compatible
// endpoint satisfies this with a ~60-line adapter (see internal/llm).
type LLMClient interface {
	// Complete returns raw text for a chat-style prompt. Implementations
	// should request JSON-constrained decoding where available.
	Complete(ctx context.Context, system, user string) (string, error)
}

// Runner ties planner → executor → repairer together. It holds no per-task
// state and is safe for concurrent use across a browser pool.
type Runner struct {
	LLM        LLMClient
	Executor   *Executor
	MaxRepairs int // default 2 (RFC §4.4)
}

func (r *Runner) defaults() {
	if r.Executor == nil {
		r.Executor = NewExecutor(WaitStrategy{})
	}
	if r.MaxRepairs == 0 {
		r.MaxRepairs = 2
	}
}

// Task is one unit of automation work.
type Task struct {
	Goal string
	// StartURL optionally seeds navigation. If empty, the planner is expected
	// to emit Goto as its first step (or the current page is already correct).
	StartURL string
	// ReplayKey, when set, lets the runner persist and replay validated plans
	// across sessions (see cache.go); zero LLM calls on a warm hit.
	ReplayKey string
	// Schema optionally constrains the shape of the Done result.
	Schema json.RawMessage
	// MaxPlannings bounds PlanAgain chains. Default 3.
	MaxPlannings int
}

// Run executes the task, returning the result plus RunMetrics describing
// what it cost (LLM calls, repairs, replans, estimated tokens).
func (r *Runner) Run(ctx context.Context, execCtx BrowserContext, t Task) (any, RunMetrics, error) {
	r.defaults()
	start := time.Now()
	var m RunMetrics
	if t.MaxPlannings == 0 {
		t.MaxPlannings = 3
	}
	if t.StartURL != "" {
		if err := execCtx.Navigate(t.StartURL); err != nil {
			m.Duration = time.Since(start)
			return nil, m, fmt.Errorf("start url: %w", err)
		}
	}

	for {
		// 1. Compile the page. Must use execCtx.CDP() — the chromedp-wrapped
		// tab context — not the plain caller ctx, or chromedp.Run fails with
		// "invalid context".
		snap, err := TakeSnapshot(execCtx.CDP(), execCtx.SnapshotMaxElements())
		if err != nil {
			m.Duration = time.Since(start)
			return nil, m, err
		}

		// 2. One planning LLM call.
		plan, err := r.plan(ctx, t.Goal, snap, &m)
		if err != nil {
			m.Duration = time.Since(start)
			return nil, m, fmt.Errorf("planning: %w", err)
		}

		// 3. Mechanical execute, with repair loop.
		result, extracted, rerr := r.executeWithRepairs(ctx, execCtx, plan, snap, &m)
		if rerr == nil {
			m.Duration = time.Since(start)
			return shapeResult(result, extracted), m, nil
		}

		// 4. Failure triage: PlanAgain or a plan that ran out without a done
		//    step (eof) → replan; anything else → abort after repair is
		//    exhausted (repairs already happened inside executeWithRepairs).
		//    eof is a common, recoverable model slip (forgetting the
		//    trailing done step) — treating it as fatal instead of
		//    replanning made every run non-deterministic even on an
		//    otherwise-correct plan.
		if rerr.Action.Kind == KindPlanAgain || rerr.Action.Kind == "eof" {
			m.Plannings++
			if m.Plannings >= t.MaxPlannings {
				m.Duration = time.Since(start)
				return nil, m, fmt.Errorf("replan limit reached: %s", rerr.Err)
			}
			continue // fresh snapshot, fresh plan
		}
		m.Duration = time.Since(start)
		m.ErrorClass = string(Classify(rerr))
		return nil, m, rerr
	}
}

// plan makes the single structured LLM call. The prompt is deliberately
// minimal: role, action vocabulary, snapshot, goal. No few-shot examples by
// default (configurable later) — modern models handle this schema fine and
// tokens are the budget.
func (r *Runner) plan(ctx context.Context, goal string, snap *Snapshot, m *RunMetrics) (*Plan, error) {
	system := `You are a browser automation planner. Output ONLY a JSON plan.

Top-level shape (exactly this envelope, no other keys):
  {"steps": [ <step>, <step>, ... ]}

Each <step> is a single flat object — "kind" plus that kind's fields, never
nested under the kind name:
  {"kind": "fill", "ref": 2, "text": "coffee"}
Fields per kind:
  goto:    {"url": "..."}
  click:   {"ref": <int>}
  fill:    {"ref": <int>, "text": "...", "secret": bool}
  select:  {"ref": <int>, "value": "..."}   (matches option text or value)
  key:     {"text": "Enter"}                 (key name sequence)
  scroll:  {"to": "top"|"bottom"}
  wait:    {"for": "dom_settle"|"2s"|"<css selector>"}
  extract: {"schema": <json schema>}  or  {"fields": {"name": "<css selector>"}}
  plan_again: {"reason": "..."}  (page state differs from expectation; triggers replan)
  done:    {"result": <final answer matching the user's requested shape>}

Worked example — goal "search for coffee", page has a search input [2] and a
Go button [3]:
  {"steps": [
    {"kind": "fill", "ref": 2, "text": "coffee"},
    {"kind": "click", "ref": 3},
    {"kind": "wait", "for": "dom_settle"},
    {"kind": "done", "result": "searched"}
  ]}

Rules:
- refs are the [N] ids in the snapshot. Never invent a ref you cannot see.
- If the snapshot is truncated and the goal needs more of the page, scroll first.
- Keep plans short. Prefer extract over many reads.
- Finish with exactly one "done" step, last.`

	user := fmt.Sprintf("Goal: %s\n\nPage:\n%s", goal, snap.Render())

	raw, err := r.LLM.Complete(ctx, system, user)
	if m != nil {
		m.LLMCalls++
		m.EstimatedTokens += (len(system) + len(user) + len(raw)) / 4
	}
	if err != nil {
		return nil, err
	}
	plan, err := parsePlan(raw)
	if err != nil {
		return nil, err
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	return plan, nil
}

// parsePlan tolerates the two most common model failure modes: markdown
// fences and leading prose. One retry-worthy cleanup, no more.
func parsePlan(raw string) (*Plan, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "{"); i > 0 {
		s = s[i:]
	}
	if i := strings.LastIndex(s, "}"); i >= 0 && i < len(s)-1 {
		s = s[:i+1]
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "```json"), "```")
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	var p Plan
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return nil, fmt.Errorf("plan json: %w (raw: %.200s)", err, raw)
	}
	return &p, nil
}

// executeWithRepairs runs the plan; on a repairable failure it takes a fresh
// snapshot and asks the LLM for a *patched step only* — not a replan, not a
// conversation. Bounded by MaxRepairs. On a successful patch it resumes
// execution at the patched step via ExecuteFrom, rather than restarting the
// whole plan.
func (r *Runner) executeWithRepairs(ctx context.Context, bc BrowserContext, plan *Plan, snap *Snapshot, m *RunMetrics) (any, extractStore, *RunError) {
	extracted := extractStore{}
	repairs := 0
	from := 0

	for {
		// Must attach the snapshot to bc.CDP() — the chromedp-wrapped tab
		// context — not the plain caller ctx, or every chromedp.Run inside
		// ExecuteFrom fails with "invalid context".
		ectx := withSnapshot(bc.CDP(), snap)
		result, rerr := r.Executor.ExecuteFrom(ectx, plan, from, extracted)
		if rerr == nil {
			return result, extracted, nil
		}

		// Non-repairable control signals pass through.
		if rerr.Action.Kind == KindPlanAgain || rerr.Action.Kind == "eof" {
			return nil, extracted, rerr
		}

		if repairs >= r.MaxRepairs {
			return nil, extracted, rerr
		}
		repairs++
		if m != nil {
			m.Repairs++
		}

		// Fresh snapshot for the repair decision. Must use bc.CDP(), not the
		// plain caller ctx — same "invalid context" landmine as above.
		fresh, err := TakeSnapshot(bc.CDP(), bc.SnapshotMaxElements())
		if err != nil {
			return nil, extracted, &RunError{StepIndex: rerr.StepIndex, Action: rerr.Action,
				Err: fmt.Errorf("repair snapshot: %v (orig: %v)", err, rerr.Err)}
		}

		patched, ok, err := r.repairStep(ctx, rerr, snap, fresh, m)
		if err != nil || !ok {
			return nil, extracted, rerr // original error wins
		}

		// Splice the patched step in and resume from it.
		plan.Steps[rerr.StepIndex] = patched
		snap = fresh
		from = rerr.StepIndex
	}
}

// shapeResult expands any {{extract.X}} templates left in a string Done
// result — Extract data may only be available after the model produced the
// literal template at plan time.
func shapeResult(result any, ex extractStore) any {
	if s, ok := result.(string); ok && strings.Contains(s, "{{") {
		a := Action{Text: s}
		if err := expandTemplates(&a, ex); err == nil {
			return a.Text
		}
	}
	return result
}
