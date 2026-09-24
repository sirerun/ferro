package mcp

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// HostedLifecycleController is the narrow control surface needed by a hosted
// process to stay available while work is active and to scale down when idle.
// Implementations own the provider-specific meaning and duration of Protect.
type HostedLifecycleController interface {
	Protect(context.Context, bool) error
	Sleep(context.Context) error
}

// HostedLifecycleOptions sets the idle policy. Zero durations select the
// hosted defaults. Now is injectable so callers can test grace boundaries.
type HostedLifecycleOptions struct {
	StartupGrace    time.Duration
	IdleGrace       time.Duration
	RenewalInterval time.Duration
	CheckInterval   time.Duration
	Now             func() time.Time
}

// HostedLifecycle gates application requests until protection has been
// established, then drains the one hosted owner before asking its controller
// to sleep. It does not wake a process after Sleep; a platform request router
// must arrange that independently.
type HostedLifecycle struct {
	owner       *Owner
	next        http.Handler
	controller  HostedLifecycleController
	opts        HostedLifecycleOptions
	mcpToken    string
	bridgeToken string

	// requests is held for the duration of short application work. Drain uses
	// TryLock so it never queues behind a task that is waiting on /bridge/reply.
	requests        sync.RWMutex
	runMu           sync.Mutex
	mu              sync.Mutex
	ready           bool
	draining        bool
	slept           bool
	started         time.Time
	lastWork        time.Time
	nextRenew       time.Time
	drainErr        error
	beforeDrainGate func() // deterministic interleaving seam for idle admission tests
}

// NewHostedLifecycle wraps an authenticated hosted handler for one extension
// owner. The wrapper intentionally treats /mcp GET event streams and
// /bridge/next long polls as transient admission checks, not active work.
func NewHostedLifecycle(owner *Owner, next http.Handler, controller HostedLifecycleController, options HostedLifecycleOptions) (*HostedLifecycle, error) {
	if owner == nil || owner.bridge == nil || owner.cfg.Backend != "extension" {
		return nil, errors.New("hosted lifecycle requires an extension-backed owner")
	}
	if next == nil {
		return nil, errors.New("hosted lifecycle requires an HTTP handler")
	}
	if controller == nil {
		return nil, errors.New("hosted lifecycle requires a controller")
	}
	mcpToken, err := loadToken(filepath.Join(owner.cfg.Home, "remote-token"))
	if err != nil {
		return nil, fmt.Errorf("load private MCP token for lifecycle admission: %w", err)
	}
	bridgeToken := owner.bridge.Token()
	if len(mcpToken) < 32 || len(bridgeToken) < 32 || mcpToken == bridgeToken {
		return nil, errors.New("hosted lifecycle requires distinct strong transport credentials")
	}
	defaults := HostedLifecycleOptions{
		StartupGrace:    120 * time.Second,
		IdleGrace:       120 * time.Second,
		RenewalInterval: 30 * time.Second,
		CheckInterval:   time.Second,
		Now:             time.Now,
	}
	if options.StartupGrace != 0 {
		defaults.StartupGrace = options.StartupGrace
	}
	if options.IdleGrace != 0 {
		defaults.IdleGrace = options.IdleGrace
	}
	if options.RenewalInterval != 0 {
		defaults.RenewalInterval = options.RenewalInterval
	}
	if options.CheckInterval != 0 {
		defaults.CheckInterval = options.CheckInterval
	}
	if options.Now != nil {
		defaults.Now = options.Now
	}
	if defaults.StartupGrace < 0 || defaults.IdleGrace < 0 || defaults.RenewalInterval <= 0 || defaults.CheckInterval <= 0 {
		return nil, errors.New("hosted lifecycle durations must be non-negative and intervals must be positive")
	}
	return &HostedLifecycle{owner: owner, next: next, controller: controller, opts: defaults, mcpToken: mcpToken, bridgeToken: bridgeToken}, nil
}

