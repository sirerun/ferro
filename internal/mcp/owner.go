package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"

	"github.com/dndungu/ferro"
	"github.com/dndungu/ferro/internal/core"
)

// Owner holds the real ferro.Browser pool and the single shared tab (ADR
// 004: "one shared tab... serialized through one mutex"). Every tool call —
// from its own stdio client, or relayed from a shim over the Unix socket —
// funnels through Call, which holds mu for the call's whole duration.
type Owner struct {
	cfg     Config
	browser *ferro.Browser
	tab     ferro.BrowserContext
	runner  *ferro.Runner
	allow   *Allowlist

	mu sync.Mutex // serializes every tool call against the shared tab

	// exec, snap, and extracted back the primitive tools (T11.4): exec runs
	// one Action at a time via ExecuteOne; snap is the last snapshot taken
	// (nil until the client calls snapshot, or after navigate, since refs
	// are page-specific), and resolves refs for click/fill/select/extract;
	// extracted persists {{extract.last...}} state across primitive calls,
	// the same templating vocabulary run_task's plans use.
	exec      *core.Executor
	snap      *core.Snapshot
	extracted map[string]any

	listener net.Listener

	stopOnce sync.Once
	stopCh   chan struct{}
}

// NewOwner launches the browser pool, acquires the single shared tab, and
// builds the runner. ctx must be the process's own lifetime context (not a
// short-lived per-call context) — chromedp ties the launched Chrome
// process's lifetime to it.
func NewOwner(ctx context.Context, cfg Config) (*Owner, error) {
	b, err := ferro.NewBrowser(ferro.BrowserConfig{
		Headless:         cfg.Headless,
		UserDataDir:      cfg.UserDataDir,
		ProfileDirectory: cfg.ProfileDirectory,
		MaxElements:      cfg.MaxElements,
	})
	if err != nil {
		return nil, fmt.Errorf("start browser pool: %w", err)
	}

	tab, err := b.Acquire(ctx)
	if err != nil {
		_ = b.Close()
		return nil, fmt.Errorf(
			"acquire tab against Chrome data dir %q: %w — either it's open in another running "+
				"Chrome process (quit that Chrome, then retry), or it resolves to a real Chrome "+
				"install's default profile location, which Chrome refuses to enable CDP against no "+
				"matter what (point FERRO_MCP_CHROME_USER_DATA_DIR at a dedicated, non-default "+
				"directory instead)",
			cfg.UserDataDir, err,
		)
	}

	if cfg.StartURL != "" {
		if err := tab.Navigate(cfg.StartURL); err != nil {
			log.Printf("ferro-mcp: initial navigation to %s failed (continuing): %v", cfg.StartURL, err)
		}
	}

	allow, err := NewAllowlist(cfg.AllowlistPath())
	if err != nil {
		tab.Release()
		_ = b.Close()
		return nil, fmt.Errorf("load allowlist: %w", err)
	}

	client := &ferro.OpenAICompatible{BaseURL: cfg.LLMBaseURL, Model: cfg.LLMModel, APIKey: cfg.LLMAPIKey}
	opts := []ferro.Option{ferro.WithMaxRepairs(cfg.MaxRepairs)}
	if cfg.CachePath != "" {
		opts = append(opts, ferro.WithResolutionCache(cfg.CachePath))
	}

	return &Owner{
		cfg:       cfg,
		browser:   b,
		tab:       tab,
		runner:    ferro.NewRunner(client, opts...),
		allow:     allow,
		exec:      core.NewExecutor(core.WaitStrategy{}),
		extracted: map[string]any{},
		stopCh:    make(chan struct{}),
	}, nil
}

// Close releases the shared tab and tears down the browser pool.
func (o *Owner) Close() error {
	if o.listener != nil {
		_ = o.listener.Close()
	}
	o.tab.Release()
	return o.browser.Close()
}

// StopRequested reports a signal that fires once an explicit shutdown was
// asked for (ferro-mcp stop over the socket). main() blocks on this to
// decide when to actually tear down the owner (see T11.6: the owner must
// keep serving other clients after its own stdio disconnects, so an
// stdio-EOF alone must never fire this).
func (o *Owner) StopRequested() <-chan struct{} { return o.stopCh }

