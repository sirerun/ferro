package mcp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// loadToken creates a private credential without ever printing its value.
func loadToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return "", fmt.Errorf("%s must be a regular file with mode 0600", filepath.Base(path))
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		token := strings.TrimSpace(string(b))
		if len(token) < 32 {
			return "", fmt.Errorf("%s token is too short", filepath.Base(path))
		}
		return token, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	var b [32]byte
	if _, err = rand.Read(b[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b[:])
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString(token + "\n")
	cerr := f.Close()
	if err = errors.Join(werr, cerr); err != nil {
		return "", err
	}
	return token, nil
}

func validateTailnetHost(host, resolved string, assigned []net.Addr) (string, error) {
	ip, err := netip.ParseAddr(strings.TrimSpace(resolved))
	if err != nil || !netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
		return "", fmt.Errorf("Tailscale did not return a tailnet IPv4 address")
	}
	if host != "" && host != ip.String() {
		return "", fmt.Errorf("FERRO_MCP_BIND_HOST must equal this machine's Tailscale IPv4 address")
	}
	for _, a := range assigned {
		p, err := netip.ParsePrefix(a.String())
		if err == nil && p.Addr() == ip {
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("Tailscale IP is not assigned to a local interface")
}
func (o *Owner) startRemote(ctx context.Context) error {
	lookup, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(lookup, "tailscale", "ip", "-4").Output()
	if err != nil {
		if o.cfg.BindHost != "" {
			return fmt.Errorf("cannot verify explicit Tailscale bind: %w", err)
		}
		log.Printf("ferro-mcp: remote listener disabled: Tailscale unavailable")
		return nil
	}
	assigned, err := net.InterfaceAddrs()
	if err != nil {
		return err
	}
	host, err := validateTailnetHost(o.cfg.BindHost, string(output), assigned)
	if err != nil {
		return err
	}
	port := o.cfg.RemotePort
	if port == 0 {
		port = 4174
	}
	token, err := loadToken(filepath.Join(o.cfg.Home, "remote-token"))
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	o.remoteAddr = ln.Addr().String()
	o.remoteServer = &http.Server{Handler: remoteHandler(o, token, o.remoteAddr), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		if err := o.remoteServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("ferro-mcp: remote listener stopped: %v", err)
		}
	}()
	log.Printf("ferro-mcp: remote MCP at http://%s/mcp (bearer token in remote-token)", o.remoteAddr)
	return nil
}

// remoteHandler is separate from binding so CI can exercise the exact HTTP
// transport on loopback. Production binding always passes validateTailnetHost.
func remoteHandler(c caller, token, host string) http.Handler {
	server := NewServer(c)
	// An idle session must remain valid for the entire longest tab lease.
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{SessionTimeout: time.Duration(maxLeaseSeconds)*time.Second + 5*time.Minute})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Origin") != "" {
			http.Error(w, "browser origins are not accepted", http.StatusForbidden)
			return
		}
		if host != "" && r.Host != host {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		handler.ServeHTTP(w, r)
	})
}
