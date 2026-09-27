package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/chromedp/chromedp"

	"github.com/sirerun/ferro/internal/core"
	"github.com/sirerun/ferro/internal/extbridge"
)

// Primitive tool argument shapes. Each maps 1:1 onto one core.ActionKind
// (T11.4); the tool name matches the ActionKind's wire name.
type (
	snapshotArgs struct{}

	navigateArgs struct {
		URL string `json:"url" jsonschema:"the URL to navigate the shared tab to"`
	}

	clickArgs struct {
		Ref int `json:"ref" jsonschema:"the element ref from the last snapshot"`
	}

	fillArgs struct {
		Ref    int    `json:"ref" jsonschema:"the element ref from the last snapshot"`
		Text   string `json:"text" jsonschema:"the text to type into the field"`
		Secret bool   `json:"secret,omitempty" jsonschema:"true to mask this value in logs"`
	}

	selectArgs struct {
		Ref   int    `json:"ref" jsonschema:"the element ref from the last snapshot"`
		Value string `json:"value" jsonschema:"the option's visible label or value to select"`
	}

	keyArgs struct {
		Text string `json:"text" jsonschema:"the key name to send, e.g. Enter or Tab"`
	}

	scrollArgs struct {
		To string `json:"to" jsonschema:"top or bottom"`
	}

	waitArgs struct {
		For string `json:"for" jsonschema:"dom_settle, a Go duration like 2s, or a CSS selector to wait for"`
	}

	extractArgs struct {
		Fields map[string]string `json:"fields" jsonschema:"field name to CSS selector; each field's element's value (inputs) or innerText is read directly, with zero LLM calls -- schema-based extraction needing an LLM is only available via run_task"`
	}
)

func registerPrimitiveTools(server *sdk.Server, c caller) {
	addRelayTool[snapshotArgs](server, &sdk.Tool{
		Name:        "snapshot",
		Description: "Take a fresh snapshot of the shared tab's current page: URL, title, and a numbered list of interactive elements (refs) for click/fill/select/extract to target. Gated by the origin allowlist because snapshots contain authenticated page content.",
	}, c)
	addRelayTool[navigateArgs](server, &sdk.Tool{
		Name:        "navigate",
		Description: "Navigate the shared tab to url and wait for the page to settle. Invalidates the last snapshot -- call snapshot again before targeting a ref. Gated by the origin allowlist.",
	}, c)
	addRelayTool[clickArgs](server, &sdk.Tool{
		Name:        "click",
		Description: "Click the element at ref (from the last snapshot). Gated by the origin allowlist.",
	}, c)
	addRelayTool[fillArgs](server, &sdk.Tool{
		Name:        "fill",
		Description: "Clear and type text into the field at ref (from the last snapshot). Gated by the origin allowlist.",
	}, c)
	addRelayTool[selectArgs](server, &sdk.Tool{
		Name:        "select",
		Description: "Choose an option (by visible label or value) in the <select> at ref. Gated by the origin allowlist.",
	}, c)
	addRelayTool[keyArgs](server, &sdk.Tool{
		Name:        "key",
		Description: "Send a key event (e.g. Enter) to the shared tab. Gated by the origin allowlist.",
	}, c)
	addRelayTool[scrollArgs](server, &sdk.Tool{
		Name:        "scroll",
		Description: "Scroll the shared tab to \"top\" or \"bottom\". Gated by the origin allowlist.",
	}, c)
	addRelayTool[waitArgs](server, &sdk.Tool{
		Name:        "wait",
		Description: "Wait for dom_settle, a fixed duration (e.g. \"2s\"), or a CSS selector to become visible. DOM waits are gated by the origin allowlist.",
	}, c)
	addRelayTool[extractArgs](server, &sdk.Tool{
		Name:        "extract",
		Description: "Read each field's CSS selector from the shared tab's current page (value for inputs, innerText otherwise) -- zero LLM calls. Gated by the origin allowlist (reading authenticated page content is a disclosure risk).",
	}, c)
}

