package extbridge

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sirerun/ferro/internal/core"
)

// nextResponse is GET /next's 200 body: {"id": "...", "action": <Command JSON>}.
type nextResponse struct {
	ID     string  `json:"id"`
	Action Command `json:"action"`
}

// replyRequest is POST /reply's expected body, per ADR 006 decision #2.
type replyRequest struct {
	ID       string         `json:"id"`
	Snapshot *core.Snapshot `json:"snapshot,omitempty"`
	Result   any            `json:"result,omitempty"`
	Error    string         `json:"error,omitempty"`
	Code     string         `json:"code,omitempty"`
	Blocked  string         `json:"blocked,omitempty"`
}

// Start binds addr (e.g. "127.0.0.1:0" for an OS-assigned ephemeral port —
// see ADR 007: this backend is loopback-only for now) and begins serving
// GET /next and POST /reply in a background goroutine. It returns once the
// listener is bound, so a caller that immediately hands the bridge's Addr
// to another process won't race the accept loop starting up.
func (b *Bridge) Start(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("extension bridge must bind a loopback IP")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	b.ln = ln

	mux := http.NewServeMux()
	if b.chat != nil {
		mux.Handle("/chat/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			origin := r.Header.Get("Origin")
			if r.Host != ln.Addr().String() || (origin != "" && !strings.HasPrefix(origin, "chrome-extension://")) || !b.authorized(r) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			b.chat.ServeHTTP(w, r)
		}))
	}
	mux.HandleFunc("/next", b.handleNext)
	mux.HandleFunc("/reply", b.handleReply)
	mux.HandleFunc("/pair", b.handlePair)
	mux.HandleFunc("/disconnect", b.handleDisconnect)
	b.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}

	go func() {
		_ = b.srv.Serve(ln) // http.ErrServerClosed on a clean Stop; nothing else to do with it
	}()
	return nil
}

// Addr returns the address Start bound to (useful after binding to
// "127.0.0.1:0" to discover the OS-assigned port). Empty until Start
// succeeds.
func (b *Bridge) Addr() string {
	if b.ln == nil {
		return ""
	}
	return b.ln.Addr().String()
}

// Stop gracefully shuts down the HTTP server. It is a no-op if Start was
// never called.
func (b *Bridge) Stop(ctx context.Context) error {
	if b.srv == nil {
		return nil
	}
	err := b.srv.Shutdown(ctx)
	if err != nil {
		return b.srv.Close()
	}
	return nil
}

// handleNext serves GET /next: long-polls up to PollTimeout for a queued
// action, returning it as 200 {"id","action"}, or 204 No Content if none
// arrives in time. See the package doc's "Deviations" section for why this
// long-polls rather than returning 204 immediately, and how pairing is
// established via TabIDHeader.
func (b *Bridge) handleNext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !b.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	tabID := r.Header.Get(TabIDHeader)
	if tabID == "" {
		http.Error(w, TabIDHeader+" header is required", http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	if err := b.pairLocked(tabID); err != nil {
		b.mu.Unlock()
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	generation := b.generation
	b.activePolls[tabID]++
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.activePolls[tabID]--
		if b.activePolls[tabID] <= 0 {
			delete(b.activePolls, tabID)
		}
		b.mu.Unlock()
	}()
	timer := time.NewTimer(b.pollTimeout)
	defer timer.Stop()
	for {
		select {
		case pa := <-b.queue:
			b.mu.Lock()
			if generation != b.generation || b.pairedTab != tabID {
				// A long poll from the previous tab may still be unwinding after
				// disconnect. Preserve commands queued for the new generation.
				requeued := false
				if pa.generation == b.generation && b.pairedTab != "" {
					select {
					case b.queue <- pa:
						requeued = true
					default:
					}
				}
				b.mu.Unlock()
				if !requeued {
					b.deliver(pa.id, Reply{Code: "pairing_changed", Error: "pairing changed before dispatch"})
				}
				http.Error(w, "pairing changed", 409)
				return
			}
			if pa.generation != generation {
				b.mu.Unlock()
				b.deliver(pa.id, Reply{Code: "pairing_changed", Error: "pairing changed before dispatch"})
				continue
			}
			_, waiting := b.waiting[pa.id]
			live := waiting && pa.ctx.Err() == nil
			if live {
				pa.delivered = true
				b.dispatched[pa.id] = true
			}
			b.mu.Unlock()
			if !live {
				continue
			}
			writeJSON(w, http.StatusOK, nextResponse{ID: pa.id, Action: pa.action})
			return
		case <-timer.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			return
		}
	}
}

// handleReply serves POST /reply: decodes the extension's result for one
// previously-issued action id and delivers it to the Enqueue call blocked
// waiting for it. Responds 204 on success, 404 if id is unknown or was
// already replied to (see the package doc), 400 on a malformed body.
func (b *Bridge) handleReply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !b.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var body replyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.ID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	reply := Reply{Snapshot: body.Snapshot, Result: body.Result, Error: body.Error, Code: body.Code, Blocked: body.Blocked}
	if !b.deliver(body.ID, reply) {
		http.Error(w, fmt.Sprintf("unknown or already-replied id %q", body.ID), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// authorized reports whether r carries the correct
// "Authorization: Bearer <token>" header, compared in constant time.
func (b *Bridge) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return false
	}
	token := strings.TrimPrefix(h, prefix)
	return subtle.ConstantTimeCompare([]byte(token), []byte(b.token)) == 1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (b *Bridge) handlePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if !b.authorized(r) {
		http.Error(w, "unauthorized", 401)
		return
	}
	id := r.Header.Get(TabIDHeader)
	if id == "" {
		http.Error(w, "missing tab id", 400)
		return
	}
	if err := b.pairExplicit(id); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	w.WriteHeader(204)
}
func (b *Bridge) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if !b.authorized(r) {
		http.Error(w, "unauthorized", 401)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if id := r.Header.Get(TabIDHeader); id == "" || id != b.pairedTab {
		http.Error(w, "wrong pairing", 409)
		return
	}
	if b.hasDispatchedLocked() {
		http.Error(w, "an action reply is still pending", http.StatusConflict)
		return
	}
	b.pairedTab = ""
	b.generation++
	for id, ch := range b.waiting {
		ch <- Reply{Code: "disconnected", Error: "extension disconnected; inspect the page before retrying"}
		delete(b.waiting, id)
		delete(b.dispatched, id)
	}
	w.WriteHeader(204)
}
