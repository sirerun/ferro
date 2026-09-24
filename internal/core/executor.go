package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BrowserContext is one task's handle on a browser: navigation, CDP access,
// and snapshot config. Implemented by internal/browser's pooled chromedp
// wrapper; defined here because the Runner (below) is the consumer.
type BrowserContext interface {
	Navigate(url string) error
	// CDP returns the chromedp execution context for the tab. Every CDP
	// round trip this package (or its PageDriver, see driver.go) makes —
	// snapshots, action execution, repairs — must run against this context
	// (or a context derived from it), never a caller's unrelated ctx:
	// whichever context first ran on the tab owns its CDP event-listener
	// goroutine for the tab's whole lifetime, and passing a different
	// context makes such a call fail with "invalid context". See
	// docs/adr/001-chromedp-context-lifetime.md.
	CDP() context.Context
	SnapshotMaxElements() int
	Release()
}

// WaitStrategy implements the RFC decision: DOM settle with a hard budget.
type WaitStrategy struct {
	// SettleDebounce is how long the page must see no DOM mutations before
	// we consider it settled. Default 250ms.
	SettleDebounce time.Duration
	// Budget is the hard cap on any single wait. Default 5s. Waits never
	// hang past this; they return and the executor proceeds (or the action
	// itself fails on its own timeout).
	Budget time.Duration
}

func (w WaitStrategy) withDefaults() WaitStrategy {
	if w.SettleDebounce <= 0 {
		w.SettleDebounce = 250 * time.Millisecond
	}
	if w.Budget <= 0 {
		w.Budget = 5 * time.Second
	}
	return w
}

// Executor runs a validated Plan against a live browser context.
// It contains zero LLM calls — that's the whole point.
type Executor struct {
	wait    WaitStrategy
	metrics *RunMetrics // owned by one run

	// cache is the M4 resolution cache (cache.go). It is nil-safe: a nil
	// cache makes every cache lookup/record a no-op, so caching is purely
	// additive on top of the base snapshot-resolution path.
	cache *ResolutionCache

	// driver is the PageDriver (driver.go) every action method below runs
	// against — Executor has zero direct knowledge of chromedp or CDP; see
	// docs/adr/006. NewExecutor defaults it to a ChromedpDriver matching
	// wait, so existing callers that never call WithDriver keep today's
	// exact behavior.
	driver PageDriver
}

func NewExecutor(w WaitStrategy) *Executor {
	w = w.withDefaults()
	return &Executor{wait: w, driver: NewChromedpDriver(w)}
}

// WithCache attaches a resolution cache (M4). Chainable.
func (x *Executor) WithCache(c *ResolutionCache) *Executor {
	x.cache = c
	return x
}

// WithDriver overrides the PageDriver Executor's action methods run
// against (default: a ChromedpDriver built from Executor's WaitStrategy —
// see NewExecutor). Chainable, like WithCache. docs/adr/006's
// ExtensionDriver (T12.3) is the intended second implementation.
func (x *Executor) WithDriver(d PageDriver) *Executor {
	x.driver = d
	return x
}

// RunError carries enough context for the repairer to patch a single step
// without seeing the full conversation history.
type RunError struct {
	StepIndex int
	Action    Action
	Err       error
}

func (e *RunError) Unwrap() error { return e.Err }

func (e *RunError) Error() string {
	return fmt.Sprintf("step %d (%s): %v", e.StepIndex, e.Action.Kind, e.Err)
}

// extractStore holds Extract results so later steps can reference
// {{extract.field}}.
type extractStore map[string]any

// Execute runs the plan from the start. It delegates to ExecuteFrom(0, ...).
func (x *Executor) Execute(ctx context.Context, p *Plan, extracted extractStore) (any, *RunError) {
	return x.ExecuteFrom(ctx, p, 0, extracted)
}

