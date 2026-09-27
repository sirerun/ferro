package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sirerun/ferro/internal/extbridge"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sirerun/ferro"
	"github.com/sirerun/ferro/internal/core"
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

	mu           sync.Mutex    // protects lease and active-call state
	gate         chan struct{} // cancelable serialization of browser calls
	leaseOwner   string
	leaseUntil   time.Time
	activeOwner  string
	activeCancel context.CancelFunc
	driver       core.PageDriver
	bridge       *extbridge.Bridge
	remoteServer *http.Server
	remoteAddr   string
	receipts     ReceiptStore
	taskCaches   map[string]*core.ResolutionCache // serialized by gate
	replayEpoch  string

	// exec, snap, and extracted back the primitive tools (T11.4): exec runs
	// one Action at a time via ExecuteOne; snap is the last snapshot taken
	// (nil until the client calls snapshot, or after navigate, since refs
	// are page-specific), and resolves refs for click/fill/select/extract;
	// extracted persists {{extract.last...}} state across primitive calls,
	// the same templating vocabulary run_task's plans use.
	exec           *core.Executor
	snap           *core.Snapshot
	snapGeneration uint64
	extracted      map[string]any

	listener net.Listener

	stopOnce sync.Once
	stopCh   chan struct{}
}

// NewOwner launches the browser pool, acquires the single shared tab, and
// builds the runner. ctx must be the process's own lifetime context (not a
// short-lived per-call context) — chromedp ties the launched Chrome
// process's lifetime to it.
func NewOwner(ctx context.Context, cfg Config) (*Owner, error) {
	if cfg.Backend == "" {
		cfg.Backend = "cdp"
	}
	if cfg.Backend != "cdp" && cfg.Backend != "extension" {
		return nil, fmt.Errorf("invalid backend %q", cfg.Backend)
	}
	if cfg.BlockTimeout <= 0 {
		cfg.BlockTimeout = 15 * time.Minute
	}
	if cfg.MaxElements <= 0 {
		cfg.MaxElements = 200
	}
	if err := os.MkdirAll(cfg.Home, 0700); err != nil {
		return nil, err
	}
	allow, err := NewAllowlist(cfg.AllowlistPath())
	if err != nil {
		return nil, err
	}
	o := &Owner{cfg: cfg, allow: allow, gate: make(chan struct{}, 1), extracted: map[string]any{}, stopCh: make(chan struct{})}
	if cfg.Backend == "extension" {
		token, err := loadToken(filepath.Join(cfg.Home, "bridge-token"))
		if err != nil {
			return nil, err
		}
		b, err := extbridge.New(extbridge.WithToken(token), extbridge.WithChatHandler(o.chatHandler()))
		if err != nil {
			return nil, err
		}
		addr := cfg.BridgeAddr
		if addr == "" {
			addr = "127.0.0.1:4173"
		}
		if err = b.Start(addr); err != nil {
			return nil, err
		}
		o.bridge = b
		o.driver = &extbridge.ExtensionDriver{Bridge: b, CheckURL: func(raw string) error { return o.checkOrigin(context.Background(), raw) }}
		log.Printf("ferro-mcp: extension bridge http://%s; pairing token is in bridge-token under FERRO_MCP_HOME", b.Addr())
	} else {
		b, err := ferro.NewBrowser(ferro.BrowserConfig{Headless: cfg.Headless, UserDataDir: cfg.UserDataDir, ProfileDirectory: cfg.ProfileDirectory, MaxElements: cfg.MaxElements})
		if err != nil {
			return nil, err
		}
		tab, err := b.Acquire(ctx)
		if err != nil {
			_ = b.Close()
			return nil, fmt.Errorf("acquire Chrome tab (use a dedicated non-default profile): %w", err)
		}
		o.browser = b
		o.tab = tab
		o.driver = core.NewChromedpDriver(core.WaitStrategy{})
	}
	// The extension authorizes the exact URL it sends with each command.
	// CDP needs a decorator to apply the same policy to every driver action.
	if cfg.Backend != "extension" {
		o.driver = &guardedDriver{PageDriver: o.driver, owner: o}
	}
	o.exec = core.NewExecutor(core.WaitStrategy{}).WithDriver(o.driver)
	var client ferro.LLMClient
	if cfg.LLMBaseURL != "" && cfg.LLMModel != "" {
		client = &ferro.OpenAICompatible{BaseURL: cfg.LLMBaseURL, Model: cfg.LLMModel, APIKey: cfg.LLMAPIKey}
	}
	opts := []ferro.Option{ferro.WithMaxRepairs(cfg.MaxRepairs)}
	if cfg.CachePath != "" {
		opts = append(opts, ferro.WithResolutionCache(cfg.CachePath))
	}
	o.runner = ferro.NewRunner(client, opts...)
	if err := o.initializeTasks(); err != nil {
		_ = o.Close()
		return nil, err
	}
	if cfg.StartURL != "" && cfg.Backend == "cdp" {
		runCtx, cancel := o.actionCtx(ctx)
		err = o.driver.Navigate(runCtx, cfg.StartURL)
		cancel()
		if err != nil {
			log.Printf("initial navigation to %q failed; service will continue: %v", cfg.StartURL, err)
		}
	}
	if cfg.StartURL != "" && cfg.Backend == "extension" {
		log.Printf("FERRO_MCP_START_URL is ignored with the extension backend; navigate explicitly after pairing")
	}
	if cfg.Remote {
		if err := o.startRemote(ctx); err != nil {
			_ = o.Close()
			return nil, err
		}
	}
	return o, nil
}

