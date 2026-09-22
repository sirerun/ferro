package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type authTransport struct{ token string }

func (a authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+a.token)
	return http.DefaultTransport.RoundTrip(r)
}
func extensionOwner(t *testing.T) *Owner {
	t.Helper()
	o, err := NewOwner(context.Background(), Config{Home: shortTempDir(t), Backend: "extension", BridgeAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })
	return o
}
func TestRemoteTransport(t *testing.T) {
	o := extensionOwner(t)
	srv := httptest.NewServer(remoteHandler(o, "test-token", ""))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, token := range []string{"", "wrong"} {
		req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/mcp", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatalf("bad token accepted %d", res.StatusCode)
		}
	}
	connect := func() *sdk.ClientSession {
		t.Helper()
		c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil)
		s, err := c.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: authTransport{"test-token"}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	a, b := connect(), connect()
	call := func(s *sdk.ClientSession, name string) *sdk.CallToolResult {
		t.Helper()
		r, e := s.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: map[string]any{}})
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	if r := call(a, "acquire_tab"); r.IsError {
		t.Fatalf("acquire: %+v", r)
	}
	if r := call(b, "acquire_tab"); !r.IsError {
		t.Fatal("second session stole lease")
	}
	if r := call(a, "browser_status"); r.IsError {
		t.Fatalf("same session identity lost across tools: %+v", r)
	}
	if r := call(a, "release_tab"); r.IsError {
		t.Fatalf("release: %+v", r)
	}
	if r := call(b, "acquire_tab"); r.IsError {
		t.Fatal("lease not released")
	}
	if r := call(b, "snapshot"); !r.IsError {
		t.Fatal("disconnected extension produced success")
	}
	child := func(mode string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRemoteTransportClientProcess$")
		cmd.Env = append(os.Environ(), "FERRO_TEST_REMOTE_URL="+srv.URL+"/mcp", "FERRO_TEST_REMOTE_MODE="+mode)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("remote subprocess (%s): %v\n%s", mode, err, out)
		}
	}
	child("online")
	_ = a.Close()
	_ = b.Close()
	srv.Close()
	child("offline")
	deadline, cancel2 := context.WithTimeout(ctx, time.Second)
	defer cancel2()
	req, _ := http.NewRequestWithContext(deadline, "GET", srv.URL+"/mcp", nil)
	if res, err := http.DefaultClient.Do(req); err == nil {
		res.Body.Close()
		t.Fatal("closed service unexpectedly responded")
	}
}
func TestTailnetBindingRejectsOtherInterfaces(t *testing.T) {
	local := []net.Addr{&net.IPNet{IP: net.ParseIP("100.99.0.1"), Mask: net.CIDRMask(32, 32)}}
	for _, host := range []string{"0.0.0.0", "127.0.0.1", "192.168.1.1", "8.8.8.8", "100.99.0.2"} {
		if _, e := validateTailnetHost(host, "100.99.0.1", local); e == nil {
			t.Fatalf("accepted %s", host)
		}
	}
	if _, e := validateTailnetHost("", "100.99.0.1", local); e != nil {
		t.Fatal(e)
	}
	if _, e := validateTailnetHost("", "100.99.0.1", nil); e == nil {
		t.Fatal("accepted unassigned interface")
	}
}
func TestTokenFilePermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "token")
	a, e := loadToken(p)
	if e != nil {
		t.Fatal(e)
	}
	b, e := loadToken(p)
	if e != nil || a != b {
		t.Fatal("token not stable")
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0600 {
		t.Fatal("token permissions")
	}
	if e = os.Chmod(p, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = loadToken(p); e == nil {
		t.Fatal("accepted readable token")
	}
}
func TestLeaseCancellationAndExpiry(t *testing.T) {
	o := extensionOwner(t)
	a := context.WithValue(context.Background(), clientKey{}, "a")
	b := context.WithValue(context.Background(), clientKey{}, "b")
	call := func(ctx context.Context, tool string) (string, bool) {
		t.Helper()
		s, b, e := o.Call(ctx, tool, json.RawMessage(`{}`))
		if e != nil {
			t.Fatal(e)
		}
		return s, b
	}
	if _, err := call(a, "snapshot"); !err {
		t.Fatal("missing lease accepted")
	}
	call(a, "acquire_tab")
	if _, err := call(b, "release_tab"); !err {
		t.Fatal("released someone else's lease")
	}
	run, cancel := context.WithCancel(a)
	defer cancel()
	o.mu.Lock()
	o.activeOwner = "a"
	o.activeCancel = cancel
	o.mu.Unlock()
	if _, err := call(b, "cancel_task"); !err {
		t.Fatal("canceled someone else's task")
	}
	if run.Err() != nil {
		t.Fatal("unauthorized cancel took effect")
	}
	call(a, "cancel_task")
	if run.Err() == nil {
		t.Fatal("cancel not delivered")
	}
	o.mu.Lock()
	o.activeCancel = nil
	o.leaseUntil = time.Now().Add(-time.Second)
	o.mu.Unlock()
	if _, err := call(b, "acquire_tab"); err {
		t.Fatal("expired lease blocked new owner")
	}
}
func TestRemoteRejectsBrowserOrigins(t *testing.T) {
	o := extensionOwner(t)
	h := remoteHandler(o, "token", "127.0.0.1:1234")
	for _, header := range []string{"Origin", "Host"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:1234/mcp", strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer token")
		if header == "Origin" {
			r.Header.Set(header, "https://evil.test")
		} else {
			r.Host = "evil.test"
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("%s allowed: %d", header, w.Code)
		}
	}
}
func TestPrimitivesDoNotRequireModel(t *testing.T) {
	t.Setenv("FERRO_MCP_HOME", t.TempDir())
	t.Setenv("FERRO_MCP_LLM_BASE_URL", "")
	t.Setenv("FERRO_MCP_LLM_MODEL", "")
	t.Setenv("FERRO_MCP_BACKEND", "extension")
	if _, e := ConfigFromEnv(); e != nil {
		t.Fatal(e)
	}
}

// A second OS process exercises the real HTTP transport without sharing owner
// or SDK session objects with the server process.
func TestRemoteTransportClientProcess(t *testing.T) {
	endpoint := os.Getenv("FERRO_TEST_REMOTE_URL")
	if endpoint == "" {
		t.Skip("invoked by TestRemoteTransport")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if os.Getenv("FERRO_TEST_REMOTE_MODE") == "offline" {
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
		if res, err := http.DefaultClient.Do(req); err == nil {
			res.Body.Close()
			t.Fatal("offline endpoint accepted connection")
		}
		return
	}
	c := sdk.NewClient(&sdk.Implementation{Name: "remote-subprocess", Version: "1"}, nil)
	session, err := c.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{Transport: authTransport{"test-token"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	r, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "browser_status", Arguments: map[string]any{}})
	if err != nil || r.IsError {
		t.Fatalf("remote tool: %v %+v", err, r)
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer wrong")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("invalid token: HTTP %d", res.StatusCode)
	}
}