// ExecuteFrom runs the plan starting at step `from`. It returns when a Done
// step fires, the plan is exhausted, or a RunError occurs (which the caller
// routes to the repairer). The runner uses this after a repaired step to
// resume execution without repeating earlier, already-applied steps.
func (x *Executor) ExecuteFrom(ctx context.Context, p *Plan, from int, extracted extractStore) (result any, rerr *RunError) {
	for i := from; i < len(p.Steps); i++ {
		a := p.Steps[i]
		if err := ctx.Err(); err != nil {
			return nil, &RunError{StepIndex: i, Action: a, Err: err}
		}

		// Template expansion: {{extract.field}} → value from prior Extract.
		if err := expandTemplates(&a, extracted); err != nil {
			return nil, &RunError{StepIndex: i, Action: a, Err: err}
		}

		if a.Kind == KindDone {
			return a.Result, nil
		}
		if a.Kind == KindPlanAgain {
			return nil, &RunError{StepIndex: i, Action: a, Err: fmt.Errorf("replan requested: %s", a.Reason)}
		}
		res, err := x.executeAction(ctx, a, extracted)

		if err != nil {
			return nil, &RunError{StepIndex: i, Action: a, Err: err}
		}
		if res != nil {
			extracted["last"] = res
		}
	}
	// Plan exhausted without Done — not fatal, but the caller should know.
	return nil, &RunError{StepIndex: len(p.Steps) - 1,
		Action: Action{Kind: "eof"}, Err: fmt.Errorf("plan ended without done")}
}

// ExecuteOne runs a single action against snap, the entrypoint for callers
// outside a full Plan/Runner cycle (internal/mcp's primitive MCP tools:
// navigate, click, fill, select, key, scroll, wait, extract map 1:1 onto
// this). snap may be nil for actions that never resolve a ref (goto, key,
// scroll, wait); store persists {{extract.last...}} state across calls
// sharing it — pass a fresh map to start clean. done/plan_again are control
// signals with no meaning outside a Plan and are rejected here.
func (x *Executor) ExecuteOne(ctx context.Context, snap *Snapshot, a Action, store map[string]any) (any, error) {
	if a.Kind == KindDone || a.Kind == KindPlanAgain {
		return nil, fmt.Errorf("%s is a plan control signal, not a step ExecuteOne can run", a.Kind)
	}
	if err := validateAction(a); err != nil {
		return nil, err
	}
	if err := expandTemplates(&a, store); err != nil {
		return nil, err
	}
	res, err := x.executeAction(withSnapshot(ctx, snap), a, store)
	if err != nil {
		return nil, err
	}
	if res != nil {
		store["last"] = res
	}
	return res, nil
}

// executeAction bounds the entire action, including WaitVisible and navigation.
func (x *Executor) executeAction(ctx context.Context, a Action, extracted extractStore) (any, error) {
	stepCtx, cancel := context.WithTimeout(ctx, x.wait.Budget)
	defer cancel()
	switch a.Kind {
	case KindGoto:
		return nil, x.doGoto(stepCtx, a)
	case KindClick:
		return nil, x.doClick(stepCtx, a)
	case KindFill:
		return nil, x.doFill(stepCtx, a)
	case KindSelect:
		return nil, x.doSelect(stepCtx, a)
	case KindKey:
		return nil, x.doKey(stepCtx, a)
	case KindScroll:
		return nil, x.doScroll(stepCtx, a)
	case KindWait:
		return nil, x.doWait(stepCtx, a)
	case KindExtract:
		return x.doExtract(stepCtx, a, extracted)
	default:
		return nil, fmt.Errorf("unhandled kind %q", a.Kind)
	}
}

// --- individual actions ---

func (x *Executor) doGoto(ctx context.Context, a Action) error {
	return x.driver.Navigate(ctx, a.URL)
}

func (x *Executor) doClick(ctx context.Context, a Action) error {
	// Resolve ref → selector at execution time (snapshot is stale by design;
	// we re-locate by the ref's stored signature via resolveRef, and check
	// the resolution cache first — see locator.go and cache.go).
	sel, key, err := x.resolveRef(ctx, a.Ref, string(KindClick))
	if err != nil {
		return err
	}
	err = x.driver.Click(ctx, sel)
	x.recordOutcome(key, sel, err) // success -> Put; stale_ref -> Invalidate
	return err
}

