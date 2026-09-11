// Package extbridge implements the Go-side half of ADR 006's poll/reply
// protocol: a small HTTP server that a single paired browser extension polls
// for pending core.Action values and posts results back to, so ferro's
// engine can drive a real, human-owned Chrome profile without CDP.
//
// # Protocol
//
//   - GET /next (bearer-token authed): returns the next queued action as
//     {"id": "...", "action": <core.Action JSON>}, or 204 No Content if
//     none is queued within the poll window (long-poll, not instant-return
//     — see "Deviations" below). Every request must also carry the
//     X-Ferro-Tab-Id header identifying the polling extension's paired tab.
//   - POST /reply (bearer-token authed): the paired extension posts
//     {"id": "...", "snapshot": <core.Snapshot JSON, if requested>,
//     "result": ..., "error": "..."}. The Bridge matches id back to the
//     Go-side caller blocked in Enqueue and delivers the Reply there.
//
// Both endpoints require "Authorization: Bearer <token>" using the token
// generated (or supplied) at construction; a missing or wrong token gets
// 401 Unauthorized.
//
// # Pairing
//
// Exactly one active pairing is allowed at a time, modeled as (token, tab
// id) per ADR 006 decision #2 — mirroring ferro-mcp's single-shared-tab
// model (ADR 004, internal/mcp/owner.go). The first successful, authorized
// GET /next request establishes the pairing by recording its X-Ferro-Tab-Id
// value; every subsequent GET /next must present that same tab id, and a
// request from a different, non-empty tab id while a pairing is active is
// rejected with 409 Conflict — the Bridge does not silently repair to a new
// tab out from under an in-flight pairing. POST /reply carries no tab id and
// is not itself pairing-checked: it is gated by the bearer token plus the
// unforgeable per-action id minted by Enqueue, which together are
// sufficient to prevent an unpaired caller from injecting a reply. Whether
// /reply should ALSO assert the caller's tab id, once the real extension
// (T12.2) and this bridge are wired together, is a T12.3 integration
// decision, not fixed here.
//
// # Deviations from ADR 006 (flagged for review, not blocking)
//
// ADR 006 decision #2 specifies the /next and /reply payload shapes and the
// (token, tab id) pairing model, but leaves two wire-level details
// unspecified; this package makes the following documented choices:
//
//  1. How the extension's tab id reaches the bridge to establish pairing:
//     ADR 006 lists only /next and /reply, with no third pairing endpoint.
//     This package conveys the tab id via an X-Ferro-Tab-Id header on every
//     /next request; the first request's value pins the pairing.
//  2. Long-poll semantics for /next: ADR 006 doesn't say whether /next
//     blocks or returns 204 immediately when no action is queued. This
//     package long-polls up to PollTimeout (default 30s, configurable via
//     WithPollTimeout) before returning 204, so a polling extension isn't
//     forced into a tight busy-loop.
//
// Both choices are documented here so a reviewer can confirm or override
// them before T12.3 (ExtensionDriver) and T12.2 (the extension itself)
// build against this contract.
package extbridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/dndungu/ferro/internal/core"
)

// TabIDHeader is the header the polling extension must set on every /next
// request naming the chrome.tabs tab id it is paired (or pairing) against.
const TabIDHeader = "X-Ferro-Tab-Id"

// defaultPollTimeout is how long GET /next blocks waiting for a queued
// action before returning 204 No Content. See the package doc's
// "Deviations" section.
const defaultPollTimeout = 30 * time.Second

// defaultQueueCapacity bounds how many actions may be enqueued ahead of the
// extension picking them up. ferro's engine issues one action at a time and
// waits for its reply before issuing the next (ADR 004's single-shared-tab
// serialization), so this only needs headroom for a small burst, not an
// unbounded backlog.
const defaultQueueCapacity = 8

// Reply is the Go-side decoding of a POST /reply body, delivered to the
// caller blocked in Enqueue for the matching action id.
type Reply struct {
	// Snapshot is the page snapshot the extension captured after acting,
	// when the action requested one. Nil when not requested/applicable.
	Snapshot *core.Snapshot
	// Result carries the action's result payload (e.g. extracted fields),
	// matching core.Action.Result's shape: arbitrary JSON, typed as any.
	Result any
	// Error is a non-empty, human-readable failure reason when the
	// extension could not complete the action (e.g. a blocked-state
	// heuristic tripped, or the target ref no longer resolves).
	Error string
}

