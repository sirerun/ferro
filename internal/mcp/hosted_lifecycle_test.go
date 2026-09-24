package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dndungu/ferro/internal/extbridge"
)

type lifecycleClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *lifecycleClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *lifecycleClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type lifecycleController struct {
	mu             sync.Mutex
	protectCalls   []bool
	sleepCalls     int
	protectErrorAt int
	protectErr     error
	sleepErr       error
	initialStarted chan struct{}
	initialRelease chan struct{}
}

func (c *lifecycleController) Protect(ctx context.Context, protect bool) error {
	c.mu.Lock()
	c.protectCalls = append(c.protectCalls, protect)
	call := len(c.protectCalls)
	start, release := c.initialStarted, c.initialRelease
	errAt, err := c.protectErrorAt, c.protectErr
	c.mu.Unlock()
	if call == 1 && start != nil {
		close(start)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if errAt == call {
		return err
	}
	return nil
}

func (c *lifecycleController) Sleep(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleepCalls++
	return c.sleepErr
}

func (c *lifecycleController) calls() ([]bool, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]bool(nil), c.protectCalls...), c.sleepCalls
}

func newLifecycleFixture(t *testing.T, next http.Handler, controller *lifecycleController) (*Owner, *HostedLifecycle, *lifecycleClock) {
	t.Helper()
	owner, _, _, _ := hostedFixture(t)
	clock := &lifecycleClock{now: time.Now()}
	if controller == nil {
		controller = &lifecycleController{}
	}
	life, err := NewHostedLifecycle(owner, next, controller, HostedLifecycleOptions{
		StartupGrace: time.Minute, IdleGrace: time.Minute,
		RenewalInterval: time.Hour, CheckInterval: time.Hour, Now: clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return owner, life, clock
}

func requestLifecycle(life *HostedLifecycle, method, path string) *httptest.ResponseRecorder {
	return requestLifecycleBody(life, method, path, "")
}

func requestLifecycleBody(life *HostedLifecycle, method, path, body string) *httptest.ResponseRecorder {
	var requestBody io.Reader
	if body != "" {
		requestBody = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, requestBody)
	if strings.HasPrefix(path, "/bridge/") {
		req.Header.Set("Authorization", "Bearer "+life.bridgeToken)
	} else if path == "/mcp" {
		req.Header.Set("Authorization", "Bearer "+life.mcpToken)
	}
	rr := httptest.NewRecorder()
	life.ServeHTTP(rr, req)
	return rr
}

func hostedBridgeCall(t *testing.T, owner *Owner, path, tab string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+owner.bridge.Addr()+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+owner.bridge.Token())
	if tab != "" {
		req.Header.Set(extbridge.TabIDHeader, tab)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func waitForPendingBridgeCommand(t *testing.T, owner *Owner) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !owner.bridge.HasPendingCommands() {
		if time.Now().After(deadline) {
			t.Fatal("bridge command did not become pending")
		}
		runtime.Gosched()
	}
}

func TestHostedLifecycleRequiresSuccessfulInitialProtection(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	controller := &lifecycleController{initialStarted: started, initialRelease: release}
	_, life, _ := newLifecycleFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok\n"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}), controller)
	result := make(chan error, 1)
	go func() { result <- life.establishProtection(context.Background()) }()
	<-started
	if got := requestLifecycle(life, http.MethodGet, "/healthz").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("health while initial Protect is unresolved: %d", got)
	}
	if got := requestLifecycle(life, http.MethodPost, "/mcp").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("application request while initial Protect is unresolved: %d", got)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if got := requestLifecycle(life, http.MethodGet, "/healthz").Code; got != http.StatusOK {
		t.Fatalf("health after initial protection: %d", got)
	}
	protects, sleeps := controller.calls()
	if len(protects) != 1 || !protects[0] || sleeps != 0 {
		t.Fatalf("controller calls before readiness: Protect=%v Sleep=%d", protects, sleeps)
	}
}

func TestHostedLifecycleIdleDrainWaitsForConnectionOwnerLeaseAndCommands(t *testing.T) {
	owner, life, clock := newLifecycleFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), nil)
	if err := life.establishProtection(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Minute)

	checks := []struct {
		name  string
		set   func()
		clear func()
	}{
		{
			name: "connected browser",
			set: func() {
				if got := hostedBridgeCall(t, owner, "/pair", "browser.42"); got != http.StatusNoContent {
					t.Fatalf("pair status %d", got)
				}
			},
			clear: func() {
				if got := hostedBridgeCall(t, owner, "/disconnect", "browser.42"); got != http.StatusNoContent {
					t.Fatalf("disconnect status %d", got)
				}
			},
		},
		{
			name: "active owner task",
			set: func() {
				owner.mu.Lock()
				owner.activeOwner = "session"
				owner.activeCancel = func() {}
				owner.mu.Unlock()
			},
			clear: func() { owner.mu.Lock(); owner.activeOwner = ""; owner.activeCancel = nil; owner.mu.Unlock() },
		},
		{
			name: "unexpired lease",
			set: func() {
				owner.mu.Lock()
				owner.leaseOwner = "session"
				owner.leaseUntil = clock.Now().Add(time.Minute)
				owner.mu.Unlock()
			},
			clear: func() { owner.mu.Lock(); owner.leaseOwner = ""; owner.leaseUntil = time.Time{}; owner.mu.Unlock() },
		},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			tc.set()
			done, err := life.step(context.Background())
			if err != nil || done {
				t.Fatalf("step with %s: done=%v err=%v", tc.name, done, err)
			}
			_, sleeps := life.controller.(*lifecycleController).calls()
			if sleeps != 0 {
				t.Fatal("controller slept while work guard was active")
			}
			tc.clear()
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	pending := make(chan error, 1)
	go func() {
		_, err := owner.bridge.Enqueue(ctx, extbridge.Command{Op: "navigate", URL: "https://example.test"})
		pending <- err
	}()
	waitForPendingBridgeCommand(t, owner)
	done, err := life.step(context.Background())
	if err != nil || done {
		t.Fatalf("step with pending bridge command: done=%v err=%v", done, err)
	}
	cancel()
	if err := <-pending; err == nil {
		t.Fatal("canceled pending bridge command returned no error")
	}
	if owner.bridge.HasPendingCommands() {
		t.Fatal("canceled bridge command remained pending")
	}
	done, err = life.step(context.Background())
	if err != nil || !done {
		t.Fatalf("idle step: done=%v err=%v", done, err)
	}
	if got := requestLifecycle(life, http.MethodPost, "/mcp").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("new application work after drain: %d", got)
	}
	if got := requestLifecycle(life, http.MethodGet, "/healthz").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("health after drain: %d", got)
	}
	protects, sleeps := life.controller.(*lifecycleController).calls()
	if len(protects) != 2 || protects[0] != true || protects[1] != false || sleeps != 1 {
		t.Fatalf("drain calls: Protect=%v Sleep=%d", protects, sleeps)
	}
}