func (o *Owner) requestStop() {
	o.stopOnce.Do(func() { close(o.stopCh) })
}

// ServeSocketBackground starts listening on cfg.SocketPath() and serves
// relayed calls from shims in background goroutines until ctx is done. It
// returns once the listener is ready, so a caller that immediately signals
// other processes (or lets them race to dial) will not lose the race.
func (o *Owner) ServeSocketBackground(ctx context.Context) error {
	if err := os.MkdirAll(o.cfg.Home, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", o.cfg.Home, err)
	}
	sockPath := o.cfg.SocketPath()
	// Unix domain socket paths are capped around 104 bytes (macOS/BSD
	// sockaddr_un.sun_path) to 108 (Linux); a long $FERRO_MCP_HOME blows
	// that limit and net.Listen fails with an opaque "bind: invalid
	// argument". Catch it here with an actionable message instead.
	if len(sockPath) > 100 {
		return fmt.Errorf("FERRO_MCP_HOME %q is too long: %s would exceed the Unix domain socket path limit (~104 bytes on macOS/BSD, 108 on Linux); point FERRO_MCP_HOME at a shorter path", o.cfg.Home, sockPath)
	}
	// Any existing socket file at this path is stale: we only reach here
	// after winning the exclusive flock, so no other owner can be live.
	_ = os.Remove(sockPath)
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", sockPath, err)
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		_ = l.Close()
		return fmt.Errorf("chmod %s: %w", sockPath, err)
	}
	o.listener = l

	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()
	go o.acceptLoop(ctx, l)
	return nil
}

func (o *Owner) acceptLoop(ctx context.Context, l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return // listener closed: shutdown or fatal accept error either way
		}
		go o.serveConn(ctx, conn)
	}
}

// serveConn handles exactly one relay request: decode, dispatch, encode the
// response, close. ADR 004 deliberately uses one connection per call rather
// than a persistent multiplexed session — simpler, and a stuck call from
// one shim can never wedge another shim's connection.
func (o *Owner) serveConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()

	var req relayRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return
	}

	var resp relayResponse
	switch req.Tool {
	case controlStatus:
		resp = relayResponse{Text: fmt.Sprintf("owner pid=%d socket=%s", os.Getpid(), o.cfg.SocketPath())}
		_ = json.NewEncoder(conn).Encode(resp)
	case controlStop:
		_ = json.NewEncoder(conn).Encode(relayResponse{Text: "stopping"})
		o.requestStop()
	default:
		text, isError, err := o.Call(ctx, req.Tool, req.Args)
		if err != nil {
			resp = relayResponse{Text: err.Error(), IsError: true}
		} else {
			resp = relayResponse{Text: text, IsError: isError}
		}
		_ = json.NewEncoder(conn).Encode(resp)
	}
}

// Call implements caller. It is used both for the owner's own
// stdio-registered tools and, via serveConn, for calls relayed from shims —
// either way it holds mu for the call's whole duration, serializing every
// tool call against the one shared tab (ADR 004).
func (o *Owner) Call(ctx context.Context, tool string, args json.RawMessage) (string, bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	result, err := o.dispatch(ctx, tool, args)
	if err != nil {
		return err.Error(), true, nil
	}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", false, fmt.Errorf("marshal %s result: %w", tool, err)
	}
	return string(body), false, nil
}

func (o *Owner) dispatch(ctx context.Context, tool string, args json.RawMessage) (any, error) {
	switch tool {
	case "run_task":
		return o.runTask(ctx, args)
	case "snapshot":
		return o.snapshot(ctx, args)
	case "navigate":
		return o.navigate(ctx, args)
	case "click":
		return o.click(ctx, args)
	case "fill":
		return o.fill(ctx, args)
	case "select":
		return o.selectOption(ctx, args)
	case "key":
		return o.key(ctx, args)
	case "scroll":
		return o.scroll(ctx, args)
	case "wait":
		return o.wait(ctx, args)
	case "extract":
		return o.extract(ctx, args)
	default:
		return nil, fmt.Errorf("unknown tool %q", tool)
	}
}
