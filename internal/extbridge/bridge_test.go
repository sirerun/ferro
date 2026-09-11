package extbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/dndungu/ferro/internal/core"
)

// --- Tier 1: queue/pairing logic in isolation (no HTTP) ---------------------

func TestPair_FirstCallEstablishesPairing(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := b.PairedTab(); got != "" {
		t.Fatalf("PairedTab before any pairing = %q, want empty", got)
	}
	if err := b.pair("tab-1"); err != nil {
		t.Fatalf("pair(tab-1): %v", err)
	}
	if got := b.PairedTab(); got != "tab-1" {
		t.Fatalf("PairedTab() = %q, want tab-1", got)
	}
}

func TestPair_SameTabRepeatsOK(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := b.pair("tab-1"); err != nil {
		t.Fatalf("first pair: %v", err)
	}
	if err := b.pair("tab-1"); err != nil {
		t.Fatalf("re-pairing the same tab should succeed, got: %v", err)
	}
}

func TestPair_SecondDifferentTabRejected(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := b.pair("tab-1"); err != nil {
		t.Fatalf("first pair: %v", err)
	}
	if err := b.pair("tab-2"); err == nil {
		t.Fatal("pairing a second, different tab while one is active: got nil error, want rejection")
	}
	if got := b.PairedTab(); got != "tab-1" {
		t.Fatalf("PairedTab() after rejected second pairing = %q, want tab-1 (unchanged)", got)
	}
}

func TestEnqueueDeliver_MatchesByID(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	action := core.Action{Kind: core.KindClick, Ref: 3}
	done := make(chan Reply, 1)
	errs := make(chan error, 1)
	go func() {
		reply, err := b.Enqueue(context.Background(), action)
		if err != nil {
			errs <- err
			return
		}
		done <- reply
	}()

	var pa *pendingAction
	select {
	case pa = <-b.queue:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for enqueued action to appear on the queue")
	}
	if pa.action.Kind != core.KindClick || pa.action.Ref != 3 {
		t.Fatalf("queued action = %+v, want the enqueued action unchanged", pa.action)
	}

	if !b.deliver(pa.id, Reply{Result: "clicked"}) {
		t.Fatal("deliver: expected a waiter for the queued id")
	}

	select {
	case reply := <-done:
		if reply.Result != "clicked" {
			t.Fatalf("reply.Result = %v, want %q", reply.Result, "clicked")
		}
	case err := <-errs:
		t.Fatalf("Enqueue returned error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Enqueue to return after deliver")
	}
}

func TestDeliver_UnknownIDReturnsFalse(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if b.deliver("no-such-id", Reply{}) {
		t.Fatal("deliver on an id nobody is waiting for: got true, want false")
	}
}

func TestEnqueue_ContextCanceledBeforeReply(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())

	errs := make(chan error, 1)
	go func() {
		_, err := b.Enqueue(ctx, core.Action{Kind: core.KindClick, Ref: 1})
		errs <- err
	}()

	// Wait for the action to actually be queued so we know Enqueue is past
	// the send-to-queue select and blocked waiting on the reply channel.
	select {
	case <-b.queue:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for action to be queued")
	}

	cancel()
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Enqueue error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Enqueue to return after cancel")
	}

	b.mu.Lock()
	_, stillWaiting := b.waiting[""] // best-effort: map should be empty of stale entries
	b.mu.Unlock()
	if stillWaiting {
		t.Fatal("waiting map retained an entry after context cancellation")
	}
}

// --- Tier 2: real HTTP boundary (httptest / a real listening http.Server) --

