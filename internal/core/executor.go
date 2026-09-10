package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// BrowserContext is one task's handle on a browser: navigation, CDP access,
// and snapshot config. Implemented by internal/browser's pooled chromedp
// wrapper; defined here because the Runner (below) is the consumer.
type BrowserContext interface {
	Navigate(url string) error
	// CDP returns the chromedp execution context for the tab. Every
	// chromedp.Run this package makes — snapshots, action execution,
	// repairs — must run against this context (or a context derived from
	// it), never a caller's unrelated ctx: whichever context first ran on
	// the tab owns its CDP event-listener goroutine for the tab's whole
	// lifetime, and passing a different context makes chromedp.Run fail
	// with "invalid context". See docs/adr/001-chromedp-context-lifetime.md.
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
}

func NewExecutor(w WaitStrategy) *Executor {
	return &Executor{wait: w.withDefaults()}
}

// WithCache attaches a resolution cache (M4). Chainable.
func (x *Executor) WithCache(c *ResolutionCache) *Executor {
	x.cache = c
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
	// Navigate, then settle. NetworkIdle is deliberately NOT used (RFC decision:
	// unreliable on SPAs with websockets); DOM settle + budget covers it.
	return chromedp.Run(ctx,
		chromedp.Navigate(a.URL),
		chromedp.WaitReady("body"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return domSettle(ctx, x.wait)
		}),
	)
}

func (x *Executor) doClick(ctx context.Context, a Action) error {
	// Resolve ref → selector at execution time (snapshot is stale by design;
	// we re-locate by the ref's stored signature via resolveRef, and check
	// the resolution cache first — see locator.go and cache.go).
	sel, key, err := x.resolveRef(ctx, a.Ref, string(KindClick))
	if err != nil {
		return err
	}
	err = chromedp.Run(ctx,
		chromedp.WaitVisible(sel, chromedp.ByQuery),
		chromedp.Click(sel, chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return domSettle(ctx, x.wait) // clicks often trigger rerenders
		}),
	)
	x.recordOutcome(key, sel, err) // success -> Put; stale_ref -> Invalidate
	return err
}

func (x *Executor) doFill(ctx context.Context, a Action) error {
	sel, key, err := x.resolveRef(ctx, a.Ref, string(KindFill))
	if err != nil {
		return err
	}
	err = chromedp.Run(ctx,
		chromedp.WaitVisible(sel, chromedp.ByQuery),
		chromedp.Clear(sel, chromedp.ByQuery),
		chromedp.SendKeys(sel, a.Text, chromedp.ByQuery),
	)
	x.recordOutcome(key, sel, err)
	return err
}

func (x *Executor) doSelect(ctx context.Context, a Action) error {
	sel, key, err := x.resolveRef(ctx, a.Ref, string(KindSelect))
	if err != nil {
		return err
	}
	defer func() { x.recordOutcome(key, sel, err) }()
	// Select by visible label first, fall back to value.
	js := fmt.Sprintf(`(() => {
		const el = document.querySelector(%q);
		if (!el || el.tagName !== 'SELECT') return 'no-select';
		for (const opt of el.options) {
			if (opt.text.trim() === %q || opt.value === %q) {
				el.value = opt.value;
				el.dispatchEvent(new Event('change', {bubbles: true}));
				return 'ok';
			}
		}
		return 'no-option';
	})()`, sel, a.Value, a.Value)

	var status string
	if err = chromedp.Run(ctx, chromedp.Evaluate(js, &status)); err != nil {
		return err
	}
	if status != "ok" {
		err = fmt.Errorf("select %q: %s", a.Value, status)
		return err
	}
	return nil
}

func (x *Executor) doKey(ctx context.Context, a Action) error {
	return chromedp.Run(ctx, chromedp.KeyEvent(a.Text))
}

func (x *Executor) doScroll(ctx context.Context, a Action) error {
	var js string
	switch a.To {
	case "top":
		js = `window.scrollTo(0, 0)`
	case "bottom":
		js = `window.scrollTo(0, document.body.scrollHeight)`
	default:
		// Target may be a ref rendered as string, e.g. "12".
		return fmt.Errorf("scroll to ref %q: ref-scroll not wired in v0.1; use top|bottom", a.To)
	}
	var dummy any
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &dummy)); err != nil {
		return err
	}
	return domSettle(ctx, x.wait) // scroll-loaded content needs the settle
}

func (x *Executor) doWait(ctx context.Context, a Action) error {
	switch {
	case a.For == "dom_settle":
		return domSettle(ctx, x.wait)
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
		return chromedp.Run(ctx, chromedp.WaitVisible(a.For, chromedp.ByQuery))
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
			var got struct {
				Value string `json:"value"`
				Error string `json:"error"`
			}
			if err == nil {
				js := fmt.Sprintf(`(() => {try {const e=document.querySelector(%q);if(!e)return {error:"no matching element"};return {value:String(e.value ?? e.innerText ?? "").trim()}}catch(e){return {error:e.message}}})()`, sel)
				err = chromedp.Run(ctx, chromedp.Evaluate(js, &got))
				if err == nil && got.Error != "" {
					err = fmt.Errorf("%s", got.Error)
				}
			}
			x.recordOutcome(key, sel, err)
			if err != nil {
				out[field] = ""
				failures[field] = err.Error()
			} else {
				out[field] = got.Value
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
	var text string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.body.innerText.slice(0,20000)`, &text)); err != nil {
		return nil, err
	}
	return nil, &structureRequest{Text: text, Schema: a.Schema}
}
