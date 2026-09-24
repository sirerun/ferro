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
	"os"
	"os/signal"
	"path/filepath"
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

	if err := prepareCredential(os.Getenv("FERRO_CLOUD_BRIDGE_TOKEN"), filepath.Join(cfg.Home, "bridge-token")); err != nil {
		return fmt.Errorf("prepare bridge credential: %w", err)
	}
	if err := prepareCredential(os.Getenv("FERRO_CLOUD_MCP_TOKEN"), filepath.Join(cfg.Home, "remote-token")); err != nil {
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
	if host, _, err := net.SplitHostPort(listenAddr); err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("FERRO_CLOUD_LISTEN_ADDR must be a loopback address behind the HTTPS proxy")
	}

	cfg.Backend = "extension"
	cfg.BridgeAddr = "127.0.0.1:0"
	cfg.Remote = false
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
	server := &http.Server{Addr: listenAddr, Handler: trustedProxyHTTPS(handler), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
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

func trustedProxyHTTPS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(peer)
		if err == nil && ip != nil && ip.IsLoopback() && r.Header.Get("X-Forwarded-Proto") == "https" {
			r.TLS = &tls.ConnectionState{}
		}
		next.ServeHTTP(w, r)
	})
}
