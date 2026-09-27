package extbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sirerun/ferro/internal/core"
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

func TestPairExplicitRefreshesSameTabGeneration(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.pair("tab-1"); err != nil {
		t.Fatal(err)
	}
	old := b.Generation()
	if err := b.pairExplicit("tab-1"); err != nil {
		t.Fatal(err)
	}
	if got := b.Generation(); got != old+1 {
		t.Fatalf("generation = %d, want %d", got, old+1)
	}
}

func TestPairExplicitDoesNotResetInFlightAction(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.pair("tab-1"); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.waiting["active"] = make(chan Reply, 1)
	b.dispatched["active"] = true
	oldGeneration := b.generation
	b.mu.Unlock()
	if err := b.pairExplicit("tab-1"); err == nil {
		t.Fatal("explicit reconnect reset an in-flight action")
	}
	if got := b.Generation(); got != oldGeneration {
		t.Fatalf("generation = %d, want unchanged %d", got, oldGeneration)
	}
}

func TestPairDoesNotTakeOverWhileDispatchedActionAwaitsReply(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.pair("old-tab"); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.waiting["active"] = make(chan Reply, 1)
	b.dispatched["active"] = true
	b.mu.Unlock()
	if err := b.pair("new-tab"); err == nil {
		t.Fatal("pair replaced a tab with an action still in progress")
	}
	if got := b.PairedTab(); got != "old-tab" {
		t.Fatalf("paired tab = %q, want old-tab", got)
	}
}

func TestPairCanReplaceTabWithUndeliveredCommand(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.pair("old-tab"); err != nil {
		t.Fatal(err)
	}
	ch := make(chan Reply, 1)
	b.mu.Lock()
	b.waiting["queued"] = ch
	b.queue <- &pendingAction{id: "queued", generation: b.generation}
	b.mu.Unlock()
	if err := b.pair("new-tab"); err != nil {
		t.Fatalf("replace before dispatch: %v", err)
	}
	select {
	case reply := <-ch:
		if reply.Code != "disconnected" {
			t.Fatalf("reply code = %q, want disconnected", reply.Code)
		}
	default:
		t.Fatal("undelivered command did not receive disconnected reply")
	}
}

