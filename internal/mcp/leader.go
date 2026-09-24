package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
)

// Leader implements ADR 004's leader election: exactly one ferro-mcp
// process per $FERRO_MCP_HOME ever opens Chrome — the owner, decided by an
// exclusive flock on cfg.LockPath(), not by a flag. Every other process (a
// shim) relays tool calls to the owner over cfg.SocketPath(). Both roles
// register the identical tool set (server.go) on their own stdio, so an MCP
// client cannot tell which role it talked to.
type Leader struct {
	cfg Config

	// rootCtx is the process's own lifetime context, captured once at
	// construction. tryBecomeOwner always launches a new Owner against this
	// context, never against a tool call's short-lived context — chromedp
	// ties the launched Chrome process's lifetime to whatever context
	// allocated it, so using a per-call context here would kill the browser
	// the moment that one call's context ended.
	rootCtx context.Context

	mu            sync.Mutex
	lockFile      *os.File // held only while this process is the owner
	owner         *Owner   // non-nil only while this process is the owner
	stopRequested chan struct{}
	stopOnce      sync.Once
}

// NewLeader attempts to become the owner by flocking cfg.LockPath(); if the
// lock is already held elsewhere, this process starts as a shim and relays
// lazily on the first call.
func NewLeader(ctx context.Context, cfg Config) (*Leader, error) {
	if err := os.MkdirAll(cfg.Home, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", cfg.Home, err)
	}
	l := &Leader{cfg: cfg, rootCtx: ctx, stopRequested: make(chan struct{})}
	if _, _, err := l.tryBecomeOwner(); err != nil {
		return nil, err
	}
	return l, nil
}

// IsOwner reports whether this process currently holds the browser.
func (l *Leader) IsOwner() bool { return l.Owner() != nil }

// Owner returns the Owner this process holds, or nil if it is a shim.
func (l *Leader) Owner() *Owner {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.owner
}

// StopRequested forwards an explicit stop request from whichever Owner this
// process currently holds, including one acquired later through promotion.
func (l *Leader) StopRequested() <-chan struct{} { return l.stopRequested }

// tryBecomeOwner attempts the exclusive flock; on success it constructs a
// real Owner (launching Chrome) and starts serving the socket for future
// shims. promoted=false with a nil error means another process already
// holds the lock — the expected, common case, not a failure.
func (l *Leader) tryBecomeOwner() (*Owner, bool, error) {
	l.mu.Lock()
	if l.owner != nil {
		o := l.owner
		l.mu.Unlock()
		return o, true, nil
	}
	l.mu.Unlock()

	f, acquired, err := acquireLock(l.cfg.LockPath())
	if err != nil {
		return nil, false, err
	}
	if !acquired {
		return nil, false, nil
	}

	o, err := NewOwner(l.rootCtx, l.cfg)
	if err != nil {
		_ = releaseLock(f)
		return nil, false, fmt.Errorf("become owner: %w", err)
	}
	if err := writePID(f); err != nil {
		_ = o.Close()
		_ = releaseLock(f)
		return nil, false, fmt.Errorf("write lock pid: %w", err)
	}
	if err := o.ServeSocketBackground(l.rootCtx); err != nil {
		_ = o.Close()
		_ = releaseLock(f)
		return nil, false, fmt.Errorf("serve socket: %w", err)
	}

	l.mu.Lock()
	l.lockFile = f
	l.owner = o
	l.mu.Unlock()
	l.watchOwner(o)
	return o, true, nil
}

func (l *Leader) watchOwner(o *Owner) {
	go func() {
		<-o.StopRequested()
		l.stopOnce.Do(func() { close(l.stopRequested) })
	}()
}

// Call implements caller: it dispatches directly if this process is the
// owner, or relays to the owner's socket if it is a shim. A shim whose
// relay fails (stale socket — the owner exited) attempts to become the new
// owner itself rather than erroring out (ADR 004's "main source of
// complexity").
func (l *Leader) Call(ctx context.Context, tool string, args json.RawMessage) (string, bool, error) {
	if o := l.Owner(); o != nil {
		return o.Call(ctx, tool, args)
	}

	text, isError, err := relayCall(ctx, l.cfg.SocketPath(), tool, args)
	if err == nil {
		return text, isError, nil
	}

	if ctx.Err() != nil {
		return "", false, ctx.Err()
	}
	var op *net.OpError
	if !errors.As(err, &op) || op.Op != "dial" {
		return "", false, fmt.Errorf("owner connection lost; action outcome uncertain, inspect before retrying: %w", err)
	}
	o, promoted, perr := l.tryBecomeOwner()
	if perr != nil {
		return "", false, fmt.Errorf("relay to owner failed (%v) and could not take over (%v)", err, perr)
	}
	if promoted {
		return o.Call(ctx, tool, args)
	}
	// Another process won the race to become owner between our failed relay
	// and our flock attempt; its socket should now be live.
	return relayCall(ctx, l.cfg.SocketPath(), tool, args)
}

// Close releases ownership, if held: tears down the browser pool and
// removes the lock and socket files.
func (l *Leader) Close() error {
	l.mu.Lock()
	o := l.owner
	f := l.lockFile
	l.owner = nil
	l.lockFile = nil
	l.mu.Unlock()
	if o == nil {
		return nil
	}
	err := o.Close()
	_ = os.Remove(l.cfg.SocketPath())
	_ = releaseLock(f)
	// Keep the lock inode: unlinking it lets a racing owner lock a different file.
	return err
}

func acquireLock(path string) (*os.File, bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("flock %s: %w", path, err)
	}
	return f, true, nil
}

func releaseLock(f *os.File) error {
	if f == nil {
		return nil
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return f.Close()
}

func writePID(f *os.File) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.WriteAt([]byte(fmt.Sprintf("%d\n", os.Getpid())), 0); err != nil {
		return err
	}
	return f.Sync()
}

// relayCall sends one request to the owner's socket and returns its
// response. Used both by Leader.Call (shim relay) and by the status/stop
// CLI commands (T11.7), which never attempt promotion.
func relayCall(ctx context.Context, sockPath, tool string, args json.RawMessage) (string, bool, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", sockPath)
	if err != nil {
		return "", false, fmt.Errorf("dial owner socket %s: %w", sockPath, err)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	request, err := json.Marshal(relayRequest{Tool: tool, Args: args, Client: clientIdentity(ctx)})
	if err != nil {
		return "", false, fmt.Errorf("encode relay request: %w", err)
	}
	for len(request) > 0 {
		n, writeErr := conn.Write(request)
		if writeErr != nil {
			return "", false, fmt.Errorf("send relay request: %w", writeErr)
		}
		if n == 0 {
			return "", false, fmt.Errorf("send relay request: %w", io.ErrShortWrite)
		}
		request = request[n:]
	}
	var resp relayResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return "", false, fmt.Errorf("read relay response: %w", err)
	}
	return resp.Text, resp.IsError, nil
}
