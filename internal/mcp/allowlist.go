package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// Allowlist is the deny-by-default per-origin gate ADR 005 requires: a flat
// list of allowed origins (scheme+host+port, e.g. "https://mail.google.com")
// loaded from $FERRO_MCP_HOME/allowlist.json. A missing file means an empty
// allowlist -- deny-by-default, not a startup error, since a fresh
// $FERRO_MCP_HOME has no allowlist yet.
type Allowlist struct {
	path string

	mu      sync.RWMutex
	origins map[string]struct{}
	modTime time.Time
}

// NewAllowlist loads path (or starts empty if it doesn't exist yet).
func NewAllowlist(path string) (*Allowlist, error) {
	a := &Allowlist{path: path, origins: map[string]struct{}{}}
	if err := a.load(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Allowlist) load() error {
	data, err := os.ReadFile(a.path)
	if errors.Is(err, os.ErrNotExist) {
		a.mu.Lock()
		a.origins = map[string]struct{}{}
		a.mu.Unlock()
		return nil
	}
	if err != nil {
		return fmt.Errorf("read allowlist %s: %w", a.path, err)
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("parse allowlist %s (want a flat JSON array of origin strings): %w", a.path, err)
	}
	set := make(map[string]struct{}, len(list))
	for _, o := range list {
		set[o] = struct{}{}
	}

	var mtime time.Time
	if fi, err := os.Stat(a.path); err == nil {
		mtime = fi.ModTime()
	}

	a.mu.Lock()
	a.origins = set
	a.modTime = mtime
	a.mu.Unlock()
	return nil
}

// reloadIfChanged re-reads the file when its mtime has advanced, so the
// operator can extend the allowlist without restarting the daemon (ADR
// 005). A file that has been deleted or is unreadable keeps the
// last-known-good set rather than falling back to empty.
func (a *Allowlist) reloadIfChanged() {
	fi, err := os.Stat(a.path)
	if err != nil {
		return
	}
	a.mu.RLock()
	changed := fi.ModTime().After(a.modTime)
	a.mu.RUnlock()
	if changed {
		_ = a.load()
	}
}

// Check returns nil if origin is allowlisted, or an error naming the
// blocked origin and the file to edit to permit it (ADR 005: "a normal MCP
// tool error... naming the blocked origin and the exact line to add").
func (a *Allowlist) Check(origin string) error {
	a.reloadIfChanged()
	a.mu.RLock()
	defer a.mu.RUnlock()
	if _, ok := a.origins[origin]; ok {
		return nil
	}
	return fmt.Errorf("origin %q is not allowlisted; add %q to the array in %s to permit this call", origin, origin, a.path)
}