// Handler returns the readiness and activity-gated HTTP handler.
func (l *HostedLifecycle) Handler() http.Handler { return l }

// Run first protects the owner, then renews that protection and checks for
// idle drain opportunities. It returns only after cancellation or terminal
// drain. A failed renewal closes admission immediately and still waits for
// active work to clear before attempting sleep.
func (l *HostedLifecycle) Run(ctx context.Context) error {
	if !l.runMu.TryLock() {
		return errors.New("hosted lifecycle is already running")
	}
	defer l.runMu.Unlock()
	if err := l.establishProtection(ctx); err != nil {
		return err
	}

	ticker := time.NewTicker(l.opts.CheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			done, err := l.step(ctx)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
		}
	}
}

func (l *HostedLifecycle) establishProtection(ctx context.Context) error {
	if err := l.controller.Protect(ctx, true); err != nil {
		return fmt.Errorf("protect hosted owner before readiness: %w", err)
	}
	now := l.opts.Now()
	l.mu.Lock()
	if l.started.IsZero() {
		l.started = now
		l.lastWork = now
		l.nextRenew = now.Add(l.opts.RenewalInterval)
		l.ready = true
	}
	l.mu.Unlock()
	return nil
}

func (l *HostedLifecycle) step(ctx context.Context) (bool, error) {
	now := l.opts.Now()
	l.mu.Lock()
	if l.started.IsZero() {
		l.mu.Unlock()
		return false, errors.New("hosted lifecycle has not established initial protection")
	}
	draining, slept, nextRenew := l.draining, l.slept, l.nextRenew
	l.mu.Unlock()
	if slept {
		return true, nil
	}
	if draining {
		return l.finishDrain(ctx)
	}
	if !now.Before(nextRenew) {
		if err := l.controller.Protect(ctx, true); err != nil {
			l.beginDrain(err)
			return l.finishDrain(ctx)
		}
		l.mu.Lock()
		l.nextRenew = now.Add(l.opts.RenewalInterval)
		l.mu.Unlock()
	}
	if now.Before(l.started.Add(l.opts.StartupGrace)) {
		return false, nil
	}
	l.mu.Lock()
	idleSince := l.lastWork
	l.mu.Unlock()
	if now.Sub(idleSince) < l.opts.IdleGrace {
		return false, nil
	}
	if l.beforeDrainGate != nil {
		l.beforeDrainGate()
	}
	if !l.requests.TryLock() {
		return false, nil
	}
	// Recheck after fencing admitted requests. Without this second sample, a
	// request can complete between the preliminary idle check and gate
	// acquisition, leaving a stale timestamp to trigger drain.
	now = l.opts.Now()
	l.mu.Lock()
	idleSince = l.lastWork
	l.mu.Unlock()
	if now.Sub(idleSince) < l.opts.IdleGrace {
		l.requests.Unlock()
		return false, nil
	}
	// The exclusive request gate fences every accepted application request.
	// Recheck owner and bridge state after acquiring it, then close admission
	// before releasing the gate.
	if !l.ownerAndBridgeIdle(now) {
		l.requests.Unlock()
		return false, nil
	}
	l.mu.Lock()
	l.draining = true
	l.ready = false
	l.mu.Unlock()
	l.requests.Unlock()
	return l.finishDrain(ctx)
}

func (l *HostedLifecycle) ownerAndBridgeIdle(now time.Time) bool {
	l.owner.mu.Lock()
	ownerBusy := l.owner.activeOwner != "" || l.owner.activeCancel != nil || (l.owner.leaseOwner != "" && now.Before(l.owner.leaseUntil))
	l.owner.mu.Unlock()
	if ownerBusy || l.owner.bridge.HasPendingCommands() {
		return false
	}
	return !l.owner.bridge.Connected()
}

