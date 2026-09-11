package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/chromedp/chromedp"
)

// PageDriver is the boundary between Executor's action-execution loop and
// whatever actually drives a live browser tab. Executor and the rest of
// internal/core (planner, repair, resolution cache) know nothing about CDP,
// chromedp, or any other automation transport; every Action's effect is
// expressed here in ferro's own vocabulary (a resolved CSS selector, plain
// text, a semantic scroll target) so a second backend that speaks a
// completely different protocol (docs/adr/006's browser-extension bridge)
// can implement this interface with zero change to core's execution,
// repair, or caching logic (docs/adr/006 decision 1).
//
// Every method receives the ctx the caller already bounded (executeAction
// wraps each step in the Executor's WaitStrategy.Budget); implementations
// must not impose their own separate timeout on top of it.
type PageDriver interface {
	// Navigate loads url, waits for the DOM to report ready, and settles
	// (see Settle) before returning.
	Navigate(ctx context.Context, url string) error

	// Click waits for sel to become visible, clicks it, and settles —
	// clicks routinely trigger rerenders.
	Click(ctx context.Context, sel string) error

	// Fill waits for sel to become visible, clears its existing value
	// (best-effort: a framework-controlled field may have nothing a
	// driver can clear, which is not itself a failure), and types text
	// into it.
	Fill(ctx context.Context, sel string, text string) error

	// Select chooses the <select> option at sel whose visible label or
	// value equals value, dispatching a change event. status reports the
	// outcome ("ok", "no-select" if sel isn't a <select>, "no-option" if
	// no option matched); err is reserved for a failed round trip against
	// the page itself.
	Select(ctx context.Context, sel, value string) (status string, err error)

	// Key sends a single named key event (e.g. "Enter") to the page.
	Key(ctx context.Context, key string) error

	// Scroll moves the viewport to the semantic target "top" or "bottom"
	// and settles.
	Scroll(ctx context.Context, to string) error

	// WaitVisible blocks until sel is visible in the DOM.
	WaitVisible(ctx context.Context, sel string) error

	// Settle blocks until the page has produced no DOM mutations for the
	// driver's configured debounce window, capped at its budget.
	Settle(ctx context.Context) error

	// ExtractField reads one field's current value from sel: an input's
	// or select's value, or innerText for anything else.
	ExtractField(ctx context.Context, sel string) (value string, err error)

	// ExtractText reads the page's visible body text — used for
	// schema-based extraction, which the Runner (never the driver or
	// Executor) structures into JSON via one LLM call.
	ExtractText(ctx context.Context) (string, error)

	// Snapshot compiles the live page into a Snapshot: the numbered,
	// interactive-element list both the planner and ref resolution work
	// from (docs/adr/006 decision 3's "run the snapshot-compiler
	// script").
	Snapshot(ctx context.Context, maxElements int) (*Snapshot, error)
}

// ChromedpDriver implements PageDriver against a real Chrome tab via
// chromedp/CDP. It is the original, and until docs/adr/006's extension
// backend (T12.3's ExtensionDriver) lands, only, PageDriver implementation —
// this file is a verbatim relocation of the chromedp calls that used to live
// inline in executor.go, with zero behavior change.
type ChromedpDriver struct {
	wait WaitStrategy
}

// NewChromedpDriver builds a ChromedpDriver using w for its settle
// debounce/budget (defaults applied the same way NewExecutor does, so a
// ChromedpDriver constructed independently of an Executor still settles
// sanely).
func NewChromedpDriver(w WaitStrategy) *ChromedpDriver {
	return &ChromedpDriver{wait: w.withDefaults()}
}

func (d *ChromedpDriver) Navigate(ctx context.Context, url string) error {
	// Navigate, then settle. NetworkIdle is deliberately NOT used (RFC
	// decision: unreliable on SPAs with websockets); DOM settle + budget
	// covers it.
	return chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitReady("body"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return domSettle(ctx, d.wait)
		}),
	)
}

func (d *ChromedpDriver) Click(ctx context.Context, sel string) error {
	return chromedp.Run(ctx,
		chromedp.WaitVisible(sel, chromedp.ByQuery),
		chromedp.Click(sel, chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return domSettle(ctx, d.wait) // clicks often trigger rerenders
		}),
	)
}

func (d *ChromedpDriver) Fill(ctx context.Context, sel, text string) error {
	err := chromedp.Run(ctx, chromedp.WaitVisible(sel, chromedp.ByQuery))
	if err == nil {
		// chromedp.Clear reads a textarea's current value from its DOM child
		// #text node, but a framework-controlled textarea (React, Vue, ...)
		// never has one — its value lives in JS state, not static markup —
		// so Clear fails on every such field, empty or not, with "does not
		// have child #text node", not just an edge case. Treat that specific
		// failure as "nothing to clear" and proceed to type; any other Clear
		// failure (bad selector, wrong element kind) still aborts the fill.
		if cerr := chromedp.Run(ctx, chromedp.Clear(sel, chromedp.ByQuery)); cerr != nil &&
			!strings.Contains(cerr.Error(), "does not have child #text node") {
			err = cerr
		}
	}
	if err == nil {
		err = chromedp.Run(ctx, chromedp.SendKeys(sel, text, chromedp.ByQuery))
	}
	return err
}

func (d *ChromedpDriver) Select(ctx context.Context, sel, value string) (string, error) {
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
	})()`, sel, value, value)

	var status string
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &status)); err != nil {
		return "", err
	}
	return status, nil
}

func (d *ChromedpDriver) Key(ctx context.Context, key string) error {
	return chromedp.Run(ctx, chromedp.KeyEvent(key))
}

func (d *ChromedpDriver) Scroll(ctx context.Context, to string) error {
	var js string
	switch to {
	case "top":
		js = `window.scrollTo(0, 0)`
	case "bottom":
		js = `window.scrollTo(0, document.body.scrollHeight)`
	default:
		// Target may be a ref rendered as string, e.g. "12".
		return fmt.Errorf("scroll to ref %q: ref-scroll not wired in v0.1; use top|bottom", to)
	}
	var dummy any
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &dummy)); err != nil {
		return err
	}
	return domSettle(ctx, d.wait) // scroll-loaded content needs the settle
}

func (d *ChromedpDriver) WaitVisible(ctx context.Context, sel string) error {
	return chromedp.Run(ctx, chromedp.WaitVisible(sel, chromedp.ByQuery))
}

func (d *ChromedpDriver) Settle(ctx context.Context) error {
	return domSettle(ctx, d.wait)
}

func (d *ChromedpDriver) ExtractField(ctx context.Context, sel string) (string, error) {
	js := fmt.Sprintf(`(() => {try {const e=document.querySelector(%q);if(!e)return {error:"no matching element"};return {value:String(e.value ?? e.innerText ?? "").trim()}}catch(e){return {error:e.message}}})()`, sel)
	var got struct {
		Value string `json:"value"`
		Error string `json:"error"`
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &got)); err != nil {
		return "", err
	}
	if got.Error != "" {
		return "", fmt.Errorf("%s", got.Error)
	}
	return got.Value, nil
}

func (d *ChromedpDriver) ExtractText(ctx context.Context) (string, error) {
	var text string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.body.innerText.slice(0,20000)`, &text)); err != nil {
		return "", err
	}
	return text, nil
}

func (d *ChromedpDriver) Snapshot(ctx context.Context, maxElements int) (*Snapshot, error) {
	return TakeSnapshot(ctx, maxElements)
}