func TestHostedLifecycleRequestGateDoesNotDeadlockBridgePollOrReply(t *testing.T) {
	started, pollStarted, replied := make(chan struct{}), make(chan struct{}), make(chan struct{})
	release, pollRelease := make(chan struct{}), make(chan struct{})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			close(started)
			<-release
			w.WriteHeader(http.StatusNoContent)
		case "/bridge/next":
			close(pollStarted)
			<-pollRelease
			w.WriteHeader(http.StatusNoContent)
		case "/bridge/reply":
			close(replied)
			close(release)
			close(pollRelease)
			w.WriteHeader(http.StatusNoContent)
		}
	})
	_, life, clock := newLifecycleFixture(t, next, nil)
	if err := life.establishProtection(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Minute)
	mcpResult := make(chan int, 1)
	go func() { mcpResult <- requestLifecycle(life, http.MethodPost, "/mcp").Code }()
	<-started
	pollResult := make(chan int, 1)
	go func() { pollResult <- requestLifecycle(life, http.MethodGet, "/bridge/next").Code }()
	<-pollStarted
	done, err := life.step(context.Background())
	if err != nil || done {
		t.Fatalf("drain while MCP waits for bridge reply: done=%v err=%v", done, err)
	}
	replyResult := make(chan int, 1)
	go func() { replyResult <- requestLifecycle(life, http.MethodPost, "/bridge/reply").Code }()
	<-replied
	if got := <-replyResult; got != http.StatusNoContent {
		t.Fatalf("bridge reply status %d", got)
	}
	if got := <-mcpResult; got != http.StatusNoContent {
		t.Fatalf("MCP task status %d", got)
	}
	if got := <-pollResult; got != http.StatusNoContent {
		t.Fatalf("bridge poll status %d", got)
	}
	clock.Advance(time.Minute)
	done, err = life.step(context.Background())
	if err != nil || !done {
		t.Fatalf("drain after work completed: done=%v err=%v", done, err)
	}
}

