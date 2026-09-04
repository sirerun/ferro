package core

// File 9 (M4): the resolution cache.
//
// The core idea: when the executor resolves a ref to a selector that works,
// remember it. Next time the same (site, element signature) appears, skip
// any LLM involvement entirely and go straight to the cached selector —
// with cheap re-validation, and invalidation driven by the error taxonomy
// from repair.go.
//
// This is what turns ferro from "one LLM call per run" into "one LLM call
// ever, per site pattern" — which is where the real token savings live for
// repeated workflows.
//
// Design:
//   - Key: (host, kind, signature) where signature is the element's
//     stable-ish identity — tag + accessible name/text + a structural hint.
//     Not the ref (positional, unstable — flagged as a footgun in the RFC).
//   - Value: the selector that worked last time, plus metadata for trust.
//   - Hit path: cache lookup → selector still matches exactly one visible
//     element → execute. Zero LLM, zero replan.
//   - Miss/invalid path: fall through to snapshot resolution (as in the
//     base v0.1 executor); on success, store. On ErrStaleRef, delete the
//     entry — the repair taxonomy is the invalidation signal.
//   - Trust decay: a counter of consecutive successes. High-trust entries
//     could relax re-validation strictness (not implemented in v0.1) —
//     guards against a selector that "works" by matching the wrong element
//     after a site redesign.

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CacheKey identifies a resolution independently of positional refs.
type CacheKey struct {
	Host      string `json:"host"`      // e.g. "shop.example.com"
	Kind      string `json:"kind"`      // action kind: "click", "fill", ...
	Signature string `json:"signature"` // element signature hash
}

// Signature is the stable identity of a target element. Built from the
// snapshot Element, deliberately excluding positional data.
func (e Element) Signature() string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s|%s|%s|%s", e.Tag, e.Role, e.Name, normalizeText(e.Text))
	return fmt.Sprintf("%x", h.Sum(nil))[:16] // short: keys are logged
}

// normalizeText lowercases and collapses whitespace so cosmetic text changes
// (capitalization, double spaces) don't invalidate the cache.
func normalizeText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// CacheEntry is one remembered resolution.
type CacheEntry struct {
	Selector  string    `json:"selector"`
	Created   time.Time `json:"created"`
	LastUsed  time.Time `json:"last_used"`
	Successes int       `json:"successes"` // consecutive, reset on failure
}

// ResolutionCache maps (host, kind, signature) → working selector.
// Safe for concurrent use; persists to a JSON file on change.
type ResolutionCache struct {
	mu    sync.RWMutex
	data  map[CacheKey]*CacheEntry
	path  string        // empty = memory only
	ttl   time.Duration // default 30 days
	dirty bool
}

func NewResolutionCache(path string) *ResolutionCache {
	return &ResolutionCache{data: map[CacheKey]*CacheEntry{}, path: path, ttl: 30 * 24 * time.Hour}
}

// Get returns the cached selector if present and unexpired.
func (c *ResolutionCache) Get(key CacheKey) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.data[key]
	if !ok {
		return "", false
	}
	if time.Since(e.LastUsed) > c.ttl {
		return "", false
	}
	return e.Selector, true
}

// Put records a selector that provably worked. Successes track trust.
func (c *ResolutionCache) Put(key CacheKey, selector string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.data[key]
	if !ok {
		e = &CacheEntry{}
		c.data[key] = e
	}
	e.Selector = selector
	e.Created = time.Now()
	e.LastUsed = time.Now()
	e.Successes++
	c.dirty = true
}

// Invalidate drops an entry — called on ErrStaleRef.
func (c *ResolutionCache) Invalidate(key CacheKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
	c.dirty = true
}

// Flush persists if dirty. Callers (the Browser pool) invoke this on
// Release/Close; a crashed run loses at most one session of learning.
func (c *ResolutionCache) Flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty || c.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(c.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.path); err != nil {
		return err
	}
	c.dirty = false
	return nil
}

// Load reads a persisted cache. A corrupt file starts empty — the cache is
// an optimization; a run must never fail because of it.
func (c *ResolutionCache) Load() {
	if c.path == "" {
		return
	}
	b, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = json.Unmarshal(b, &c.data) // best effort
}

// hostOf extracts the host portion of a page URL for use in a CacheKey.
func hostOf(rawURL string) string {
	// Minimal parse to avoid importing net/url just for Hostname(); a real
	// build should just use url.Parse(rawURL).Hostname().
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[:i]
	}
	return s
}

/*
Open issues, flagged rather than silently resolved (see WORKPLAN.md §5):

  - Signature collisions on generic elements. button "Submit" is identical
    across many pages of the same host. Mitigation: fold a coarse structural
    hint into the signature (nearest heading text, or form ancestor's
    action) — cheap in the snapshot script, meaningful disambiguation. Add
    an Element.Context field (nearest h1-h3 ancestor-scoped text) before
    shipping this (kazi E2-T2 / E4-T5).

  - Cache poisoning on false-positive matches. selectorMatches (in
    locator.go) checks count==1 + visible + text similarity, which is
    good but not proof the element is the right one. The Successes counter
    mitigates (a wrong match usually fails at the next step, which
    invalidates), but every cache-hit action should be logged at debug level
    so misfires are diagnosable.

  - Multi-user / multi-profile. The cache file is per-process. If the pool
    ever runs distinct browser profiles (different logins), selectors for
    personalized pages must be namespaced — CacheKey would gain a Profile
    field. Not a v0.1 problem, but the JSON layout is a map so adding a
    top-level dimension later isn't a migration.

Token/latency payoff (informal):

	Scenario                     v0.1 (no cache)        With cache, warm
	First run of a task          1 plan call (~3k tok)  1 plan call
	Repeat run, stable site      1 plan call (~3k tok)  0 LLM calls (see ReplayKey, Task.ReplayKey in runner.go)
	Site redesigned              plan + up to 2 repairs plan + repairs (cache invalidated honestly)

Resolution caching alone doesn't get repeat runs to zero LLM calls — the
runner still calls the planner every Run. That's what Task.ReplayKey (see
runner.go) is for: a stable key lets the runner persist the validated plan
itself and skip the planner entirely on replay, relying on recordOutcome +
the repairer to absorb drift. Worst case for a replay is the same
1+MaxRepairs bound as a fresh run; best case is zero model contact:

	Run(ReplayKey)
	  -> plan cache hit? --yes--> execute with resolution cache --> all hit: 0 tokens
	  |                              \-- miss: snapshot-resolve, learn selector
	  \-- no --> plan (1 call) --> execute --> learn both caches for next time
*/
