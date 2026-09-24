package mcp

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hostedFixture(t *testing.T) (*Owner, http.Handler, string, string) {
	t.Helper()
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	owner, err := NewOwner(context.Background(), Config{Home: home, Backend: "extension", BridgeAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Errorf("close owner: %v", err)
		}
	})
	mcpToken, err := loadToken(filepath.Join(home, "remote-token"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewPrivateHostedHandler(owner, "https://ferro.sire.run")
	if err != nil {
		t.Fatal(err)
	}
	return owner, h, mcpToken, owner.bridge.Token()
}

func TestPrivateHostedAuthAndRouting(t *testing.T) {
	owner, handler, mcpToken, bridgeToken := hostedFixture(t)
	cases := []struct {
		name, path, auth string
		origin           bool
		want             int
	}{
		{"missing MCP token", "/mcp", "", false, http.StatusUnauthorized},
		{"wrong MCP token", "/mcp", "Bearer wrong", false, http.StatusUnauthorized},
		{"browser MCP origin", "/mcp", "Bearer " + mcpToken, true, http.StatusForbidden},
		{"missing bridge token", "/bridge/pair", "", false, http.StatusUnauthorized},
		{"wrong bridge token", "/bridge/pair", "Bearer " + mcpToken, false, http.StatusUnauthorized},
		{"correct bridge token", "/bridge/pair", "Bearer " + bridgeToken, false, http.StatusBadRequest},
		{"wrong host", "/bridge/pair", "Bearer " + bridgeToken, false, http.StatusForbidden},
		{"unknown path", "/private", "", false, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("{}"))
			req.Host = "ferro.sire.run"
			req.TLS = &tls.ConnectionState{}
			if tc.name == "wrong host" {
				req.Host = "attacker.example"
			}
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			if tc.origin {
				req.Header.Set("Origin", "chrome-extension://example")
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("status %d, want %d; body %q", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
	validMCP := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	validMCP.Host = "ferro.sire.run"
	validMCP.TLS = &tls.ConnectionState{}
	validMCP.Header.Set("Authorization", "Bearer "+mcpToken)
	validMCPResponse := httptest.NewRecorder()
	handler.ServeHTTP(validMCPResponse, validMCP)
	if validMCPResponse.Code == http.StatusUnauthorized || validMCPResponse.Code == http.StatusForbidden {
		t.Fatalf("valid MCP token rejected: %d %s", validMCPResponse.Code, validMCPResponse.Body.String())
	}

	// A valid pair request reaches the loopback bridge after /bridge is
	// stripped, and the upstream sees its own loopback Host for /chat guards.
	req := httptest.NewRequest(http.MethodPost, "/bridge/pair", nil)
	req.Host = "ferro.sire.run"
	req.TLS = &tls.ConnectionState{}
	req.Header.Set("Authorization", "Bearer "+bridgeToken)
	req.Header.Set("X-Ferro-Tab-Id", "42")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("pair status %d: %s", rr.Code, rr.Body.String())
	}

	// The proxy also preserves the chat guard's loopback Host requirement.
	req = httptest.NewRequest(http.MethodPost, "/bridge/chat/status", strings.NewReader("{}"))
	req.Host = "ferro.sire.run"
	req.TLS = &tls.ConnectionState{}
	req.Header.Set("Authorization", "Bearer "+bridgeToken)
	req.Header.Set("Origin", "chrome-extension://test-extension")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ferro-Chat-Session", "hosted-proxy-test")
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("proxied chat status %d: %s (bridge %s)", rr.Code, rr.Body.String(), owner.bridge.Addr())
	}
}

func TestPrivateHostedHealthIsPrivateInfoFree(t *testing.T) {
	_, handler, _, _ := hostedFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Host = "ferro.sire.run"
	req.TLS = &tls.ConnectionState{}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "ok\n" {
		t.Fatalf("health response: %d %q", rr.Code, rr.Body.String())
	}
}

func TestPrivateHostedOriginValidation(t *testing.T) {
	cases := []struct {
		name, origin string
		valid        bool
	}{
		{"approved https", "https://ferro.sire.run", true},
		{"https custom port", "https://ferro.sire.run:8443", true},
		{"loopback http test", "http://127.0.0.1:8443", true},
		{"remote http", "http://ferro.sire.run", false},
		{"arbitrary https", "https://other.example", true},
		{"path", "https://ferro.sire.run/elsewhere", false},
		{"userinfo", "https://user@ferro.sire.run", false},
		{"nonloopback http", "http://192.0.2.3", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			owner, err := NewOwner(context.Background(), Config{Home: home, Backend: "extension", BridgeAddr: "127.0.0.1:0"})
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			_, err = NewPrivateHostedHandler(owner, tc.origin)
			if (err == nil) != tc.valid {
				t.Fatalf("origin valid=%v, got err=%v", tc.valid, err)
			}
		})
	}
}

func TestPrivateHostedRejectsWeakOrReusedCredentials(t *testing.T) {
	t.Run("weak MCP token", func(t *testing.T) {
		home := t.TempDir()
		owner, err := NewOwner(context.Background(), Config{Home: home, Backend: "extension", BridgeAddr: "127.0.0.1:0"})
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		if err := os.WriteFile(filepath.Join(home, "remote-token"), []byte("short\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewPrivateHostedHandler(owner, "https://ferro.sire.run"); err == nil {
			t.Fatal("expected weak MCP token rejection")
		}
	})
	t.Run("reused bridge token", func(t *testing.T) {
		home := t.TempDir()
		owner, err := NewOwner(context.Background(), Config{Home: home, Backend: "extension", BridgeAddr: "127.0.0.1:0"})
		if err != nil {
			t.Fatal(err)
		}
		defer owner.Close()
		if err := os.WriteFile(filepath.Join(home, "remote-token"), []byte(owner.bridge.Token()+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewPrivateHostedHandler(owner, "https://ferro.sire.run"); err == nil {
			t.Fatal("expected reused credential rejection")
		}
	})
}