func TestHostedLifecycleSSEAndPollDoNotHoldActivityGate(t *testing.T) {
	streamStarted, release := make(chan struct{}), make(chan struct{})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mcp" {
			close(streamStarted)
			<-release
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	_, life, clock := newLifecycleFixture(t, next, nil)
	if err := life.establishProtection(context.Background()); err != nil {
		t.Fatal(err)
	}
	initialLastWork := life.lastWork
	streamResult := make(chan int, 1)
	go func() { streamResult <- requestLifecycle(life, http.MethodGet, "/mcp").Code }()
	<-streamStarted
	if life.lastWork != initialLastWork {
		t.Fatal("SSE stream extended the application idle timestamp")
	}
	clock.Advance(2 * time.Minute)
	done, err := life.step(context.Background())
	if err != nil || !done {
		t.Fatalf("drain while SSE stream remains open: done=%v err=%v", done, err)
	}
	close(release)
	if got := <-streamResult; got != http.StatusOK {
		t.Fatalf("stream response status %d", got)
	}
}

func TestHostedLifecycleRenewalFailureAllowsOnlyInFlightBridgeCompletion(t *testing.T) {
	started, polled := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	controller := &lifecycleController{protectErrorAt: 2, protectErr: errors.New("renewal unavailable")}
	owner, life, clock := newLifecycleFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), controller)
	if got := hostedBridgeCall(t, owner, "/pair", "browser.42"); got != http.StatusNoContent {
		t.Fatalf("pair status %d", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	enqueued := make(chan error, 1)
	go func() {
		_, err := owner.bridge.Enqueue(ctx, extbridge.Command{Op: "navigate", URL: "https://example.test"})
		enqueued <- err
	}()
	waitForPendingBridgeCommand(t, owner)
	dispatchReq, err := http.NewRequest(http.MethodGet, "http://"+owner.bridge.Addr()+"/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	dispatchReq.Header.Set("Authorization", "Bearer "+owner.bridge.Token())
	dispatchReq.Header.Set(extbridge.TabIDHeader, "browser.42")
	dispatchResp, err := http.DefaultClient.Do(dispatchReq)
	if err != nil {
		t.Fatal(err)
	}
	var dispatched struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(dispatchResp.Body).Decode(&dispatched); err != nil {
		dispatchResp.Body.Close()
		t.Fatal(err)
	}
	dispatchResp.Body.Close()
	if dispatchResp.StatusCode != http.StatusOK || dispatched.ID == "" {
		t.Fatalf("dispatch status=%d id=%q", dispatchResp.StatusCode, dispatched.ID)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bridge/next":
			close(polled)
			w.WriteHeader(http.StatusNoContent)
		case "/bridge/reply":
			body, readErr := io.ReadAll(r.Body)
			if readErr != nil {
				http.Error(w, "read fixture reply", http.StatusBadRequest)
				return
			}
			replyReq, requestErr := http.NewRequest(http.MethodPost, "http://"+owner.bridge.Addr()+"/reply", strings.NewReader(string(body)))
			if requestErr != nil {
				http.Error(w, "build fixture reply", http.StatusInternalServerError)
				return
			}
			replyReq.Header.Set("Authorization", "Bearer "+owner.bridge.Token())
			replyResp, doErr := http.DefaultClient.Do(replyReq)
			if doErr != nil {
				http.Error(w, "deliver fixture reply", http.StatusBadGateway)
				return
			}
			replyResp.Body.Close()
			close(release)
			w.WriteHeader(replyResp.StatusCode)
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/mcp":
			close(started)
			<-release
			w.WriteHeader(http.StatusNoContent)
		}
	})
	life.next = next
	if err := life.establishProtection(context.Background()); err != nil {
		t.Fatal(err)
	}
	activeResult := make(chan int, 1)
	go func() { activeResult <- requestLifecycle(life, http.MethodPost, "/mcp").Code }()
	<-started
	clock.Advance(time.Hour)
	done, err := life.step(context.Background())
	if err != nil || done {
		t.Fatalf("renewal failure while work active: done=%v err=%v", done, err)
	}
	if got := requestLifecycle(life, http.MethodGet, "/healthz").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("health after renewal failure: %d", got)
	}
	if got := requestLifecycle(life, http.MethodPost, "/mcp").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("new MCP work after renewal failure: %d", got)
	}
	pollResult := make(chan int, 1)
	go func() { pollResult <- requestLifecycle(life, http.MethodGet, "/bridge/next").Code }()
	<-polled
	replyResult := make(chan int, 1)
	go func() {
		replyResult <- requestLifecycleBody(life, http.MethodPost, "/bridge/reply", `{"id":"`+dispatched.ID+`","result":"ok"}`).Code
	}()
	if got := <-pollResult; got != http.StatusNoContent {
		t.Fatalf("existing task poll was rejected: %d", got)
	}
	if got := <-replyResult; got != http.StatusNoContent {
		t.Fatalf("existing task reply was rejected: %d", got)
	}
	if got := <-activeResult; got != http.StatusNoContent {
		t.Fatalf("existing active request was interrupted: %d", got)
	}
	if err := <-enqueued; err != nil {
		t.Fatalf("extension action failed after reply: %v", err)
	}
	done, err = life.step(context.Background())
	if err != nil || done {
		t.Fatalf("connected browser must prevent sleep after task completion: done=%v err=%v", done, err)
	}
	_, sleeps := controller.calls()
	if sleeps != 0 {
		t.Fatal("lifecycle slept while the browser remained connected")
	}
	if got := hostedBridgeCall(t, owner, "/disconnect", "browser.42"); got != http.StatusNoContent {
		t.Fatalf("disconnect after completion status %d", got)
	}
	done, err = life.step(context.Background())
	if err == nil || !done || !errors.Is(err, controller.protectErr) {
		t.Fatalf("drain result after renewal failure: done=%v err=%v", done, err)
	}
	_, sleeps = controller.calls()
	if sleeps != 1 {
		t.Fatalf("Sleep calls=%d, want one", sleeps)
	}
	if got := requestLifecycle(life, http.MethodPost, "/mcp").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("application request reopened after failed renewal: %d", got)
	}
}

