// Package extbridge implements the authenticated local transport between
// Ferro's PageDriver and one explicitly paired Chrome extension tab.
// GET /next long-polls for a resolved Command; POST /reply completes it.
// POST /pair verifies pairing before the popup reports success, and
// POST /disconnect revokes it. Poll requests carry X-Ferro-Tab-Id.
// Replies are authenticated by bearer token and an unpredictable action ID.
// Canceled commands are discarded before dispatch; canceled dispatched
// commands report an uncertain outcome and must not be retried blindly.
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
const defaultPollTimeout = 20 * time.Second

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
	Error   string
	Code    string
	Blocked string
}

// pendingAction is one action queued for delivery to the paired extension,
// alongside the id a later POST /reply must echo back.
type pendingAction struct {
	id         string
	action     Command
	ctx        context.Context
	delivered  bool
	generation uint64
}

// Option configures a Bridge at construction time.
type Option func(*Bridge)

// WithChatHandler mounts the local, bearer-authenticated side-panel API.
// Configure before Start; the handler never receives unauthenticated requests.
func WithChatHandler(h http.Handler) Option { return func(b *Bridge) { b.chat = h } }

// WithToken sets the bearer token both endpoints require, instead of
// generating a random one. Mainly useful for tests that need a known token.
func WithToken(token string) Option {
	return func(b *Bridge) { b.token = token }
}

// WithPollTimeout overrides the default duration GET /next blocks waiting
// for a queued action before returning 204 No Content. Default 20s.
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

	mu            sync.Mutex
	lastSeen      time.Time
	generation    uint64
	pairedTab     string                // "" until the first /next request pairs
	strictPairing bool                  // hosted mode requires explicit pairing and disconnect
	activePolls   map[string]int        // live /next requests by tab
	waiting       map[string]chan Reply // action id -> the Enqueue call awaiting its reply
	dispatched    map[string]bool       // actions handed to the extension but not yet replied
	queue         chan *pendingAction   // actions waiting to be handed to /next

	// srv and ln back Start/Stop; the HTTP wiring itself (handlers, routing,
	// auth) lives in server.go, kept separate from the queue/pairing logic
	// above.
	chat http.Handler
	srv  *http.Server
	ln   net.Listener
}

// New constructs a Bridge, generating a random bearer token unless
// WithToken overrides it. The Bridge is not yet listening — call Start to
// bind and serve.
func New(opts ...Option) (*Bridge, error) {
	b := &Bridge{
		pollTimeout: defaultPollTimeout,
		waiting:     make(map[string]chan Reply),
		dispatched:  make(map[string]bool),
		activePolls: make(map[string]int),
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

// Generation returns the current pairing generation. Snapshot refs are valid
// only for the generation in which they were captured.
func (b *Bridge) Generation() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.generation
}

// EnableStrictPairing requires an explicit /pair before polling and prevents
// an idle or expired poll from transferring a hosted pairing to another tab.
// It is safe to call repeatedly; hosted owners enable it before publishing
// their handler.
func (b *Bridge) EnableStrictPairing() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.strictPairing = true
}

// pair records the tabID reported by a poll, pairing it if none is active and
// replacing an inactive tab. It returns an error when another tab still has
// a live poll.
func (b *Bridge) pair(tabID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pairLocked(tabID)
}

// pairExplicit starts a fresh extension control session, including when
// Chrome reuses the previous numeric tab id after restarting.
func (b *Bridge) pairExplicit(tabID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.strictPairing && b.pairedTab == "" {
		b.pairedTab = tabID
		b.lastSeen = time.Now()
		return nil
	}
	if b.pairedTab == tabID {
		if b.strictPairing {
			// Hosted reconnect is idempotent. In particular, don't invalidate
			// active polls, snapshots, queued work, or an in-flight action.
			b.lastSeen = time.Now()
			return nil
		}
		if b.hasDispatchedLocked() {
			return fmt.Errorf("tab %q has an action in progress; wait for it to finish before reconnecting", tabID)
		}
		b.resetPairingLocked()
	}
	return b.pairLocked(tabID)
}

