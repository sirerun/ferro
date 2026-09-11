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

	"github.com/dndungu/ferro/internal/core"
)

// nextResponse is GET /next's 200 body: {"id": "...", "action": <core.Action JSON>}.
type nextResponse struct {
	ID     string      `json:"id"`
	Action core.Action `json:"action"`
}

// replyRequest is POST /reply's expected body, per ADR 006 decision #2.
type replyRequest struct {
	ID       string         `json:"id"`
	Snapshot *core.Snapshot `json:"snapshot,omitempty"`
	Result   any            `json:"result,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// Start binds addr (e.g. "127.0.0.1:0" for an OS-assigned ephemeral port —
// see ADR 007: this backend is loopback-only for now) and begins serving
// GET /next and POST /reply in a background goroutine. It returns once the
// listener is bound, so a caller that immediately hands the bridge's Addr
// to another process won't race the accept loop starting up.
func (b *Bridge) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	b.ln = ln

	mux := http.NewServeMux()
	mux.HandleFunc("/next", b.handleNext)
	mux.HandleFunc("/reply", b.handleReply)
	b.srv = &http.Server{Handler: mux}

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
	return b.srv.Shutdown(ctx)
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
	if err := b.pair(tabID); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	select {
	case pa := <-b.queue:
		writeJSON(w, http.StatusOK, nextResponse{ID: pa.id, Action: pa.action})
	case <-time.After(b.pollTimeout):
		w.WriteHeader(http.StatusNoContent)
	case <-r.Context().Done():
		// Client disconnected (or the request's own context expired) while
		// long-polling: nothing to write back to a gone connection.
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
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if body.ID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}

	reply := Reply{Snapshot: body.Snapshot, Result: body.Result, Error: body.Error}
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
