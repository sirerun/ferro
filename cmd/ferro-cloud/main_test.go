package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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

func TestTrustedProxyHTTPSRequiresLoopbackPeer(t *testing.T) {
	check := func(remote, forwarded string) bool {
		t.Helper()
		var marked bool
		handler := trustedProxyHTTPS(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { marked = r.TLS != nil }))
		req := httptest.NewRequest(http.MethodGet, "http://ferro.sire.run/healthz", nil)
		req.RemoteAddr = remote
		req.Header.Set("X-Forwarded-Proto", forwarded)
		handler.ServeHTTP(httptest.NewRecorder(), req)
		return marked
	}
	if !check("127.0.0.1:1234", "https") {
		t.Fatal("loopback HTTPS proxy was not trusted")
	}
	if check("203.0.113.1:1234", "https") {
		t.Fatal("remote forwarded HTTPS header was trusted")
	}
	if check("127.0.0.1:1234", "http") {
		t.Fatal("loopback HTTP request was marked TLS")
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
