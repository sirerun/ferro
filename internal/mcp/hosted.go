package mcp

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
)

// LoadPrivateHostedToken loads or creates a strong token using the same
// private-file rules as the existing MCP and bridge credentials.
func LoadPrivateHostedToken(path string) (string, error) { return loadToken(path) }

// NewPrivateHostedHandler exposes the existing authenticated remote MCP and
// extension bridge transports behind one exact public origin. Use an HTTPS
// origin in production. HTTP is accepted only for a loopback origin so tests
// can exercise the handler without TLS. The owner's bridge remains loopback;
// /bridge/* is reverse-proxied to it with the prefix removed.
func NewPrivateHostedHandler(owner *Owner, publicOrigin string) (http.Handler, error) {
	if owner == nil || owner.bridge == nil || owner.cfg.Backend != "extension" {
		return nil, fmt.Errorf("private hosted service requires an extension-backed owner")
	}
	origin, err := url.Parse(publicOrigin)
	if err != nil || origin == nil || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") || origin.Host == "" {
		return nil, fmt.Errorf("public origin must be an HTTPS origin")
	}
	if origin.Scheme != "https" && !(origin.Scheme == "http" && isLoopbackHost(origin.Hostname())) {
		return nil, fmt.Errorf("public origin must use HTTPS (HTTP is allowed for loopback tests)")
	}
	if origin.Port() != "" && (origin.Scheme == "https" && origin.Port() == "443" || origin.Scheme == "http" && origin.Port() == "80") {
		return nil, fmt.Errorf("public origin must use its canonical host")
	}
	if origin.Scheme == "https" && origin.Port() != "" {
		if _, err := net.LookupPort("tcp", origin.Port()); err != nil {
			return nil, fmt.Errorf("invalid public origin port")
		}
	}
	if strings.ToLower(origin.Host) != origin.Host {
		return nil, fmt.Errorf("public origin host must be lowercase")
	}

	mcpToken, err := loadToken(filepath.Join(owner.cfg.Home, "remote-token"))
	if err != nil {
		return nil, fmt.Errorf("load private MCP token: %w", err)
	}
	bridgeToken := owner.bridge.Token()
	if len(bridgeToken) < 32 || bridgeToken == mcpToken {
		return nil, fmt.Errorf("bridge and MCP tokens must be distinct strong credentials")
	}
	publicHost := origin.Host
	mcp := remoteHandler(owner, mcpToken, publicHost)
	target := &url.URL{Scheme: "http", Host: owner.bridge.Addr()}
	bridge := httputil.NewSingleHostReverseProxy(target)
	bridge.Director = func(r *http.Request) {
		r.URL.Scheme = target.Scheme
		r.URL.Host = target.Host
		r.Host = target.Host
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/bridge")
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
		r.URL.RawPath = ""
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != publicHost {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if origin.Scheme == "https" && r.TLS == nil {
			http.Error(w, "HTTPS required", http.StatusForbidden)
			return
		}
		switch {
		case r.URL.Path == "/healthz" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok\n"))
		case r.URL.Path == "/mcp":
			mcp.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/bridge/"):
			bridge.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	}), nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
