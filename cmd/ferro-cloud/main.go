// Command ferro-cloud serves the private, single-owner Ferro hosted pilot.
// It requires a persistent FERRO_MCP_HOME, an extension-paired owner, and a
// reverse proxy that terminates HTTPS for https://ferro.sire.run.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	fmcp "github.com/dndungu/ferro/internal/mcp"
)

const pilotOrigin = "https://ferro.sire.run"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (runErr error) {
	if os.Getenv("FERRO_MCP_HOME") == "" {
		return fmt.Errorf("FERRO_MCP_HOME must point to persistent private storage")
	}
	cfg, err := fmcp.ConfigFromEnv()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.Home, 0700); err != nil {
		return fmt.Errorf("create persistent home: %w", err)
	}
	if err := os.Chmod(cfg.Home, 0700); err != nil {
		return fmt.Errorf("secure persistent home: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(cfg.Home, "ferro-cloud.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open single-owner lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another ferro-cloud process owns this home")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	if err := prepareCredentialInput(os.Getenv("FERRO_CLOUD_BRIDGE_TOKEN"), os.Getenv("FERRO_CLOUD_BRIDGE_TOKEN_VALUE"), filepath.Join(cfg.Home, "bridge-token")); err != nil {
		return fmt.Errorf("prepare bridge credential: %w", err)
	}
	if err := prepareCredentialInput(os.Getenv("FERRO_CLOUD_MCP_TOKEN"), os.Getenv("FERRO_CLOUD_MCP_TOKEN_VALUE"), filepath.Join(cfg.Home, "remote-token")); err != nil {
		return fmt.Errorf("prepare MCP credential: %w", err)
	}
	bridgeToken, err := secureToken(filepath.Join(cfg.Home, "bridge-token"))
	if err != nil {
		return err
	}
	mcpToken, err := secureToken(filepath.Join(cfg.Home, "remote-token"))
	if err != nil {
		return err
	}
	if bridgeToken == mcpToken {
		return fmt.Errorf("bridge and MCP credentials must be distinct")
	}
	publicOrigin := os.Getenv("FERRO_CLOUD_PUBLIC_ORIGIN")
	if publicOrigin == "" {
		publicOrigin = pilotOrigin
	}
	if publicOrigin != pilotOrigin {
		return fmt.Errorf("FERRO_CLOUD_PUBLIC_ORIGIN must be %s", pilotOrigin)
	}
	listenAddr := os.Getenv("FERRO_CLOUD_LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = "127.0.0.1:8080"
	}
	trustedProxies, err := parseTrustedProxyCIDRs(os.Getenv("FERRO_CLOUD_TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return fmt.Errorf("configure trusted proxies: %w", err)
	}
	if err := validateListenAddr(listenAddr, len(trustedProxies) > 0); err != nil {
		return fmt.Errorf("invalid FERRO_CLOUD_LISTEN_ADDR: %w", err)
	}

	cfg.Backend = "extension"
	cfg.BridgeAddr = "127.0.0.1:0"
	cfg.Remote = false
	cfg.BlockTimeout = 45 * time.Second
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	owner, err := fmcp.NewOwner(ctx, cfg)
	if err != nil {
		return fmt.Errorf("start extension owner: %w", err)
	}
	defer func() {
		if err := owner.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close owner: %w", err))
		}
	}()

	handler, err := fmcp.NewPrivateHostedHandler(owner, publicOrigin)
	if err != nil {
		return err
	}
	var publicHandler http.Handler = trustedProxyHTTPS(handler, trustedProxies)
	var lifecycleErr <-chan error
	if serviceARN := os.Getenv("FERRO_CLOUD_ECS_SERVICE_ARN"); serviceARN != "" {
		controller, err := newECSIdleController(ctx, serviceARN, os.Getenv("AWS_REGION"), os.Getenv("ECS_AGENT_URI"))
		if err != nil {
			return fmt.Errorf("configure container lifecycle: %w", err)
		}
		lifecycle, err := fmcp.NewHostedLifecycle(owner, publicHandler, controller, fmcp.HostedLifecycleOptions{})
		if err != nil {
			return fmt.Errorf("configure idle lifecycle: %w", err)
		}
		publicHandler = lifecycle.Handler()
		done := make(chan error, 1)
		lifecycleErr = done
		go func() { done <- lifecycle.Run(ctx) }()
	}
	server := &http.Server{Addr: listenAddr, Handler: boundHostedRequests(publicHandler), ReadTimeout: 10 * time.Second, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	log.Printf("ferro-cloud: private pilot listening behind HTTPS proxy on %s", listenAddr)
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shutdown hosted server: %w", err)
		}
		return nil
	case err := <-lifecycleErr:
		stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		if shutdownErr != nil {
			_ = server.Close()
		}
		return errors.Join(err, shutdownErr)
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve private hosted pilot: %w", err)
	}
}