func TestHTTP_LongPollNextThenReply(t *testing.T) {
	b, err := New(WithToken("test-token"), WithPollTimeout(5*time.Second))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := b.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = b.Stop(ctx)
	})

	baseURL := "http://" + b.Addr()

	// The Go-side caller (a stand-in for T12.3's ExtensionDriver) enqueues
	// an action and blocks for its reply, exactly like a real caller would.
	action := core.Action{Kind: core.KindFill, Ref: 7, Text: "hello"}
	replyCh := make(chan Reply, 1)
	errCh := make(chan error, 1)
	go func() {
		reply, err := b.Enqueue(context.Background(), action)
		if err != nil {
			errCh <- err
			return
		}
		replyCh <- reply
	}()

	// A test HTTP client long-polls GET /next across the real network
	// boundary and must receive the queued action.
	req, err := http.NewRequest(http.MethodGet, baseURL+"/next", nil)
	if err != nil {
		t.Fatalf("build /next request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set(TabIDHeader, "tab-42")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /next: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /next status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var next nextResponse
	if err := json.NewDecoder(resp.Body).Decode(&next); err != nil {
		t.Fatalf("decode /next body: %v", err)
	}
	if next.ID == "" {
		t.Fatal("/next returned an empty id")
	}
	if next.Action.Kind != core.KindFill || next.Action.Ref != 7 || next.Action.Text != "hello" {
		t.Fatalf("/next action = %+v, want the enqueued action unchanged", next.Action)
	}
	if got := b.PairedTab(); got != "tab-42" {
		t.Fatalf("PairedTab() after /next = %q, want tab-42", got)
	}

	// The paired extension posts its result back via POST /reply.
	body, err := json.Marshal(replyRequest{
		ID:     next.ID,
		Result: map[string]any{"filled": true},
	})
	if err != nil {
		t.Fatalf("marshal reply body: %v", err)
	}
	req, err = http.NewRequest(http.MethodPost, baseURL+"/reply", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build /reply request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /reply: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("POST /reply status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	// The Go-side caller from Enqueue must receive the reply with the
	// matching id's payload.
	select {
	case reply := <-replyCh:
		m, ok := reply.Result.(map[string]any)
		if !ok || m["filled"] != true {
			t.Fatalf("Enqueue reply.Result = %#v, want map with filled=true", reply.Result)
		}
	case err := <-errCh:
		t.Fatalf("Enqueue returned error: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Enqueue to return after /reply")
	}
}

func TestHTTP_NextReturns204WhenNoneQueued(t *testing.T) {
	b, err := New(WithToken("test-token"), WithPollTimeout(200*time.Millisecond))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := b.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = b.Stop(ctx)
	})

	req, err := http.NewRequest(http.MethodGet, "http://"+b.Addr()+"/next", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set(TabIDHeader, "tab-1")

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /next: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond {
		t.Fatalf("returned after %v, want it to have long-polled for roughly PollTimeout", elapsed)
	}
}

func TestHTTP_MissingBearerTokenRejected(t *testing.T) {
	b, err := New(WithToken("test-token"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := b.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = b.Stop(ctx)
	})

	tests := []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"wrong token", "Bearer nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "http://"+b.Addr()+"/next", nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			req.Header.Set(TabIDHeader, "tab-1")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET /next: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
			}
		})
	}
}

func TestHTTP_SecondPairingAttemptRejected(t *testing.T) {
	b, err := New(WithToken("test-token"), WithPollTimeout(200*time.Millisecond))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := b.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = b.Stop(ctx)
	})

	poll := func(tabID string) int {
		req, err := http.NewRequest(http.MethodGet, "http://"+b.Addr()+"/next", nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set(TabIDHeader, tabID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /next: %v", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if status := poll("tab-1"); status != http.StatusNoContent {
		t.Fatalf("first pairing poll status = %d, want %d", status, http.StatusNoContent)
	}
	if status := poll("tab-2"); status != http.StatusConflict {
		t.Fatalf("second, different-tab pairing poll status = %d, want %d", status, http.StatusConflict)
	}
	if status := poll("tab-1"); status != http.StatusNoContent {
		t.Fatalf("re-polling the already-paired tab status = %d, want %d", status, http.StatusNoContent)
	}
}

func TestHTTP_ReplyUnknownIDReturns404(t *testing.T) {
	b, err := New(WithToken("test-token"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := b.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = b.Stop(ctx)
	})

	body, err := json.Marshal(replyRequest{ID: "does-not-exist", Result: "x"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+b.Addr()+"/reply", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /reply: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestHTTP_ReplyMalformedBodyReturns400(t *testing.T) {
	b, err := New(WithToken("test-token"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := b.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = b.Stop(ctx)
	})

	req, err := http.NewRequest(http.MethodPost, "http://"+b.Addr()+"/reply", bytes.NewReader([]byte("not json")))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer test-token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /reply: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}
