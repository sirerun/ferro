// Command ferro-mcp is a Go MCP server that lets an MCP client (Claude Code
// or any other MCP-speaking agent) drive ferro's browser-automation engine
// against a real, persistent Chrome session — not a copied or ephemeral
// one. It exposes one tool, run_task, a thin wrapper over ferro.RunOn: the
// model plans once, ferro executes deterministically, and one Chrome tab is
// acquired at startup and reused across every call so a multi-turn session
// (e.g. a chat thread) keeps its state.
//
// Chrome refuses to enable the remote-debugging (CDP) protocol at all when
// --user-data-dir resolves to the OS's actual default profile location —
// this is a deliberate Chrome guardrail against automating a user's real
// logged-in session, and it applies regardless of whether that profile is
// currently open elsewhere. So this server can never attach to a Chrome
// install's real default profile (e.g. "Profile 7" inside
// ~/Library/Application Support/Google/Chrome); it must use a dedicated,
// non-default profile directory instead: a persistent profile the server
// owns outright, signed into by hand once in a headful window (see README
// "Staying signed in"), then reused headless-or-headful on every later
// launch. FERRO_MCP_CHROME_USER_DATA_DIR defaults to exactly such a
// directory. Chrome's launch lock is still per --user-data-dir, so that
// directory must not be open in another running Chrome process at startup
// (see internal/mcp for how multiple ferro-mcp processes share one profile
// without hitting this lock).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dndungu/ferro"
)

type config struct {
	llmBaseURL string
	llmModel   string
	llmAPIKey  string

	userDataDir      string
	profileDirectory string
	headless         bool
	startURL         string

	maxRepairs  int
	maxElements int
	cachePath   string
}

func configFromEnv() (config, error) {
	cfg := config{
		llmBaseURL:       os.Getenv("FERRO_MCP_LLM_BASE_URL"),
		llmModel:         os.Getenv("FERRO_MCP_LLM_MODEL"),
		llmAPIKey:        os.Getenv("FERRO_MCP_LLM_API_KEY"),
		userDataDir:      os.Getenv("FERRO_MCP_CHROME_USER_DATA_DIR"),
		profileDirectory: os.Getenv("FERRO_MCP_CHROME_PROFILE_DIRECTORY"),
		startURL:         os.Getenv("FERRO_MCP_START_URL"),
		maxRepairs:       2,
	}
	if cfg.llmBaseURL == "" {
		return cfg, fmt.Errorf("FERRO_MCP_LLM_BASE_URL is required (an OpenAI-compatible /v1 endpoint)")
	}
	if cfg.llmModel == "" {
		return cfg, fmt.Errorf("FERRO_MCP_LLM_MODEL is required")
	}
	if cfg.userDataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return cfg, fmt.Errorf("resolve home dir: %w", err)
		}
		// A dedicated, non-default directory this server owns outright —
		// never the real Chrome install's default profile location, which
		// Chrome refuses to enable CDP against (see the package doc).
		cfg.userDataDir = filepath.Join(home, ".ferro-mcp", "chrome-profile")
	}
	cfg.headless = envBool("FERRO_MCP_HEADLESS", false)
	cfg.cachePath = os.Getenv("FERRO_MCP_CACHE_PATH")
	if n := os.Getenv("FERRO_MCP_MAX_REPAIRS"); n != "" {
		v, err := strconv.Atoi(n)
		if err != nil {
			return cfg, fmt.Errorf("FERRO_MCP_MAX_REPAIRS: %w", err)
		}
		cfg.maxRepairs = v
	}
	if n := os.Getenv("FERRO_MCP_MAX_ELEMENTS"); n != "" {
		v, err := strconv.Atoi(n)
		if err != nil {
			return cfg, fmt.Errorf("FERRO_MCP_MAX_ELEMENTS: %w", err)
		}
		cfg.maxElements = v
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

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := configFromEnv()
	if err != nil {
		return err
	}

	b, err := ferro.NewBrowser(ferro.BrowserConfig{
		Headless:         cfg.headless,
		UserDataDir:      cfg.userDataDir,
		ProfileDirectory: cfg.profileDirectory,
		MaxElements:      cfg.maxElements,
	})
	if err != nil {
		return fmt.Errorf("start browser pool: %w", err)
	}
	defer func() { _ = b.Close() }()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	tab, err := b.Acquire(ctx)
	if err != nil {
		return fmt.Errorf(
			"acquire tab against Chrome data dir %q: %w — either it's open in another running Chrome process (quit that Chrome, then retry), or it resolves to a real Chrome install's default profile location, which Chrome refuses to enable CDP against no matter what (point FERRO_MCP_CHROME_USER_DATA_DIR at a dedicated, non-default directory instead)",
			cfg.userDataDir, err,
		)
	}
	defer tab.Release()

	if cfg.startURL != "" {
		if err := tab.Navigate(cfg.startURL); err != nil {
			log.Printf("initial navigation to %s failed (continuing; run_task can still navigate explicitly): %v", cfg.startURL, err)
		}
	}

	client := &ferro.OpenAICompatible{BaseURL: cfg.llmBaseURL, Model: cfg.llmModel, APIKey: cfg.llmAPIKey}
	opts := []ferro.Option{ferro.WithMaxRepairs(cfg.maxRepairs)}
	if cfg.cachePath != "" {
		opts = append(opts, ferro.WithResolutionCache(cfg.cachePath))
	}
	runner := ferro.NewRunner(client, opts...)

	server := mcp.NewServer(&mcp.Implementation{Name: "ferro-mcp", Version: "0.1.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name: "run_task",
		Description: "Drive the shared browser tab toward a natural-language goal. " +
			"ferro asks the model once for a declarative plan, then executes it " +
			"deterministically; the model is consulted again only to repair a " +
			"failed step or replan. The tab persists across calls, so a goal can " +
			"continue where the previous one left off (e.g. the next turn of a chat).",
	}, newRunTaskHandler(runner, tab))

	return server.Run(ctx, &mcp.StdioTransport{})
}

type runTaskArgs struct {
	Goal         string `json:"goal" jsonschema:"the task for the browser agent to accomplish"`
	StartURL     string `json:"start_url,omitempty" jsonschema:"optional URL to navigate to before planning; omit to act on the tab's current page"`
	MaxPlannings int    `json:"max_plannings,omitempty" jsonschema:"cap on replans within this call (ferro default 3); raise it for a goal needing several wait-and-recheck cycles, e.g. an AI reply that streams for a while or truncates and needs 'continue' clicked more than a couple of times"`
}

type runTaskOutput struct {
	Result  any              `json:"result"`
	Metrics ferro.RunMetrics `json:"metrics"`
}

func newRunTaskHandler(runner *ferro.Runner, tab ferro.BrowserContext) mcp.ToolHandlerFor[runTaskArgs, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, args runTaskArgs) (*mcp.CallToolResult, any, error) {
		if args.Goal == "" {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "goal is required"}},
			}, nil, nil
		}

		result, metrics, err := runner.RunOn(ctx, tab, ferro.Task{Goal: args.Goal, StartURL: args.StartURL, MaxPlannings: args.MaxPlannings})
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, nil, nil
		}

		out := runTaskOutput{Result: result, Metrics: metrics}
		body, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return nil, nil, fmt.Errorf("marshal result: %w", err)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
		}, out, nil
	}
}
