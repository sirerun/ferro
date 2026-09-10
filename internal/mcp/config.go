// Package mcp implements cmd/ferro-mcp's daemon: leader election over a
// single shared browser tab (ADR 004) and a per-origin allowlist gating
// state-changing tool calls (ADR 005). It is a new consumer of the
// top-level ferro package (like examples/shop), not a change to the
// library's own "library, not platform" design — see DESIGN.md.
package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Config bundles every environment-derived setting cmd/ferro-mcp needs:
// where the daemon's shared state lives ($FERRO_MCP_HOME) and how to reach
// Chrome and the LLM.
type Config struct {
	// Home is $FERRO_MCP_HOME: the directory holding the lock file, the
	// relay socket, the allowlist, and (by default) the Chrome profile.
	// Defaults to $HOME/.ferro-mcp.
	Home string

	LLMBaseURL string
	LLMModel   string
	LLMAPIKey  string

	UserDataDir      string
	ProfileDirectory string
	Headless         bool
	StartURL         string

	MaxRepairs  int
	MaxElements int
	CachePath   string
}

// LockPath is the exclusive flock leader election contends for.
func (c Config) LockPath() string { return filepath.Join(c.Home, "mcp.lock") }

// SocketPath is the owner's Unix domain socket that shims relay tool calls
// over.
func (c Config) SocketPath() string { return filepath.Join(c.Home, "mcp.sock") }

// AllowlistPath is the per-origin allowlist (ADR 005).
func (c Config) AllowlistPath() string { return filepath.Join(c.Home, "allowlist.json") }

// ConfigFromEnv reads FERRO_MCP_* environment variables into a Config.
func ConfigFromEnv() (Config, error) {
	cfg := Config{
		Home:             os.Getenv("FERRO_MCP_HOME"),
		LLMBaseURL:       os.Getenv("FERRO_MCP_LLM_BASE_URL"),
		LLMModel:         os.Getenv("FERRO_MCP_LLM_MODEL"),
		LLMAPIKey:        os.Getenv("FERRO_MCP_LLM_API_KEY"),
		UserDataDir:      os.Getenv("FERRO_MCP_CHROME_USER_DATA_DIR"),
		ProfileDirectory: os.Getenv("FERRO_MCP_CHROME_PROFILE_DIRECTORY"),
		StartURL:         os.Getenv("FERRO_MCP_START_URL"),
		MaxRepairs:       2,
	}
	if cfg.LLMBaseURL == "" {
		return cfg, fmt.Errorf("FERRO_MCP_LLM_BASE_URL is required (an OpenAI-compatible /v1 endpoint)")
	}
	if cfg.LLMModel == "" {
		return cfg, fmt.Errorf("FERRO_MCP_LLM_MODEL is required")
	}
	if cfg.Home == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return cfg, fmt.Errorf("resolve home dir: %w", err)
		}
		cfg.Home = filepath.Join(home, ".ferro-mcp")
	}
	if cfg.UserDataDir == "" {
		// A dedicated, non-default directory this server owns outright —
		// never the real Chrome install's default profile location, which
		// Chrome refuses to enable CDP against (see cmd/ferro-mcp's doc
		// comment).
		cfg.UserDataDir = filepath.Join(cfg.Home, "chrome-profile")
	}
	cfg.Headless = envBool("FERRO_MCP_HEADLESS", false)
	cfg.CachePath = os.Getenv("FERRO_MCP_CACHE_PATH")
	if n := os.Getenv("FERRO_MCP_MAX_REPAIRS"); n != "" {
		v, err := strconv.Atoi(n)
		if err != nil {
			return cfg, fmt.Errorf("FERRO_MCP_MAX_REPAIRS: %w", err)
		}
		cfg.MaxRepairs = v
	}
	if n := os.Getenv("FERRO_MCP_MAX_ELEMENTS"); n != "" {
		v, err := strconv.Atoi(n)
		if err != nil {
			return cfg, fmt.Errorf("FERRO_MCP_MAX_ELEMENTS: %w", err)
		}
		cfg.MaxElements = v
	}
	return cfg, nil
}

func envBool(name string, def bool) bool {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