// pendingAction is one action queued for delivery to the paired extension,
// alongside the id a later POST /reply must echo back.
type pendingAction struct {
	id     string
	action core.Action
}

// Option configures a Bridge at construction time.
type Option func(*Bridge)

// WithToken sets the bearer token both endpoints require, instead of
// generating a random one. Mainly useful for tests that need a known token.
func WithToken(token string) Option {
	return func(b *Bridge) { b.token = token }
}

// WithPollTimeout overrides the default duration GET /next blocks waiting
// for a queued action before returning 204 No Content. Default 30s.
func WithPollTimeout(d time.Duration) Option {
	return func(b *Bridge) { b.pollTimeout = d }
}

// Bridge is the poll/reply server described in the package doc. It owns the
// pending-action queue, the (token, tab id) pairing state, and — once
// Start is called — the listening HTTP server. The zero value is not
// usable; construct with New.
type Bridge struct {
	token       string
	pollTimeout time.Duration

	mu        sync.Mutex
	pairedTab string                // "" until the first /next request pairs
	waiting   map[string]chan Reply // action id -> the Enqueue call awaiting its reply
	queue     chan *pendingAction   // actions waiting to be handed to /next

	// srv and ln back Start/Stop; the HTTP wiring itself (handlers, routing,
	// auth) lives in server.go, kept separate from the queue/pairing logic
	// above.
	srv *http.Server
	ln  net.Listener
}

// New constructs a Bridge, generating a random bearer token unless
// WithToken overrides it. The Bridge is not yet listening — call Start to
// bind and serve.
func New(opts ...Option) (*Bridge, error) {
	b := &Bridge{
		pollTimeout: defaultPollTimeout,
		waiting:     make(map[string]chan Reply),
		queue:       make(chan *pendingAction, defaultQueueCapacity),
	}
	for _, opt := range opts {
		opt(b)
	}
	if b.token == "" {
		token, err := randomHex(32)
		if err != nil {
			return nil, fmt.Errorf("generate bearer token: %w", err)
		}
		b.token = token
	}
	return b, nil
}

// Token returns the bearer token both endpoints require. Give this to the
// extension (e.g. via its pairing popup) alongside the bridge's address.
func (b *Bridge) Token() string { return b.token }

// PairedTab reports the tab id of the currently active pairing, or "" if
// no extension has paired yet.
func (b *Bridge) PairedTab() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pairedTab
}

// pair records tabID as the active pairing if none is active yet, or
// confirms tabID matches the existing pairing. It returns an error — meant
// to surface as 409 Conflict — when a different tab is already paired.
func (b *Bridge) pair(tabID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pairedTab == "" {
		b.pairedTab = tabID
		return nil
	}
	if b.pairedTab != tabID {
		return fmt.Errorf("tab %q is already paired; only one active pairing is allowed at a time", b.pairedTab)
	}
	return nil
}

// Enqueue queues action for delivery to the paired extension's next GET
// /next poll and blocks until a matching POST /reply arrives or ctx is
// done. It is safe to call concurrently; actions are handed to /next in the
// order Enqueue was called (FIFO via the buffered queue channel).
func (b *Bridge) Enqueue(ctx context.Context, action core.Action) (Reply, error) {
	id, err := randomHex(16)
	if err != nil {
		return Reply{}, fmt.Errorf("generate action id: %w", err)
	}

	replyCh := make(chan Reply, 1)
	b.mu.Lock()
	b.waiting[id] = replyCh
	b.mu.Unlock()

	pa := &pendingAction{id: id, action: action}
	select {
	case b.queue <- pa:
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.waiting, id)
		b.mu.Unlock()
		return Reply{}, ctx.Err()
	}

	select {
	case reply := <-replyCh:
		return reply, nil
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.waiting, id)
		b.mu.Unlock()
		return Reply{}, ctx.Err()
	}
}

// deliver matches id to a waiting Enqueue call and hands it reply. It
// reports whether a waiter was found; the caller (handleReply) turns a
// false into 404 Not Found (unknown or already-replied id).
func (b *Bridge) deliver(id string, reply Reply) bool {
	b.mu.Lock()
	ch, ok := b.waiting[id]
	if ok {
		delete(b.waiting, id)
	}
	b.mu.Unlock()
	if !ok {
		return false
	}
	ch <- reply
	return true
}

// randomHex returns a random hex string encoding n random bytes.
func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
