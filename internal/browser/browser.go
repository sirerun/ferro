// Package browser is the chromedp tab pool: warm, reusable browser contexts
// that satisfy core.BrowserContext. Cold Chrome launch is 300-1500ms;
// pooling removes it from every task after the first wave.
//
// Context lifetime: whichever context is passed to the *first*
// chromedp.Run call on a tab owns that tab's CDP event-listener goroutine
// for the tab's entire lifetime. Wrapping that context (context.WithTimeout,
// context.WithCancel) and later cancelling the wrapper — even after a
// successful call — tears the whole session down, and every subsequent Run
// on the tab then fails with "invalid context" or "context canceled".
// newTab below enforces its launch deadline with a select on a goroutine
// instead of a WithTimeout wrapper for exactly this reason, and only ever
// passes the bare pooledContext.cdpCtx to that first Run. See
// docs/adr/001-chromedp-context-lifetime.md.
package browser

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/dndungu/ferro/internal/core"
)

// Browser is a pool of allocated-but-idle chromedp contexts.
type Browser struct {
	cfg    Config
	mu     sync.Mutex
	idle   []*pooledContext
	all    map[*pooledContext]struct{}
	closed bool
}

// Config configures the pool and each tab it launches.
type Config struct {
	Headless        bool
	PoolSize        int           // default 8
	MaxElements     int           // snapshot cap, default 200 (RFC §4.1)
	AllocateTimeout time.Duration // default 10s
	// ExecPath, Proxy, UserAgent flags pass through to chromedp flags.
	ExecPath  string
	Proxy     string
	UserAgent string
	// UserDataDir, when set, launches Chrome against a persistent profile
	// directory instead of an ephemeral one — cookies/session survive across
	// runs, so a human can sign in once (headful, outside automation) and
	// subsequent runs stay logged in. Never automate the sign-in itself.
	UserDataDir string
	// ProfileDirectory selects which profile within UserDataDir to launch
	// (Chrome's --profile-directory flag, e.g. "Default" or "Profile 1").
	// Only meaningful alongside UserDataDir; most callers should just point
	// UserDataDir at a dedicated directory per profile instead.
	ProfileDirectory string
}

type pooledContext struct {
	parentCtx context.Context
	cdpCtx    context.Context // cancel to destroy the tab
	cancel    context.CancelFunc
	busy      bool
}

func New(cfg Config) (*Browser, error) {
	if cfg.PoolSize <= 0 {
		cfg.PoolSize = 8
	}
	if cfg.MaxElements <= 0 {
		cfg.MaxElements = 200
	}
	b := &Browser{
		cfg: cfg,
		all: make(map[*pooledContext]struct{}, cfg.PoolSize),
	}
	// Warm the pool lazily on first Acquire rather than eagerly — avoids
	// paying launch cost when the binary starts idle.
	return b, nil
}

// newTab builds one chromedp context. Each tab gets its own allocator target
// under a shared process — chromedp reuses running Chrome.
func (b *Browser) newTab(ctx context.Context) (*pooledContext, error) {
	opts := []chromedp.ExecAllocatorOption{
		// chromedp.DefaultExecAllocatorOptions minus a few paranoid flags;
		// listed explicitly so the browser fingerprint is deliberate.
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("headless", b.cfg.Headless),
		chromedp.Flag("disable-gpu", b.cfg.Headless),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("background-networking", false),
	}
	if b.cfg.ExecPath != "" {
		opts = append(opts, chromedp.ExecPath(b.cfg.ExecPath))
	}
	if b.cfg.Proxy != "" {
		opts = append(opts, chromedp.ProxyServer(b.cfg.Proxy))
	}
	if b.cfg.UserAgent != "" {
		opts = append(opts, chromedp.UserAgent(b.cfg.UserAgent))
	}
	if b.cfg.UserDataDir != "" {
		opts = append(opts, chromedp.UserDataDir(b.cfg.UserDataDir))
		// A persistent profile means a real login session; disable the
		// "Chrome is being controlled by automated test software" signal
		// some sites gate on.
		opts = append(opts, chromedp.Flag("disable-blink-features", "AutomationControlled"))
	}
	if b.cfg.ProfileDirectory != "" {
		opts = append(opts, chromedp.Flag("profile-directory", b.cfg.ProfileDirectory))
	}

	parent, cancelParent := chromedp.NewExecAllocator(ctx, opts...)
	cdp, cancelTab := chromedp.NewContext(parent)

	// Force eager launch so Acquire pays the cost, not the first action.
	// The context passed to chromedp's *first* Run() call governs the CDP
	// event-listener goroutine for the tab's whole lifetime — cancelling it
	// (even after a successful launch) kills the session outright. So the
	// launch deadline is enforced with a select, not a WithTimeout wrapper
	// around cdp: cdp itself is only ever passed to Run unwrapped.
	launchErr := make(chan error, 1)
	go func() { launchErr <- chromedp.Run(cdp) }()
	select {
	case err := <-launchErr:
		if err != nil {
			cancelTab()
			cancelParent()
			return nil, fmt.Errorf("launch: %w", err)
		}
	case <-time.After(b.cfg.allocateTimeoutOrDefault()):
		cancelTab()
		cancelParent()
		return nil, fmt.Errorf("launch: timed out after %s", b.cfg.allocateTimeoutOrDefault())
	}

	return &pooledContext{
		parentCtx: parent,
		cdpCtx:    cdp,
		cancel: func() {
			cancelTab()
			cancelParent()
		},
	}, nil
}

func (c Config) allocateTimeoutOrDefault() time.Duration {
	if c.AllocateTimeout > 0 {
		return c.AllocateTimeout
	}
	return 10 * time.Second
}

// Acquire checks out a tab, launching a new one only if the idle pool is
// empty.
func (b *Browser) Acquire(ctx context.Context) (core.BrowserContext, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, fmt.Errorf("browser closed")
	}
	if n := len(b.idle); n > 0 {
		pc := b.idle[n-1]
		b.idle = b.idle[:n-1]
		pc.busy = true
		b.mu.Unlock()
		return &taskContext{pc: pc, b: b, maxElements: b.cfg.MaxElements}, nil
	}
	// Note: in a strict pool we'd count live tabs vs PoolSize here; v0.1
	// allows temporary over-allocation and relies on Release returning tabs
	// to idle. PoolSize governs pre-warming, not a hard cap. (Documented in
	// the kazi work plan as a deferred hardening item.)
	b.mu.Unlock()

	pc, err := b.newTab(ctx)
	if err != nil {
		return nil, err
	}
	pc.busy = true
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		pc.cancel()
		return nil, fmt.Errorf("browser closed during acquisition")
	}
	b.all[pc] = struct{}{}
	b.mu.Unlock()
	return &taskContext{pc: pc, b: b, maxElements: b.cfg.MaxElements}, nil
}

// Close tears down every tab. In-flight tasks get cancelled.
func (b *Browser) Close() error {
	b.mu.Lock()
	b.closed = true
	pcs := make([]*pooledContext, 0, len(b.all))
	for pc := range b.all {
		pcs = append(pcs, pc)
	}
	b.all = nil
	b.idle = nil
	b.mu.Unlock()
	for _, pc := range pcs {
		pc.cancel()
	}
	return nil
}