// currentOrigin returns the shared tab's current page origin
// (scheme://host[:port]). Driver actions gate content reads and effects;
// navigation is checked against its target, and fixed sleeps read no content.
func (o *Owner) currentOrigin(ctx context.Context) (string, error) {
	runCtx, cancel := o.actionCtx(ctx)
	defer cancel()
	var raw string
	if o.bridge != nil {
		d := &extbridge.ExtensionDriver{Bridge: o.bridge}
		var err error
		raw, err = d.Location(runCtx)
		if err != nil {
			return "", err
		}
	} else if err := chromedp.Run(runCtx, chromedp.Location(&raw)); err != nil {
		return "", fmt.Errorf("read current URL: %w", err)
	}
	return originOf(raw)
}

func originOf(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse URL %q: %w", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("URL %q has no scheme+host to check against the allowlist", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

// actionCtx returns a context chromedp actions can run against, bridging
// caller cancellation into the tab's own CDP context. chromedp ties a tab's
// whole CDP event-listener goroutine to whatever context first launched it
// (o.tab.CDP()); running an action against ctx directly fails with "invalid
// context" because it was never passed through chromedp.NewContext. This is
// the same landmine internal/core/runner.go's Run documents and works
// around the same way: derive from the tab's context, and layer the
// caller's cancellation on top via AfterFunc rather than by using the
// caller's context as the base.
func (o *Owner) actionCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if o.tab == nil {
		return context.WithCancel(ctx)
	}
	runCtx, cancel := context.WithCancel(o.tab.CDP())
	stop := context.AfterFunc(ctx, cancel)
	return runCtx, func() {
		stop()
		cancel()
	}
}

// checkOrigin resolves origin (or the tab's current origin, if target is
// empty) and gates it against the allowlist.
func (o *Owner) checkOrigin(ctx context.Context, target string) error {
	origin := target
	if origin == "" {
		var err error
		origin, err = o.currentOrigin(ctx)
		if err != nil {
			return err
		}
	} else {
		var err error
		origin, err = originOf(target)
		if err != nil {
			return err
		}
	}
	return o.allow.Check(origin)
}

func (o *Owner) snapshot(ctx context.Context, _ json.RawMessage) (any, error) {
	runCtx, cancel := o.actionCtx(ctx)
	defer cancel()
	generation := uint64(0)
	if o.bridge != nil {
		generation = o.bridge.Generation()
		runCtx = o.bridge.PinGeneration(runCtx, generation)
	}
	snap, err := o.driver.Snapshot(runCtx, o.cfg.MaxElements)
	if err != nil {
		return nil, err
	}
	if o.bridge != nil && generation != o.bridge.Generation() {
		return nil, &core.StopError{Code: "pairing_changed", Message: "tab pairing changed during the snapshot; take a fresh snapshot"}
	}
	o.snap = snap
	o.snapGeneration = generation
	return snap, nil
}

func (o *Owner) navigate(ctx context.Context, args json.RawMessage) (any, error) {
	var in navigateArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("decode navigate args: %w", err)
	}
	if in.URL == "" {
		return nil, fmt.Errorf("url is required")
	}
	runCtx, cancel := o.actionCtx(ctx)
	defer cancel()
	res, err := o.exec.ExecuteOne(runCtx, o.snap, core.Action{Kind: core.KindGoto, URL: in.URL}, o.extracted)
	if err != nil {
		return nil, err
	}
	// Refs are page-specific; force the client to snapshot again before
	// targeting one on the new page.
	o.snap = nil
	o.snapGeneration = 0
	return res, nil
}

func (o *Owner) click(ctx context.Context, args json.RawMessage) (any, error) {
	var in clickArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("decode click args: %w", err)
	}
	if o.snap == nil {
		return nil, fmt.Errorf("no snapshot yet; call snapshot before targeting a ref")
	}
	runCtx, cancel := o.actionCtx(ctx)
	defer cancel()
	var err error
	if runCtx, err = o.pinSnapshot(runCtx); err != nil {
		return nil, err
	}
	return o.exec.ExecuteOne(runCtx, o.snap, core.Action{Kind: core.KindClick, Ref: in.Ref}, o.extracted)
}