// Close releases the shared tab and tears down the browser pool.
func (o *Owner) Close() error {
	o.requestStop()
	if o.listener != nil {
		_ = o.listener.Close()
	}
	o.mu.Lock()
	if o.activeCancel != nil {
		o.activeCancel()
	}
	o.mu.Unlock()
	var errs []error
	if o.remoteServer != nil {
		errs = append(errs, o.remoteServer.Close())
	}
	if o.bridge != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		errs = append(errs, o.bridge.Stop(ctx))
		cancel()
	}
	if o.tab != nil {
		o.tab.Release()
	}
	if o.browser != nil {
		errs = append(errs, o.browser.Close())
	}
	if o.receipts != nil {
		errs = append(errs, o.receipts.Close())
	}
	return errors.Join(errs...)
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
		select {
		case <-ctx.Done():
		case <-o.stopCh:
		}
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
		state, _, _ := o.control(ctx, "browser_status")
		resp = relayResponse{Text: fmt.Sprintf("owner pid=%d socket=%s\n%s", os.Getpid(), o.cfg.SocketPath(), state)}
		_ = json.NewEncoder(conn).Encode(resp)
	case controlStop:
		_ = json.NewEncoder(conn).Encode(relayResponse{Text: "stopping"})
		o.requestStop()
	default:
		callCtx, cancel := context.WithCancel(context.WithValue(ctx, clientKey{}, req.Client))
		defer cancel()
		// Each relay connection carries one request; EOF means client cancellation.
		// relayCall writes a single JSON value without a trailing newline. The
		// next byte therefore means the client closed its connection to cancel;
		// the response path remains independent in the opposite direction.
		go func() { var buf [1]byte; _, _ = conn.Read(buf[:]); cancel() }()
		text, isError, err := o.Call(callCtx, req.Tool, req.Args)
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
	select {
	case <-o.stopCh:
		return stopResult("disconnected", "service is stopping")
	default:
	}
	if tool == "run_task" && hasAdvancedTaskFields(args) && !hasTaskID(args) {
		return stopResult("invalid_input", "receipt-backed fields require task_id; no browser work was started")
	}
	if tool == "browser_status" || tool == "cancel_task" {
		return o.control(ctx, tool)
	}
	if tool == "get_task_receipt" || tool == "read_task_artifact" || tool == "cleanup_task_receipts" || tool == "list_model_profiles" {
		result, err := o.taskControl(ctx, tool, args)
		if err != nil {
			return err.Error(), true, nil
		}
		data, err := json.Marshal(result)
		if err != nil {
			return "", false, err
		}
		return string(data), false, nil
	}
	if tool == "run_task" && hasTaskID(args) && o.receipts != nil {
		if in, err := ValidateTaskRequest(args); err == nil {
			digest, digestErr := canonicalRequestDigest(in)
			if digestErr != nil {
				return digestErr.Error(), true, nil
			}
			previous, lookupErr := o.receipts.Lookup(ctx, privateReceiptOwner, in.TaskID)
			if lookupErr == nil {
				if previous.RequestDigest != digest {
					return ErrReceiptConflict.Error(), true, nil
				}
				data, marshalErr := json.Marshal(receiptResponse(previous))
				if marshalErr != nil {
					return "", false, marshalErr
				}
				return string(data), false, nil
			}
			if !errors.Is(lookupErr, ErrReceiptNotFound) {
				return lookupErr.Error(), true, nil
			}
		}
	}
	select {
	case o.gate <- struct{}{}:
	case <-ctx.Done():
		return "", false, ctx.Err()
	}
	defer func() { <-o.gate }()
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	who := clientIdentity(ctx)
	if tool == "acquire_tab" || tool == "release_tab" {
		return o.lease(who, tool, args)
	}
	o.mu.Lock()
	if o.leaseOwner != "" && time.Now().After(o.leaseUntil) {
		o.leaseOwner = ""
		o.snap = nil
		o.snapGeneration = 0
		o.extracted = map[string]any{}
	}
	if o.leaseOwner != "" && o.leaseOwner != who {
		o.mu.Unlock()
		return stopResult("tab_busy", "another agent owns this tab; retry after its lease expires")
	}
	if o.cfg.Backend == "extension" && tool != "run_task" && o.leaseOwner == "" {
		o.mu.Unlock()
		return stopResult("lease_required", "call acquire_tab before direct browser tools; release_tab when finished")
	}
	runCtx, cancel := context.WithTimeout(ctx, o.cfg.BlockTimeout)
	o.activeOwner = who
	o.activeCancel = cancel
	o.mu.Unlock()
	defer func() { cancel(); o.mu.Lock(); o.activeOwner = ""; o.activeCancel = nil; o.mu.Unlock() }()
	ctx = runCtx
	if o.bridge != nil {
		ctx = o.bridge.Pin(ctx)
	}
	result, err := o.dispatch(ctx, tool, args)
	if err != nil {
		var stopped *core.StopError
		if errors.As(err, &stopped) {
			return stopResult(stopped.Code, stopped.Message)
		}
		return err.Error(), true, nil
	}
	var body []byte
	if tool == "run_task" && hasTaskID(args) {
		body, err = json.Marshal(result)
	} else {
		body, err = json.MarshalIndent(result, "", "  ")
	}
	if err != nil {
		return "", false, fmt.Errorf("marshal %s result: %w", tool, err)
	}
	return string(body), false, nil
}

func hasTaskID(args json.RawMessage) bool {
	var request struct {
		TaskID string `json:"task_id"`
	}
	return json.Unmarshal(args, &request) == nil && request.TaskID != ""
}

func hasAdvancedTaskFields(args json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil {
		return false // The ordinary request decoder reports malformed JSON.
	}
	for field := range fields {
		for _, name := range []string{"task_id", "schema", "policy", "output_schema", "limits", "replay_key", "evidence"} {
			if strings.EqualFold(field, name) {
				return true
			}
		}
	}
	return false
}

func (o *Owner) dispatch(ctx context.Context, tool string, args json.RawMessage) (any, error) {
	switch tool {
	case "run_task":
		if hasTaskID(args) {
			return o.runTaskWithReceipt(ctx, args)
		}
		return o.runTaskSimple(ctx, args)
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
