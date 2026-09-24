package extbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStrictHostedPairingRequiresExplicitOwnerRelease(t *testing.T) {
	bridge, err := New(WithToken(strings.Repeat("t", 64)), WithPollTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	bridge.EnableStrictPairing()
	if err := bridge.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := bridge.Stop(ctx); err != nil {
			t.Errorf("stop bridge: %v", err)
		}
	})
	client := &http.Client{Timeout: time.Second}
	request := func(method, path, browser, tab string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+bridge.Addr()+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+bridge.Token())
		if browser != "" {
			req.Header.Set("X-Ferro-Browser-Id", browser)
			tab = browser + "." + tab
		}
		if tab != "" {
			req.Header.Set(TabIDHeader, tab)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	closeResponse := func(resp *http.Response) { t.Helper(); _ = resp.Body.Close() }
	status := func(method, path, browser, tab string) int {
		t.Helper()
		resp := request(method, path, browser, tab)
		defer closeResponse(resp)
		return resp.StatusCode
	}
	const browserA = "browser-context-a-123456"
	const browserB = "browser-context-b-123456"

	if got := status(http.MethodGet, "/next", browserA, "42"); got != http.StatusConflict {
		t.Fatalf("unpaired /next status %d, want 409", got)
	}
	if got := status(http.MethodPost, "/pair", browserA, "42"); got != http.StatusNoContent {
		t.Fatalf("initial pair status %d, want 204", got)
	}
	if got := status(http.MethodGet, "/next", browserA, "42"); got != http.StatusNoContent {
		t.Fatalf("owner poll status %d, want 204", got)
	}
	if got := status(http.MethodGet, "/next", browserB, "42"); got != http.StatusConflict {
		t.Fatalf("different browser replaced pair after poll gap: status %d, want 409", got)
	}
	if got := status(http.MethodPost, "/pair", browserB, "42"); got != http.StatusConflict {
		t.Fatalf("different browser explicit pair displaced owner: status %d, want 409", got)
	}
	if got := status(http.MethodPost, "/disconnect", browserB, "42"); got != http.StatusConflict {
		t.Fatalf("different browser disconnect status %d, want 409", got)
	}
	if got := status(http.MethodPost, "/disconnect", browserA, "42"); got != http.StatusNoContent {
		t.Fatalf("owner disconnect status %d, want 204", got)
	}
	if got := status(http.MethodPost, "/pair", browserB, "42"); got != http.StatusNoContent {
		t.Fatalf("pair after owner release status %d, want 204", got)
	}
	if got := status(http.MethodGet, "/next", browserA, "42"); got != http.StatusConflict {
		t.Fatalf("stale browser reclaimed current pair: status %d, want 409", got)
	}
}

func TestStrictHostedDisconnectFencesStalePollAndSameIdentityRejoinIsIdempotent(t *testing.T) {
	bridge, err := New(WithToken(strings.Repeat("t", 64)), WithPollTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	bridge.EnableStrictPairing()
	if err := bridge.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := bridge.Stop(ctx); err != nil {
			t.Errorf("stop bridge: %v", err)
		}
	})
	client := &http.Client{Timeout: 2 * time.Second}
	call := func(method, path, browser, tab string) (*http.Response, error) {
		req, err := http.NewRequest(method, "http://"+bridge.Addr()+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+bridge.Token())
		req.Header.Set("X-Ferro-Browser-Id", browser)
		req.Header.Set(TabIDHeader, browser+"."+tab)
		return client.Do(req)
	}
	const browserA = "browser-context-a-123456"
	const browserB = "browser-context-b-123456"
	post := func(path, browser string) int {
		t.Helper()
		resp, err := call(http.MethodPost, path, browser, "42")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := post("/pair", browserA); got != http.StatusNoContent {
		t.Fatalf("pair A status %d", got)
	}

	// Leave a real /next request blocked in the server, then replace the
	// pairing explicitly. Its stale poll may wake to the queued command, but
	// must return 409 and leave the command deliverable to B.
	staleResult := make(chan int, 1)
	go func() {
		resp, err := call(http.MethodGet, "/next", browserA, "42")
		if err != nil {
			staleResult <- 0
			return
		}
		defer resp.Body.Close()
		staleResult <- resp.StatusCode
	}()
	deadline := time.Now().Add(time.Second)
	for {
		bridge.mu.Lock()
		active := bridge.activePolls[browserA+".42"]
		bridge.mu.Unlock()
		if active > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("stale poll did not become active")
		}
		runtime.Gosched()
	}
	if got := post("/disconnect", browserA); got != http.StatusNoContent {
		t.Fatalf("disconnect A status %d", got)
	}
	if got := post("/pair", browserB); got != http.StatusNoContent {
		t.Fatalf("pair B status %d", got)
	}

	gen := bridge.Generation()
	replyCh := make(chan error, 1)
	go func() {
		_, err := bridge.Enqueue(context.Background(), Command{Op: "snapshot"})
		replyCh <- err
	}()
	resp, err := call(http.MethodGet, "/next", browserB, "42")
	if err != nil {
		t.Fatal(err)
	}
	var next nextResponse
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("owner B next status %d, want 200", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&next); err != nil {
		resp.Body.Close()
		t.Fatal(err)
	}
	resp.Body.Close()
	if code := <-staleResult; code != http.StatusConflict && code != http.StatusNoContent {
		t.Fatalf("stale poll status %d, want 409 or timeout 204", code)
	}
	if got := post("/pair", browserB); got != http.StatusNoContent {
		t.Fatalf("same identity reconnect status %d, want 204", got)
	}
	if got := bridge.Generation(); got != gen {
		t.Fatalf("same identity reconnect changed generation from %d to %d", gen, got)
	}
	replyReq, err := http.NewRequest(http.MethodPost, "http://"+bridge.Addr()+"/reply", strings.NewReader(`{"id":"`+next.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	replyReq.Header.Set("Authorization", "Bearer "+bridge.Token())
	replyResp, err := client.Do(replyReq)
	if err != nil {
		t.Fatal(err)
	}
	replyResp.Body.Close()
	if replyResp.StatusCode != http.StatusNoContent {
		t.Fatalf("reply status %d, want 204", replyResp.StatusCode)
	}
	if err := <-replyCh; err != nil {
		t.Fatalf("enqueue reply: %v", err)
	}
}