// prepareCredential accepts an optional env value naming a private token
// file. loadToken creates a strong credential there when absent. Its value
// is copied to the owner's established token file without logging it.
func prepareCredential(source, destination string) error {
	if source == "" {
		_, err := secureToken(destination)
		return err
	}
	token, err := secureToken(source)
	if err != nil {
		return err
	}
	if existing, err := secureTokenIfExists(destination); err == nil {
		if existing != token {
			return fmt.Errorf("configured token conflicts with the owner's existing private token file")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.WriteString(token + "\n")
	closeErr := f.Close()
	return errors.Join(writeErr, closeErr)
}

// prepareCredentialInput preserves the existing path-based environment setting
// and accepts a distinct secret-injected value setting. Supplying both is
// ambiguous and fails before either value is read or copied.
func prepareCredentialInput(sourcePath, value, destination string) error {
	if sourcePath != "" && value != "" {
		return fmt.Errorf("configure either the token file path or token value, not both")
	}
	if value != "" {
		return prepareCredentialValue(value, destination)
	}
	return prepareCredential(sourcePath, destination)
}

func prepareCredentialValue(value, destination string) error {
	if len(value) < 32 || strings.TrimSpace(value) != value {
		return fmt.Errorf("configured token value is too short or has surrounding whitespace")
	}
	for _, b := range []byte(value) {
		if b < 0x21 || b > 0x7e {
			return fmt.Errorf("configured token value contains unsupported characters")
		}
	}
	if existing, err := secureTokenIfExists(destination); err == nil {
		if existing != value {
			return fmt.Errorf("configured token conflicts with the owner's existing private token file")
		}
		if err := os.Chmod(destination, 0600); err != nil {
			return fmt.Errorf("secure private token file: %w", err)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return fmt.Errorf("create private token directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".ferro-token-*")
	if err != nil {
		return fmt.Errorf("create private token temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.WriteString(value + "\n")
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write private token: %w", err)
	}
	if err = os.Link(tmpPath, destination); err != nil {
		return fmt.Errorf("install private token: %w", err)
	}
	dir, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return fmt.Errorf("open private token directory: %w", err)
	}
	syncErr := dir.Sync()
	closeErr = dir.Close()
	if err = errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("sync private token directory: %w", err)
	}
	return nil
}

func secureToken(path string) (string, error)         { return tokenAt(path, true) }
func secureTokenIfExists(path string) (string, error) { return tokenAt(path, false) }

func tokenAt(path string, create bool) (string, error) {
	if !create {
		if _, err := os.Lstat(path); err != nil {
			return "", err
		}
	}
	return fmcp.LoadPrivateHostedToken(path)
}

func parseTrustedProxyCIDRs(raw string) ([]netip.Prefix, error) {
	if raw == "" {
		return nil, nil
	}
	allowed := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("fc00::/7"),
		netip.MustParsePrefix("::1/128"),
	}
	seen := make(map[netip.Prefix]struct{})
	parts := strings.Split(raw, ",")
	trusted := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		prefix, err := netip.ParsePrefix(part)
		if err != nil || prefix.String() != part || prefix != prefix.Masked() || prefix.Addr().Zone() != "" || prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("proxy CIDRs must be canonical network prefixes")
		}
		if _, duplicate := seen[prefix]; duplicate {
			return nil, fmt.Errorf("duplicate trusted proxy CIDR")
		}
		contained := false
		for _, boundary := range allowed {
			if boundary.Addr().Is4() == prefix.Addr().Is4() && boundary.Bits() <= prefix.Bits() && boundary.Contains(prefix.Addr()) {
				contained = true
				break
			}
		}
		if !contained {
			return nil, fmt.Errorf("proxy CIDRs must be wholly within private or loopback ranges")
		}
		seen[prefix] = struct{}{}
		trusted = append(trusted, prefix)
	}
	return trusted, nil
}

func validateListenAddr(addr string, explicitProxyTrust bool) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("must be a literal IP address and port")
	}
	parsedPort, err := strconv.Atoi(port)
	if err != nil || parsedPort < 1 || parsedPort > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" {
		return fmt.Errorf("host must be a literal IP address")
	}
	ip = ip.Unmap()
	if ip.IsLoopback() {
		return nil
	}
	if ip.IsUnspecified() && explicitProxyTrust {
		return nil
	}
	return fmt.Errorf("non-loopback bind requires explicit trusted proxy CIDRs")
}

func trustedProxyHTTPS(next http.Handler, trusted []netip.Prefix) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer := trustedProxyPeer(r.RemoteAddr, trusted)
		if r.URL.Path == "/healthz" && peer && requestHostIsIP(r.Host) {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", http.MethodGet)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			forwarded := r.Header.Get("X-Forwarded-Proto")
			if r.TLS != nil || forwarded != "" && forwarded != "http" {
				http.Error(w, "health check requires plain HTTP", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok\n"))
			return
		}
		if peer && r.Header.Get("X-Forwarded-Proto") == "https" {
			r.TLS = &tls.ConnectionState{}
		}
		next.ServeHTTP(w, r)
	})
}

func trustedProxyPeer(remote string, trusted []netip.Prefix) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	if len(trusted) == 0 {
		return ip.IsLoopback()
	}
	for _, prefix := range trusted {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func requestHostIsIP(hostport string) bool {
	if ip, err := netip.ParseAddr(hostport); err == nil && ip.Zone() == "" {
		return true
	}
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Zone() == ""
}

// ALB has a shared 60-second idle timeout. Bound queued requests as well as
// active execution, leaving time for durable finalization and the response.
// MCP's optional GET event stream carries no executing browser task.
func boundHostedRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/mcp" {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
