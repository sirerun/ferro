// Package ferro is a token-efficient AI browser automation library: the LLM
// is asked once (or rarely) for a declarative JSON plan; deterministic Go
// code executes that plan mechanically against Chrome via CDP. Re-planning
// happens only on failure.
//
// This file is a thin facade over internal/core (the engine),
// internal/browser (the chromedp tab pool), and internal/llm (LLM client
// implementations). It exists to keep the public surface small and stable
// while the engine evolves freely underneath it. See DESIGN.md.
package ferro

import (
	"context"
	"fmt"

	"github.com/dndungu/ferro/internal/browser"
	"github.com/dndungu/ferro/internal/core"
	"github.com/dndungu/ferro/internal/llm"
)

// Task is one unit of automation work: a goal, an optional starting URL,
// and knobs bounding how much replanning is allowed.
type Task = core.Task

// RunMetrics reports what a Run call cost: LLM calls, repairs, replans, and
// an estimated token count.
type RunMetrics = core.RunMetrics

// LLMClient is the only model interface ferro needs. Any OpenAI-compatible
// endpoint satisfies this via OpenAICompatible; bring your own for anything
// else.
type LLMClient = core.LLMClient

// OpenAICompatible talks to any /v1/chat/completions endpoint: OpenAI,
// Ollama, vLLM, LM Studio, OpenRouter, and OpenAI-compatible gateways.
type OpenAICompatible = llm.OpenAICompatible

// BrowserConfig configures the browser pool: headless mode, pool size,
// snapshot element cap, and launch flags.
type BrowserConfig = browser.Config

// Browser is a pool of warm, reusable Chrome tabs. Acquire a BrowserContext
// from it for each Task, and Release it when done.
type Browser struct {
	pool *browser.Browser
}

// NewBrowser starts a (lazily-warmed) tab pool.
func NewBrowser(cfg BrowserConfig) (*Browser, error) {
	pool, err := browser.New(cfg)
	if err != nil {
		return nil, err
	}
	return &Browser{pool: pool}, nil
}

// Close tears down every tab in the pool.
func (b *Browser) Close() error { return b.pool.Close() }

// BrowserContext is one task's handle on a browser tab: navigation, CDP
// access, and snapshot config.
type BrowserContext = core.BrowserContext

// Acquire checks out a tab, launching one if the pool is empty. Callers
// must call Release on the result when done.
func (b *Browser) Acquire(ctx context.Context) (BrowserContext, error) {
	return b.pool.Acquire(ctx)
}

// runnerOptions holds knobs set via Option.
type runnerOptions struct {
	maxRepairs int
	cache      *core.ResolutionCache
}

// Option configures a Runner.
type Option func(*runnerOptions)

// WithMaxRepairs bounds how many single-step repairs the runner attempts
// before giving up on a failed action. Default 2.
func WithMaxRepairs(n int) Option {
	return func(o *runnerOptions) { o.maxRepairs = n }
}

// WithResolutionCache attaches a persisted selector-resolution cache at
// path (M4): repeat runs against a stable site skip LLM-driven ref
// resolution entirely once selectors are learned. Empty path keeps the
// cache in memory only.
func WithResolutionCache(path string) Option {
	return func(o *runnerOptions) {
		c := core.NewResolutionCache(path)
		c.Load()
		o.cache = c
	}
}

// Runner ties planner, executor, and repairer together against a given
// LLMClient. It is safe for concurrent use across a browser pool.
type Runner struct {
	inner *core.Runner
}

// NewRunner builds a Runner backed by client, applying opts.
func NewRunner(client LLMClient, opts ...Option) *Runner {
	ro := &runnerOptions{}
	for _, opt := range opts {
		opt(ro)
	}
	r := &core.Runner{LLM: client}
	if ro.maxRepairs > 0 {
		r.MaxRepairs = ro.maxRepairs
	}
	if ro.cache != nil {
		r.Executor = core.NewExecutor(core.WaitStrategy{}).WithCache(ro.cache)
	}
	return &Runner{inner: r}
}

// Run executes t against bctx, returning the task's result, run metrics,
// and an error if the task could not complete.
func (r *Runner) Run(ctx context.Context, bctx BrowserContext, t Task) (any, RunMetrics, error) {
	return r.inner.Run(ctx, bctx, t)
}

// Run is a one-shot convenience wrapper: acquire a tab from b, run t
// against it with client, and release the tab. Prefer NewRunner+Runner.Run
// directly when running many tasks, so Option setup (e.g. a warm resolution
// cache) is paid once.
func Run(ctx context.Context, b *Browser, client LLMClient, t Task, opts ...Option) (any, RunMetrics, error) {
	bctx, err := b.Acquire(ctx)
	if err != nil {
		var m RunMetrics
		return nil, m, fmt.Errorf("acquire browser: %w", err)
	}
	defer bctx.Release()
	return NewRunner(client, opts...).Run(ctx, bctx, t)
}