func (b *Bridge) pairLocked(tabID string) error {
	if b.strictPairing {
		if b.pairedTab == "" {
			return fmt.Errorf("tab must explicitly pair before polling")
		}
		if b.pairedTab != tabID {
			return fmt.Errorf("tab %q is already paired; disconnect it before pairing another tab", b.pairedTab)
		}
		b.lastSeen = time.Now()
		return nil
	}
	// Chrome clears storage.session when it restarts or reloads an extension.
	// If the old extension no longer has a live poll, replace it immediately.
	// Otherwise preserve the active pairing until it disconnects.
	if b.pairedTab != "" && b.pairedTab != tabID {
		if b.activePolls[b.pairedTab] > 0 || b.hasDispatchedLocked() {
			return fmt.Errorf("tab %q is already paired; disconnect it or wait for its current action or poll to stop", b.pairedTab)
		}
		b.resetPairingLocked()
	} else if b.pairedTab != "" && b.activePolls[b.pairedTab] == 0 && !b.hasDispatchedLocked() && time.Since(b.lastSeen) >= 45*time.Second {
		// A restarted Chrome may reuse a numeric tab id; force refs to be
		// reacquired even when the id happens to match.
		b.resetPairingLocked()
	}
	if b.pairedTab == "" {
		b.pairedTab = tabID
		b.lastSeen = time.Now()
		return nil
	}
	b.lastSeen = time.Now()
	return nil
}

func (b *Bridge) hasDispatchedLocked() bool { return len(b.dispatched) > 0 }

func (b *Bridge) resetPairingLocked() {
	b.pairedTab = ""
	b.generation++
	for id, ch := range b.waiting {
		ch <- Reply{Code: "disconnected", Error: "extension disconnected; inspect the page before retrying"}
		delete(b.waiting, id)
		delete(b.dispatched, id)
	}
	for {
		select {
		case <-b.queue:
		default:
			return
		}
	}
}

// Enqueue queues action for delivery to the paired extension's next GET
// /next poll and blocks until a matching POST /reply arrives or ctx is
// done. It is safe to call concurrently; actions are handed to /next in the
// order Enqueue was called (FIFO via the buffered queue channel).
func (b *Bridge) Enqueue(ctx context.Context, action Command) (Reply, error) {
	id, err := randomHex(16)
	if err != nil {
		return Reply{}, fmt.Errorf("generate action id: %w", err)
	}

	replyCh := make(chan Reply, 1)
	b.mu.Lock()
	if pin, ok := ctx.Value(pairingKey{}).(pairingPin); ok && (pin.bridge != b || pin.generation != b.generation) {
		b.mu.Unlock()
		return Reply{}, &core.StopError{Code: "pairing_changed", Message: "tab pairing changed; acquire a fresh snapshot before continuing"}
	}
	generation := b.generation
	b.waiting[id] = replyCh
	b.mu.Unlock()

	pa := &pendingAction{id: id, action: action, ctx: ctx, generation: generation}
	select {
	case b.queue <- pa:
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.waiting, id)
		delete(b.dispatched, id)
		b.mu.Unlock()
		return Reply{}, ctx.Err()
	}

	select {
	case reply := <-replyCh:
		return reply, nil
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.waiting, id)
		delete(b.dispatched, id)
		delivered := pa.delivered
		b.mu.Unlock()
		if delivered {
			return Reply{}, &core.StopError{Code: "outcome_uncertain", Message: "extension action was dispatched before cancellation; inspect the page before retrying"}
		}
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
		delete(b.dispatched, id)
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

// Connected reports recent contact, including an outstanding long poll.
func (b *Bridge) Connected() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pairedTab != "" && time.Since(b.lastSeen) < 45*time.Second
}

type pairingKey struct{}
type pairingPin struct {
	bridge     *Bridge
	generation uint64
}

// Pin binds a sequence of commands to the current pairing. Re-pairing cannot
// transfer an old task's refs or actions into the newly selected tab.
func (b *Bridge) Pin(ctx context.Context) context.Context {
	if _, ok := ctx.Value(pairingKey{}).(pairingPin); ok {
		return ctx
	}
	b.mu.Lock()
	generation := b.generation
	b.mu.Unlock()
	return context.WithValue(ctx, pairingKey{}, pairingPin{b, generation})
}

// PinGeneration binds a call to a previously observed pairing generation.
// A mismatch is rejected by Enqueue before an action reaches Chrome.
func (b *Bridge) PinGeneration(ctx context.Context, generation uint64) context.Context {
	if _, ok := ctx.Value(pairingKey{}).(pairingPin); ok {
		return ctx
	}
	return context.WithValue(ctx, pairingKey{}, pairingPin{b, generation})
}
