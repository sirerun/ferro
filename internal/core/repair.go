package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ErrorClass is a coarse taxonomy so the repairer (and the user) can tell
// apart "stale ref" from "page fundamentally changed" from "site is broken".
type ErrorClass string

const (
	ErrStaleRef   ErrorClass = "stale_ref"   // element gone / selector no match
	ErrTimeout    ErrorClass = "timeout"     // wait budget exceeded
	ErrWrongState ErrorClass = "wrong_state" // element present, interaction failed
	ErrNavigation ErrorClass = "navigation"  // goto failed, redirect, 404
	ErrSchema     ErrorClass = "schema"      // plan/extract output malformed
	ErrUnknown    ErrorClass = "unknown"
)

// Classify maps a RunError to a repair strategy hint. Deliberately
// pattern-matching on message text — chromedp errors aren't typed well
// enough for errors.Is, and this keeps the taxonomy in one place.
func Classify(rerr *RunError) ErrorClass {
	if rerr == nil || rerr.Err == nil {
		return ErrUnknown
	}
	msg := rerr.Err.Error()
	switch rerr.Action.Kind {
	case KindClick, KindFill, KindSelect:
		if containsAny(msg, "no snapshot", "not in snapshot", "no such element", "not visible", "stale ref") {
			return ErrStaleRef
		}
		if containsAny(msg, "timeout", "deadline") {
			return ErrTimeout
		}
		return ErrWrongState
	case KindGoto:
		return ErrNavigation
	case KindExtract:
		if containsAny(msg, "json", "schema") {
			return ErrSchema
		}
		return ErrWrongState
	case KindWait:
		return ErrTimeout
	}
	return ErrUnknown
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// repairSystem is a narrow prompt: patch one step, nothing else. The model
// never sees prior steps, history, or the original goal context beyond a
// one-line reminder — repair cost stays near ~1k tokens.
const repairSystem = `You repair a single failed browser automation step.
You will receive: the failed step (JSON), the error, and a FRESH page snapshot.
Respond with ONLY the corrected step as JSON, same schema, or {"kind":"abort","reason":"..."}
if the goal is impossible on this page.
Rules:
- Prefer remapping the ref to a matching [N] in the fresh snapshot.
- If a wait would fix it (content still loading), return {"kind":"wait","for":"dom_settle"}.
- Do not invent refs. Do not change the overall approach.`

// repairStep asks the LLM to patch one failed action. ok=false means the
// model declined (abort) or output was unusable — the caller keeps the
// original error. orig is the snapshot the failed plan was built against;
// fresh is a just-taken snapshot of the current page — both are needed for
// the local fuzzy ref remap below.
func (r *Runner) repairStep(ctx context.Context, rerr *RunError, orig, fresh *Snapshot, m *RunMetrics) (patched Action, ok bool, err error) {
	failed := rerr.Action
	if failed.Secret {
		failed.Text = "[REDACTED]"
	}
	failedJSON, err := json.Marshal(failed)
	if err != nil {
		return Action{}, false, err
	}

	user := fmt.Sprintf(
		"Failed step: %s\nError class: %s\nError: %v\n\nFresh page:\n%s",
		string(failedJSON), Classify(rerr), rerr.Err, fresh.Render(),
	)

	raw, err := r.LLM.Complete(withBudgetRequestKindV2(ctx, budgetKindRepairV2), repairSystem, user)
	if m != nil {
		m.LLMCalls++
		m.EstimatedTokens += (len(repairSystem) + len(user) + len(raw)) / 4
	}
	if err != nil {
		return Action{}, false, err
	}

	patched, err = parseAction(raw, true)
	if err != nil {
		return Action{}, false, err
	}

	if rerr.Action.Secret && patched.Kind == KindFill {
		patched.Secret = true
		patched.Text = rerr.Action.Text
	}

	// Model chose to abort — surface as a definitive no.
	if patched.Kind == "abort" {
		return Action{}, false, fmt.Errorf("repairer aborted: %s", patched.Reason)
	}
	if err := validateAction(patched); err != nil {
		return Action{}, false, fmt.Errorf("patched step invalid: %w", err)
	}

	// Free win before burning an execution attempt: if the patch targets a
	// ref that no longer resolves, try fuzzy-remapping against the fresh
	// snapshot locally, using the *original* action's element as the hint —
	// don't trust the model's guess for the ref number itself.
	if patched.Ref > 0 && !fresh.hasRef(patched.Ref) {
		if e := orig.element(rerr.Action.Ref); e != nil {
			if hint, found := fresh.RefByHint(e.Tag, firstNonEmpty(e.Name, e.Text)); found {
				patched.Ref = hint
			}
		}
	}

	return patched, true, nil
}

// --- small snapshot helpers ---

func (s *Snapshot) hasRef(ref int) bool {
	return s.element(ref) != nil
}

func (s *Snapshot) element(ref int) *Element {
	for i := range s.Elements {
		if s.Elements[i].Ref == ref {
			return &s.Elements[i]
		}
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