func TestHostedLifecycleFailuresRemainUnavailable(t *testing.T) {
	t.Run("initial protection", func(t *testing.T) {
		controller := &lifecycleController{protectErrorAt: 1, protectErr: errors.New("initial protect failed")}
		_, life, _ := newLifecycleFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), controller)
		if err := life.establishProtection(context.Background()); err == nil {
			t.Fatal("initial Protect failure was ignored")
		}
		if got := requestLifecycle(life, http.MethodPost, "/mcp").Code; got != http.StatusServiceUnavailable {
			t.Fatalf("request admitted after failed initial protection: %d", got)
		}
	})
	t.Run("protection release", func(t *testing.T) {
		controller := &lifecycleController{protectErrorAt: 2, protectErr: errors.New("release outcome unknown")}
		_, life, clock := newLifecycleFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), controller)
		if err := life.establishProtection(context.Background()); err != nil {
			t.Fatal(err)
		}
		clock.Advance(2 * time.Minute)
		done, err := life.step(context.Background())
		if err == nil || !done || !errors.Is(err, controller.protectErr) {
			t.Fatalf("Protect(false) failure: done=%v err=%v", done, err)
		}
		_, sleeps := controller.calls()
		if sleeps != 1 {
			t.Fatalf("Sleep calls=%d after failed Protect(false)", sleeps)
		}
		if got := requestLifecycle(life, http.MethodPost, "/mcp").Code; got != http.StatusServiceUnavailable {
			t.Fatalf("application request reopened after Protect(false) error: %d", got)
		}
	})
	t.Run("sleep failure", func(t *testing.T) {
		controller := &lifecycleController{sleepErr: errors.New("sleep outcome unknown")}
		_, life, clock := newLifecycleFixture(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), controller)
		if err := life.establishProtection(context.Background()); err != nil {
			t.Fatal(err)
		}
		clock.Advance(2 * time.Minute)
		done, err := life.step(context.Background())
		if err == nil || done {
			t.Fatalf("Sleep failure: done=%v err=%v", done, err)
		}
		if got := requestLifecycle(life, http.MethodPost, "/mcp").Code; got != http.StatusServiceUnavailable {
			t.Fatalf("application request reopened after Sleep error: %d", got)
		}
	})
}

func TestHostedLifecycleInvalidBearerDoesNotResetIdleTimestamp(t *testing.T) {
	_, life, _ := newLifecycleFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}), nil)
	if err := life.establishProtection(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := life.lastWork
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer incorrect")
	rr := httptest.NewRecorder()
	life.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("invalid bearer status %d, want 401", rr.Code)
	}
	if !life.lastWork.Equal(before) {
		t.Fatal("invalid bearer reset the hosted idle timestamp")
	}
}

func TestHostedLifecycleConstructorRequiresHostedOwner(t *testing.T) {
	t.Run("nil owner", func(t *testing.T) {
		if _, err := NewHostedLifecycle(nil, http.NotFoundHandler(), &lifecycleController{}, HostedLifecycleOptions{}); err == nil {
			t.Fatal("nil owner accepted")
		}
	})
	t.Run("nil handler", func(t *testing.T) {
		owner, _, _, _ := hostedFixture(t)
		if _, err := NewHostedLifecycle(owner, nil, &lifecycleController{}, HostedLifecycleOptions{}); err == nil {
			t.Fatal("nil handler accepted")
		}
	})
}

func TestHostedLifecycleHealthPreservesWrappedHostRejection(t *testing.T) {
	owner, hosted, _, _ := hostedFixture(t)
	controller := &lifecycleController{}
	life, err := NewHostedLifecycle(owner, hosted, controller, HostedLifecycleOptions{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Host = "attacker.example"
	rr := httptest.NewRecorder()
	life.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("health host rejection status %d, want 403", rr.Code)
	}
}