func (l *HostedLifecycle) beginDrain(reason error) {
	l.mu.Lock()
	if !l.draining {
		l.draining = true
		l.ready = false
		l.drainErr = reason
	}
	l.mu.Unlock()
}

func (l *HostedLifecycle) finishDrain(ctx context.Context) (bool, error) {
	if !l.requests.TryLock() {
		return false, nil
	}
	l.mu.Lock()
	reason, slept := l.drainErr, l.slept
	l.mu.Unlock()
	if slept {
		l.requests.Unlock()
		return true, reason
	}
	if !l.ownerAndBridgeIdle(l.opts.Now()) {
		l.requests.Unlock()
		return false, nil
	}
	// Admission is already closed by draining. Release the activity gate before
	// control-plane calls so health/SSE admission never waits on scale latency.
	l.requests.Unlock()
	protectErr := l.controller.Protect(ctx, false)
	sleepErr := l.controller.Sleep(ctx)
	l.mu.Lock()
	l.slept = sleepErr == nil
	l.ready = false
	l.mu.Unlock()
	if protectErr != nil || sleepErr != nil || reason != nil {
		return sleepErr == nil, errors.Join(wrapIf("release hosted protection", protectErr), wrapIf("sleep hosted owner", sleepErr), reason)
	}
	return true, nil
}

func wrapIf(message string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
}

func (l *HostedLifecycle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
		response := &healthReadinessWriter{ResponseWriter: w, lifecycle: l}
		l.next.ServeHTTP(response, r)
		response.finish()
		return
	}

	if r.URL.Path == "/mcp" && r.Method == http.MethodGet {
		if !l.authorized(r, l.mcpToken) {
			l.next.ServeHTTP(w, r)
			return
		}
		if !l.admitTransient(w) {
			return
		}
		l.next.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/bridge/next" && r.Method == http.MethodGet {
		if !l.authorized(r, l.bridgeToken) {
			l.next.ServeHTTP(w, r)
			return
		}
		if !l.admitTransientBridgePoll(w, r) {
			return
		}
		l.next.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/bridge/chat/status" && r.Method == http.MethodPost {
		if !l.authorized(r, l.bridgeToken) {
			l.next.ServeHTTP(w, r)
			return
		}
		if !l.admitTransient(w) {
			return
		}
		l.next.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/mcp" && r.Method == http.MethodPost && l.authorized(r, l.mcpToken) && mcpRequestIsTransient(r) {
		if !l.admitTransient(w) {
			return
		}
		l.next.ServeHTTP(w, r)
		return
	}

	active := r.URL.Path == "/mcp" && r.Method == http.MethodPost || strings.HasPrefix(r.URL.Path, "/bridge/") && !(r.URL.Path == "/bridge/next" && r.Method == http.MethodGet)
	if !active {
		l.next.ServeHTTP(w, r)
		return
	}
	token := l.mcpToken
	if strings.HasPrefix(r.URL.Path, "/bridge/") {
		token = l.bridgeToken
	}
	if !l.authorized(r, token) {
		// Let the existing transport handler produce its normal denial, but
		// invalid credentials never acquire activity admission or reset idle.
		l.next.ServeHTTP(w, r)
		return
	}
	l.requests.RLock()
	if !l.isReady() && !l.allowsDrainTransport(r) {
		l.requests.RUnlock()
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	l.markWork()
	defer func() {
		l.markWork()
		l.requests.RUnlock()
	}()
	l.next.ServeHTTP(w, r)
}

const maxLifecycleMCPBodyBytes int64 = 2 << 20

