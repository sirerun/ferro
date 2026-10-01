package core

// Selector and plan caches are shared across runs within one Runner. Persistence
// uses a versioned envelope; cache files must have one process owner. No cached
// plan containing a secret fill is retained.

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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
	mu        sync.RWMutex
	data      map[CacheKey]*CacheEntry
	path      string        // empty = memory only
	ttl       time.Duration // default 30 days
	dirty     bool
	plans     map[string]replayEntry
	loadError error
}

func NewResolutionCache(path string) *ResolutionCache {
	return &ResolutionCache{data: map[CacheKey]*CacheEntry{}, path: path, ttl: 30 * 24 * time.Hour, plans: map[string]replayEntry{}}
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
	if e.Created.IsZero() {
		e.Created = time.Now()
	}
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

// replayEntry stores a plan only after a successful execution. Plans containing
// secret fills are never retained. The key includes the goal, schema, URL and
// initial snapshot, so positional refs cannot silently bind to a changed page.
type replayEntry struct {
	Plan     Plan      `json:"plan"`
	LastUsed time.Time `json:"last_used"`
}
type diskResolution struct {
	Key   CacheKey   `json:"key"`
	Entry CacheEntry `json:"entry"`
}
type diskCache struct {
	Version     int                    `json:"version"`
	Resolutions []diskResolution       `json:"resolutions"`
	Plans       map[string]replayEntry `json:"plans"`
}

func replayID(t Task, s *Snapshot) string {
	b, _ := json.Marshal(struct {
		Key, Goal, URL string
		Schema         json.RawMessage
		Snapshot       *Snapshot
	}{t.ReplayKey, t.Goal, t.StartURL, t.Schema, s})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func clonePlan(p Plan) *Plan {
	b, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	var copy Plan
	if json.Unmarshal(b, &copy) != nil {
		return nil
	}
	return &copy
}
func (c *ResolutionCache) getPlan(key string) *Plan {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.plans[key]
	if !ok || time.Since(e.LastUsed) > c.ttl || e.Plan.Validate() != nil {
		return nil
	}
	return clonePlan(e.Plan)
}
func (c *ResolutionCache) putPlan(key string, p *Plan) {
	if key == "" {
		return
	}
	for _, a := range p.Steps {
		if a.Secret || a.Kind == KindPlanAgain {
			return
		}
	}
	cp := clonePlan(*p)
	if cp == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plans[key] = replayEntry{Plan: *cp, LastUsed: time.Now()}
	c.dirty = true
}
func (c *ResolutionCache) deletePlan(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.plans, key)
	c.dirty = true
}

// Flush atomically persists both caches. Runner calls this after every run,
// including failures. One cache object/file should have one process owner.
func (c *ResolutionCache) Flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty || c.path == "" {
		return nil
	}
	disk := diskCache{Version: 1, Plans: map[string]replayEntry{}}
	for k, v := range c.data {
		if time.Since(v.LastUsed) <= c.ttl {
			disk.Resolutions = append(disk.Resolutions, diskResolution{k, *v})
		}
	}
	sort.Slice(disk.Resolutions, func(i, j int) bool { return fmt.Sprint(disk.Resolutions[i].Key) < fmt.Sprint(disk.Resolutions[j].Key) })
	for k, v := range c.plans {
		if time.Since(v.LastUsed) <= c.ttl {
			disk.Plans[k] = v
		}
	}
	b, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}
	if err = os.MkdirAll(filepath.Dir(c.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(c.path), ".ferro-cache-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), c.path); err != nil {
		return err
	}
	c.dirty = false
	return nil
}

// Load starts empty on malformed or obsolete cache data; the warning is exposed
// in RunMetrics.CacheErrors. It never partially installs a corrupt cache.
func (c *ResolutionCache) Load() {
	if c.path == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	b, err := os.ReadFile(c.path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		c.loadError = err
		return
	}
	var disk diskCache
	if err = json.Unmarshal(b, &disk); err != nil {
		c.loadError = err
		return
	}
	if disk.Version != 1 {
		c.loadError = fmt.Errorf("unsupported cache version %d", disk.Version)
		return
	}
	data := map[CacheKey]*CacheEntry{}
	for _, v := range disk.Resolutions {
		entry := v.Entry
		data[v.Key] = &entry
	}
	c.data = data
	c.plans = disk.Plans
	if c.plans == nil {
		c.plans = map[string]replayEntry{}
	}
	c.loadError = nil
}
func (c *ResolutionCache) warning() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.loadError
}
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