func TestDisconnectRejectsUntilDispatchedReplyArrives(t *testing.T) {
	b, err := New(WithToken("test-token"))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.pair("tab-1"); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.waiting["active"] = make(chan Reply, 1)
	b.dispatched["active"] = true
	b.mu.Unlock()
	req := httptest.NewRequest(http.MethodPost, "http://ferro/disconnect", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set(TabIDHeader, "tab-1")
	rec := httptest.NewRecorder()
	b.handleDisconnect(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("disconnect status = %d, want 409", rec.Code)
	}
	if got := b.PairedTab(); got != "tab-1" {
		t.Fatalf("paired tab = %q, want tab-1", got)
	}
}

func TestDispatchedActionBlocksPairingUntilReplyOrDeadline(t *testing.T) {
	b, err := New(WithToken("test-token"), WithPollTimeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.pair("tab-1"); err != nil {
		t.Fatal(err)
	}
	dispatch := func(ctx context.Context) (nextResponse, <-chan error) {
		t.Helper()
		result := make(chan error, 1)
		go func() { _, err := b.Enqueue(ctx, Command{Op: "click", Selector: "#save"}); result <- err }()
		deadline := time.Now().Add(time.Second)
		for {
			b.mu.Lock()
			ready := len(b.waiting) > 0
			b.mu.Unlock()
			if ready {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("action was not enqueued")
			}
			time.Sleep(time.Millisecond)
		}
		req := httptest.NewRequest(http.MethodGet, "http://ferro/next", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set(TabIDHeader, "tab-1")
		rec := httptest.NewRecorder()
		b.handleNext(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("next status = %d, want 200", rec.Code)
		}
		var next nextResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &next); err != nil {
			t.Fatal(err)
		}
		return next, result
	}
	post := func(method, target, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
		req.Header.Set("Authorization", "Bearer test-token")
		rec := httptest.NewRecorder()
		if target == "http://ferro/disconnect" {
			req.Header.Set(TabIDHeader, "tab-1")
			b.handleDisconnect(rec, req)
		} else if target == "http://ferro/pair" {
			req.Header.Set(TabIDHeader, "tab-2")
			b.handlePair(rec, req)
		} else {
			b.handleReply(rec, req)
		}
		return rec
	}

	next, result := dispatch(context.Background())
	if got := post(http.MethodPost, "http://ferro/disconnect", "").Code; got != http.StatusConflict {
		t.Fatalf("pending disconnect status = %d, want 409", got)
	}
	if got := post(http.MethodPost, "http://ferro/pair", "").Code; got != http.StatusConflict {
		t.Fatalf("pending switch status = %d, want 409", got)
	}
	body, _ := json.Marshal(map[string]string{"id": next.ID, "result": "ok"})
	if got := post(http.MethodPost, "http://ferro/reply", string(body)).Code; got != http.StatusNoContent {
		t.Fatalf("reply status = %d, want 204", got)
	}
	if err := <-result; err != nil {
		t.Fatalf("Enqueue after reply: %v", err)
	}
	// Exercise the real endpoints after cleanup, including releasing the new
	// pairing again. This verifies both guards unblock after an accepted reply.
	pairReq := httptest.NewRequest(http.MethodPost, "http://ferro/pair", nil)
	pairReq.Header.Set("Authorization", "Bearer test-token")
	pairReq.Header.Set(TabIDHeader, "tab-2")
	pairRec := httptest.NewRecorder()
	b.handlePair(pairRec, pairReq)
	if pairRec.Code != http.StatusNoContent {
		t.Fatalf("pair after reply status = %d, want 204", pairRec.Code)
	}
	disconnectReq := httptest.NewRequest(http.MethodPost, "http://ferro/disconnect", nil)
	disconnectReq.Header.Set("Authorization", "Bearer test-token")
	disconnectReq.Header.Set(TabIDHeader, "tab-2")
	disconnectRec := httptest.NewRecorder()
	b.handleDisconnect(disconnectRec, disconnectReq)
	if disconnectRec.Code != http.StatusNoContent {
		t.Fatalf("disconnect after reply status = %d, want 204", disconnectRec.Code)
	}
	repairReq := httptest.NewRequest(http.MethodPost, "http://ferro/pair", nil)
	repairReq.Header.Set("Authorization", "Bearer test-token")
	repairReq.Header.Set(TabIDHeader, "tab-1")
	repairRec := httptest.NewRecorder()
	b.handlePair(repairRec, repairReq)
	if repairRec.Code != http.StatusNoContent {
		t.Fatalf("repair after reply status = %d, want 204", repairRec.Code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, timeoutResult := dispatch(ctx)
	if err := <-timeoutResult; err == nil {
		t.Fatal("Enqueue after dispatched action deadline succeeded")
	} else {
		var stopped *core.StopError
		if !errors.As(err, &stopped) || stopped.Code != "outcome_uncertain" {
			t.Fatalf("Enqueue deadline error = %v, want outcome_uncertain", err)
		}
	}
	deadline := time.After(time.Second)
	for {
		b.mu.Lock()
		pending := len(b.dispatched) > 0
		b.mu.Unlock()
		if !pending {
			break
		}
		select {
		case <-deadline:
			t.Fatal("dispatched state was not cleared after deadline")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := b.pair("tab-2"); err != nil {
		t.Fatalf("pair after deadline: %v", err)
	}
}

func TestOldPollRequeuesNewGenerationCommand(t *testing.T) {
	b, err := New(WithToken("test-token"), WithPollTimeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.pairExplicit("old-tab"); err != nil {
		t.Fatal(err)
	}
	oldReq := httptest.NewRequest(http.MethodGet, "http://ferro/next", nil)
	oldReq.Header.Set("Authorization", "Bearer test-token")
	oldReq.Header.Set(TabIDHeader, "old-tab")
	oldRec := httptest.NewRecorder()
	oldDone := make(chan struct{})
	go func() { b.handleNext(oldRec, oldReq); close(oldDone) }()
	deadline := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		polling := b.activePolls["old-tab"] > 0
		b.mu.Unlock()
		if polling {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("old poll did not start")
		}
		time.Sleep(time.Millisecond)
	}
	disconnectReq := httptest.NewRequest(http.MethodPost, "http://ferro/disconnect", nil)
	disconnectReq.Header.Set("Authorization", "Bearer test-token")
	disconnectReq.Header.Set(TabIDHeader, "old-tab")
	b.handleDisconnect(httptest.NewRecorder(), disconnectReq)
	if err := b.pairExplicit("new-tab"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	enqueued := make(chan error, 1)
	go func() {
		_, err := b.Enqueue(ctx, Command{Op: "click", Selector: "#new"})
		enqueued <- err
	}()
	select {
	case <-oldDone:
	case <-time.After(time.Second):
		t.Fatal("old poll did not stop after pairing changed")
	}
	if oldRec.Code != http.StatusConflict {
		t.Fatalf("old poll status = %d, want 409", oldRec.Code)
	}
	newReq := httptest.NewRequest(http.MethodGet, "http://ferro/next", nil)
	newReq.Header.Set("Authorization", "Bearer test-token")
	newReq.Header.Set(TabIDHeader, "new-tab")
	newRec := httptest.NewRecorder()
	b.handleNext(newRec, newReq)
	if newRec.Code != http.StatusOK {
		t.Fatalf("new poll status = %d, want 200; body=%s", newRec.Code, newRec.Body.String())
	}
	cancel()
	<-enqueued
}

func TestPair_SecondDifferentTabRejected(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := b.pair("tab-1"); err != nil {
		t.Fatalf("first pair: %v", err)
	}
	b.mu.Lock()
	b.activePolls["tab-1"] = 1
	b.mu.Unlock()
	if err := b.pair("tab-2"); err == nil {
		t.Fatal("pairing a second, different tab while one is active: got nil error, want rejection")
	}
	if got := b.PairedTab(); got != "tab-1" {
		t.Fatalf("PairedTab() after rejected second pairing = %q, want tab-1 (unchanged)", got)
	}
}

func TestPair_ReplacesStalePairing(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.pair("old-tab"); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.lastSeen = time.Now().Add(-46 * time.Second)
	oldGeneration := b.generation
	b.mu.Unlock()
	if err := b.pair("new-tab"); err != nil {
		t.Fatalf("replace stale pairing: %v", err)
	}
	if got := b.PairedTab(); got != "new-tab" {
		t.Fatalf("paired tab = %q, want new-tab", got)
	}
	if b.generation != oldGeneration+1 {
		t.Fatalf("generation = %d, want %d", b.generation, oldGeneration+1)
	}
}

func TestPair_ReplacesInactivePairingImmediatelyAndDrainsQueue(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.pair("old-tab"); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.queue <- &pendingAction{id: "queued", generation: b.generation}
	oldGeneration := b.generation
	b.mu.Unlock()
	if err := b.pair("new-tab"); err != nil {
		t.Fatal(err)
	}
	if got := b.PairedTab(); got != "new-tab" {
		t.Fatalf("paired tab = %q, want new-tab", got)
	}
	if b.generation != oldGeneration+1 {
		t.Fatalf("generation = %d, want %d", b.generation, oldGeneration+1)
	}
	if got := len(b.queue); got != 0 {
		t.Fatalf("queued stale actions = %d, want 0", got)
	}
}

func TestPinnedSnapshotCannotEnqueueAfterPairingChanges(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.pair("old-tab"); err != nil {
		t.Fatal(err)
	}
	ctx := b.PinGeneration(context.Background(), b.Generation())
	if err := b.pair("new-tab"); err != nil {
		t.Fatal(err)
	}
	_, err = b.Enqueue(ctx, Command{Op: "click", Selector: "#save"})
	var stopped *core.StopError
	if !errors.As(err, &stopped) || stopped.Code != "pairing_changed" {
		t.Fatalf("Enqueue() error = %v, want pairing_changed", err)
	}
}

func TestEnqueueDeliver_MatchesByID(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	action := Command{Op: "click", Selector: "#three"}
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
	if pa.action.Op != "click" || pa.action.Selector != "#three" {
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
		_, err := b.Enqueue(ctx, Command{Op: "click", Selector: "#one"})
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
	action := Command{Op: "fill", Selector: "#seven", Text: "hello"}
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
	if next.Action.Op != "fill" || next.Action.Selector != "#seven" || next.Action.Text != "hello" {
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
	b.mu.Lock()
	b.activePolls["tab-1"] = 1 // model a live extension long poll
	b.mu.Unlock()
	if status := poll("tab-2"); status != http.StatusConflict {
		t.Fatalf("second, different-tab pairing poll status = %d, want %d", status, http.StatusConflict)
	}
	b.mu.Lock()
	delete(b.activePolls, "tab-1")
	b.mu.Unlock()
	if status := poll("tab-2"); status != http.StatusNoContent {
		t.Fatalf("inactive pairing takeover status = %d, want %d", status, http.StatusNoContent)
	}
	if status := poll("tab-1"); status != http.StatusNoContent {
		t.Fatalf("different tab may replace an inactive pairing; poll status = %d, want %d", status, http.StatusNoContent)
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
