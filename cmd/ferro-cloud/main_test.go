package main

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrepareCredentialCopiesPrivateTokenFile(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source-token")
	destination := filepath.Join(dir, "home", "bridge-token")
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("0123456789abcdef0123456789abcdef\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareCredential(source, destination); err != nil {
		t.Fatal(err)
	}
	got, err := secureToken(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got != "0123456789abcdef0123456789abcdef" {
		t.Fatal("destination token differs")
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("destination mode %o, want 600", info.Mode().Perm())
	}
}

func TestTrustedProxyHTTPSRequiresConfiguredPeerAndHTTPS(t *testing.T) {
	check := func(remote, forwarded string, trusted []netip.Prefix) bool {
		t.Helper()
		var marked bool
		handler := trustedProxyHTTPS(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { marked = r.TLS != nil }), trusted)
		req := httptest.NewRequest(http.MethodGet, "http://ferro.sire.run/healthz", nil)
		req.RemoteAddr = remote
		req.Header.Set("X-Forwarded-Proto", forwarded)
		handler.ServeHTTP(httptest.NewRecorder(), req)
		return marked
	}
	if !check("127.0.0.1:1234", "https", nil) {
		t.Fatal("default loopback HTTPS proxy was not trusted")
	}
	private := []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16")}
	if !check("10.20.1.4:1234", "https", private) {
		t.Fatal("configured proxy CIDR was not trusted")
	}
	if check("10.21.1.4:1234", "https", private) {
		t.Fatal("untrusted private peer spoofed forwarded HTTPS")
	}
	if check("127.0.0.1:1234", "https", private) {
		t.Fatal("explicit CIDR mode implicitly trusted loopback")
	}
	if check("127.0.0.1:1234", "http", nil) {
		t.Fatal("loopback HTTP request was marked TLS")
	}
}

func TestParseTrustedProxyCIDRsRequiresCanonicalPrivateRanges(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		wantErr     bool
	}{
		{"private v4", "10.0.0.0/8", false},
		{"private v6", "fc00::/7", false},
		{"loopback", "127.0.0.0/8", false},
		{"default route", "0.0.0.0/0", true},
		{"public range", "203.0.113.0/24", true},
		{"noncanonical host bits", "10.1.2.0/8", true},
		{"whitespace", "10.0.0.0/8, 10.1.0.0/16", true},
		{"duplicate", "10.0.0.0/8,10.0.0.0/8", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTrustedProxyCIDRs(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("prefixes=%v err=%v wantErr=%v", got, err, tc.wantErr)
			}
		})
	}
}

func TestValidateListenAddrRequiresExplicitTrustForPublicBind(t *testing.T) {
	for _, tc := range []struct {
		name, addr       string
		trusted, wantErr bool
	}{
		{"default loopback", "127.0.0.1:8080", false, false},
		{"loopback with explicit proxy", "127.0.0.1:8080", true, false},
		{"wildcard no proxy trust", "0.0.0.0:8080", false, true},
		{"wildcard with proxy trust", "0.0.0.0:8080", true, false},
		{"public address", "203.0.113.2:8080", true, true},
		{"invalid port", "127.0.0.1:0", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateListenAddr(tc.addr, tc.trusted)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestTrustedProxyHealthCheckUsesTrustedPeerIPHostAndGETOnly(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16")}
	handler := trustedProxyHTTPS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "application handler reached", http.StatusForbidden)
	}), trusted)
	request := func(remote, host, method, forwarded string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(method, "http://"+host+"/healthz", nil)
		req.RemoteAddr = remote
		if forwarded != "" {
			req.Header.Set("X-Forwarded-Proto", forwarded)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder.Code, recorder.Body.String()
	}
	if status, body := request("10.20.1.4:9000", "10.20.4.8:8080", http.MethodGet, ""); status != http.StatusOK || body != "ok\n" {
		t.Fatalf("trusted health status=%d body=%q", status, body)
	}
	if status, _ := request("10.20.1.4:9000", "ferro.sire.run", http.MethodGet, ""); status == http.StatusOK {
		t.Fatal("health bypass accepted DNS Host")
	}
	if status, _ := request("10.21.1.4:9000", "10.20.4.8:8080", http.MethodGet, ""); status == http.StatusOK {
		t.Fatal("health bypass accepted untrusted peer")
	}
	if status, _ := request("10.20.1.4:9000", "10.20.4.8:8080", http.MethodPost, ""); status != http.StatusMethodNotAllowed {
		t.Fatalf("health method status=%d", status)
	}
	if status, _ := request("10.20.1.4:9000", "10.20.4.8:8080", http.MethodGet, "https"); status != http.StatusForbidden {
		t.Fatalf("health protocol status=%d", status)
	}
}

func TestPrepareCredentialRejectsConflictAndWeakFile(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source-token")
	destination := filepath.Join(dir, "bridge-token")
	if err := os.WriteFile(source, []byte("0123456789abcdef0123456789abcdef\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("abcdef0123456789abcdef0123456789\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareCredential(source, destination); err == nil {
		t.Fatal("expected conflicting private token files to be rejected")
	}
	weak := filepath.Join(dir, "weak-token")
	if err := os.WriteFile(weak, []byte("short\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareCredential(weak, filepath.Join(dir, "other-token")); err == nil {
		t.Fatal("expected weak token to be rejected")
	}
}

func TestPrepareCredentialValuePersistsPrivateValueAndRejectsConflict(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "home", "remote-token")
	value := "0123456789abcdef0123456789abcdef"
	if err := prepareCredentialInput("", value, dest); err != nil {
		t.Fatal(err)
	}
	got, err := secureToken(dest)
	if err != nil || got != value {
		t.Fatalf("persisted token differs: got=%q err=%v", got, err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o, want 600", info.Mode().Perm())
	}
	if err := prepareCredentialInput("", value, dest); err != nil {
		t.Fatalf("same value should be stable: %v", err)
	}
	if err := prepareCredentialInput("", strings.Repeat("a", 32), dest); err == nil {
		t.Fatal("conflicting value was accepted")
	}
	if err := prepareCredentialInput("some-token-file", value, dest); err == nil {
		t.Fatal("simultaneous file and value inputs were accepted")
	}
	if err := prepareCredentialInput("", strings.Repeat("x", 31), filepath.Join(dir, "weak")); err == nil {
		t.Fatal("weak token value was accepted")
	}
	if err := prepareCredentialInput("", "0123456789abcdef0123456789abcde\n", filepath.Join(dir, "newline")); err == nil {
		t.Fatal("newline token value was accepted")
	}
}

func TestHostedRequestDeadlineIncludesQueueButExemptsEventStream(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		bounded      bool
	}{
		{"POST", "/mcp", true}, {"POST", "/bridge/chat/run", true}, {"GET", "/bridge/next", true}, {"GET", "/mcp", false},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			h := boundHostedRequests(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				deadline, ok := r.Context().Deadline()
				if ok != tc.bounded {
					t.Fatalf("bounded=%v want=%v", ok, tc.bounded)
				}
				if ok && (time.Until(deadline) > 50*time.Second || time.Until(deadline) < 49*time.Second) {
					t.Fatal("unexpected deadline")
				}
			}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, "http://ferro.sire.run"+tc.path, nil))
		})
	}
}