func (x *Executor) doFill(ctx context.Context, a Action) error {
	sel, key, err := x.resolveRef(ctx, a.Ref, string(KindFill))
	if err != nil {
		return err
	}
	err = x.driver.Fill(ctx, sel, a.Text)
	x.recordOutcome(key, sel, err)
	return err
}

func (x *Executor) doSelect(ctx context.Context, a Action) error {
	sel, key, err := x.resolveRef(ctx, a.Ref, string(KindSelect))
	if err != nil {
		return err
	}
	defer func() { x.recordOutcome(key, sel, err) }()

	var status string
	status, err = x.driver.Select(ctx, sel, a.Value)
	if err != nil {
		return err
	}
	if status != "ok" {
		err = fmt.Errorf("select %q: %s", a.Value, status)
		return err
	}
	return nil
}

func (x *Executor) doKey(ctx context.Context, a Action) error {
	return x.driver.Key(ctx, a.Text)
}

func (x *Executor) doScroll(ctx context.Context, a Action) error {
	return x.driver.Scroll(ctx, a.To)
}

func (x *Executor) doWait(ctx context.Context, a Action) error {
	switch {
	case a.For == "dom_settle":
		return x.driver.Settle(ctx)
	case strings.HasSuffix(a.For, "s") || strings.HasSuffix(a.For, "ms"):
		d, err := time.ParseDuration(a.For)
		if err != nil {
			return fmt.Errorf("wait %q: %v", a.For, err)
		}
		if d > x.wait.Budget {
			d = x.wait.Budget
		}
		select {
		case <-time.After(d):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	default:
		// Treat as CSS selector.
		return x.driver.WaitVisible(ctx, a.For)
	}
}

// doExtract implements the v0.1 compromise: if the plan's Extract action
// carries Fields (CSS selectors per field), extraction is pure JS — zero LLM
// calls. Otherwise the executor returns raw page text, and the runner makes
// one small LLM call to structure it against Schema. That call belongs to
// the runner, not the executor, so "the executor has zero LLM calls" holds.
// structureRequest transfers control to Runner after the deterministic text read.
// The executor never invokes the model, even indirectly through a callback.
type structureRequest struct {
	Text   string
	Schema json.RawMessage
}

func (*structureRequest) Error() string { return "extract requires schema structuring" }

func (x *Executor) doExtract(ctx context.Context, a Action, store extractStore) (any, error) {
	if len(a.Fields) > 0 {
		out := map[string]any{}
		failures := map[string]string{}
		successes := 0
		fields := make([]string, 0, len(a.Fields))
		for f := range a.Fields {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		for _, field := range fields {
			sel := a.Fields[field]
			var key CacheKey
			var err error
			if strings.HasPrefix(sel, "[") && strings.HasSuffix(sel, "]") {
				if ref, e := strconv.Atoi(sel[1 : len(sel)-1]); e == nil {
					sel, key, err = x.resolveRef(ctx, ref, string(KindExtract))
				}
			}
			var value string
			if err == nil {
				value, err = x.driver.ExtractField(ctx, sel)
			}
			x.recordOutcome(key, sel, err)
			if err != nil {
				var stopped *StopError
				if errors.As(err, &stopped) || isTaskV2BudgetFailure(err) || isProviderContextTerminationV2(err) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return nil, err
				}
				out[field] = ""
				failures[field] = err.Error()
			} else {
				out[field] = value
				successes++
			}
		}
		if x.metrics != nil && len(failures) > 0 {
			if x.metrics.ExtractErrors == nil {
				x.metrics.ExtractErrors = map[string]string{}
			}
			for field, err := range failures {
				x.metrics.ExtractErrors[field] = err
			}
		}
		if successes == 0 {
			return nil, fmt.Errorf("extract: all %d fields failed: %v", len(fields), failures)
		}
		return out, nil
	}
	text, err := x.driver.ExtractText(ctx)
	if err != nil {
		return nil, err
	}
	return nil, &structureRequest{Text: text, Schema: a.Schema}
}