func (o *Owner) fill(ctx context.Context, args json.RawMessage) (any, error) {
	var in fillArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("decode fill args: %w", err)
	}
	if o.snap == nil {
		return nil, fmt.Errorf("no snapshot yet; call snapshot before targeting a ref")
	}
	runCtx, cancel := o.actionCtx(ctx)
	defer cancel()
	var err error
	if runCtx, err = o.pinSnapshot(runCtx); err != nil {
		return nil, err
	}
	return o.exec.ExecuteOne(runCtx, o.snap, core.Action{Kind: core.KindFill, Ref: in.Ref, Text: in.Text, Secret: in.Secret}, o.extracted)
}

func (o *Owner) selectOption(ctx context.Context, args json.RawMessage) (any, error) {
	var in selectArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("decode select args: %w", err)
	}
	if o.snap == nil {
		return nil, fmt.Errorf("no snapshot yet; call snapshot before targeting a ref")
	}
	runCtx, cancel := o.actionCtx(ctx)
	defer cancel()
	var err error
	if runCtx, err = o.pinSnapshot(runCtx); err != nil {
		return nil, err
	}
	return o.exec.ExecuteOne(runCtx, o.snap, core.Action{Kind: core.KindSelect, Ref: in.Ref, Value: in.Value}, o.extracted)
}

func (o *Owner) key(ctx context.Context, args json.RawMessage) (any, error) {
	var in keyArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("decode key args: %w", err)
	}
	runCtx, cancel := o.actionCtx(ctx)
	defer cancel()
	var err error
	if runCtx, err = o.pinSnapshot(runCtx); err != nil {
		return nil, err
	}
	return o.exec.ExecuteOne(runCtx, o.snap, core.Action{Kind: core.KindKey, Text: in.Text}, o.extracted)
}

func (o *Owner) scroll(ctx context.Context, args json.RawMessage) (any, error) {
	var in scrollArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("decode scroll args: %w", err)
	}
	runCtx, cancel := o.actionCtx(ctx)
	defer cancel()
	var err error
	if runCtx, err = o.pinSnapshot(runCtx); err != nil {
		return nil, err
	}
	return o.exec.ExecuteOne(runCtx, o.snap, core.Action{Kind: core.KindScroll, To: in.To}, o.extracted)
}

func (o *Owner) wait(ctx context.Context, args json.RawMessage) (any, error) {
	var in waitArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("decode wait args: %w", err)
	}
	// Fixed sleeps read no page content. The driver gates DOM waits.
	runCtx, cancel := o.actionCtx(ctx)
	defer cancel()
	var err error
	if runCtx, err = o.pinSnapshot(runCtx); err != nil {
		return nil, err
	}
	return o.exec.ExecuteOne(runCtx, o.snap, core.Action{Kind: core.KindWait, For: in.For}, o.extracted)
}

func (o *Owner) extract(ctx context.Context, args json.RawMessage) (any, error) {
	var in extractArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("decode extract args: %w", err)
	}
	if len(in.Fields) == 0 {
		return nil, fmt.Errorf("fields is required and must be non-empty; schema-based extraction needs an LLM call and is only available via run_task")
	}
	runCtx, cancel := o.actionCtx(ctx)
	defer cancel()
	var err error
	if runCtx, err = o.pinSnapshot(runCtx); err != nil {
		return nil, err
	}
	return o.exec.ExecuteOne(runCtx, o.snap, core.Action{Kind: core.KindExtract, Fields: in.Fields}, o.extracted)
}

func (o *Owner) pinSnapshot(ctx context.Context) (context.Context, error) {
	if o.bridge == nil || o.snap == nil {
		return ctx, nil
	}
	if o.snapGeneration != o.bridge.Generation() {
		o.snap = nil
		o.snapGeneration = 0
		return ctx, &core.StopError{Code: "pairing_changed", Message: "tab pairing changed; take a fresh snapshot before continuing"}
	}
	return o.bridge.PinGeneration(ctx, o.snapGeneration), nil
}