// mcpRequestIsTransient identifies protocol and status traffic that must not
// refresh the service idle clock. It reads no more than the same 2 MiB body
// limit enforced by remoteHandler and restores every consumed byte before the
// SDK sees the request. Oversize, malformed, and unrecognized requests remain
// ordinary application activity; this classifier never rejects input.
func mcpRequestIsTransient(r *http.Request) bool {
	if r.Body == nil || r.ContentLength > maxLifecycleMCPBodyBytes {
		return false
	}
	original := r.Body
	body, err := io.ReadAll(io.LimitReader(original, maxLifecycleMCPBodyBytes+1))
	r.Body = &replayedRequestBody{Reader: io.MultiReader(bytes.NewReader(body), original), original: original}
	if err != nil || int64(len(body)) > maxLifecycleMCPBodyBytes {
		return false
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return false
	}
	if body[0] == '[' {
		var messages []json.RawMessage
		if json.Unmarshal(body, &messages) != nil || len(messages) == 0 {
			return false
		}
		for _, message := range messages {
			if !transientMCPMessage(message) {
				return false
			}
		}
		return true
	}
	return transientMCPMessage(body)
}

type replayedRequestBody struct {
	io.Reader
	original io.ReadCloser
}

func (b *replayedRequestBody) Close() error { return b.original.Close() }

func transientMCPMessage(raw json.RawMessage) bool {
	var message struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(raw, &message) != nil || message.Method == "" {
		return false
	}
	switch {
	case message.Method == "ping", message.Method == "initialize", strings.HasPrefix(message.Method, "notifications/"):
		return true
	case message.Method == "tools/call":
		var params struct {
			Name string `json:"name"`
		}
		return json.Unmarshal(message.Params, &params) == nil && params.Name == "browser_status"
	default:
		return false
	}
}

// healthReadinessWriter preserves host/TLS middleware decisions in the
// wrapped handler while downgrading a successful health response when this
// lifecycle is not ready. The health endpoint is small and is not activity
// tracked.
type healthReadinessWriter struct {
	http.ResponseWriter
	lifecycle *HostedLifecycle
	wrote     bool
	blocked   bool
}

func (w *healthReadinessWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.wrote = true
	if status == http.StatusOK && !w.lifecycle.isReady() {
		w.blocked = true
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.ResponseWriter.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.ResponseWriter.Write([]byte("not ready\n"))
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *healthReadinessWriter) Write(body []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if w.blocked {
		return len(body), nil
	}
	return w.ResponseWriter.Write(body)
}

func (w *healthReadinessWriter) finish() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
}

func (l *HostedLifecycle) admitTransient(w http.ResponseWriter) bool {
	// This short read lock makes readiness admission atomic with drain. It is
	// released before serving SSE or a long poll, so either stream may outlive
	// activity tracking without preventing the owner from draining.
	l.requests.RLock()
	defer l.requests.RUnlock()
	if !l.isReady() {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (l *HostedLifecycle) admitTransientBridgePoll(w http.ResponseWriter, r *http.Request) bool {
	l.requests.RLock()
	defer l.requests.RUnlock()
	if !l.isReady() && !l.allowsDrainTransport(r) {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (l *HostedLifecycle) allowsDrainTransport(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/bridge/") {
		return false
	}
	l.mu.Lock()
	renewalFailed := l.draining && l.drainErr != nil && !l.slept
	l.mu.Unlock()
	if !renewalFailed || r.URL.Path != "/bridge/next" && r.URL.Path != "/bridge/reply" {
		return false
	}
	if r.URL.Path == "/bridge/next" && r.Method != http.MethodGet || r.URL.Path == "/bridge/reply" && r.Method != http.MethodPost {
		return false
	}
	if l.owner.bridge.HasPendingCommands() {
		return true
	}
	l.owner.mu.Lock()
	active := l.owner.activeOwner != "" || l.owner.activeCancel != nil
	l.owner.mu.Unlock()
	return active
}

func (l *HostedLifecycle) authorized(r *http.Request, token string) bool {
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) == 1
}

func (l *HostedLifecycle) isReady() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ready && !l.draining && !l.slept
}

func (l *HostedLifecycle) markWork() {
	l.mu.Lock()
	l.lastWork = l.opts.Now()
	l.mu.Unlock()
}
