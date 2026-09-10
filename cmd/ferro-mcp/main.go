// Command ferro-mcp is a Go MCP server that lets an MCP client (Claude Code
// or any other MCP-speaking agent) drive ferro's browser-automation engine
// against a real, persistent Chrome session — not a copied or ephemeral
// one. It exposes tools (run_task, and the primitive snapshot/navigate/
// click/fill/select/key/scroll/wait/extract) over a single Chrome tab that
// persists across calls, so a multi-turn session (e.g. a chat thread) keeps
// its state.
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
// directory, under $FERRO_MCP_HOME.
//
// Any number of ferro-mcp processes can point at the same $FERRO_MCP_HOME
// (e.g. one per Claude Code session) without hitting Chrome's per-profile
// launch lock: internal/mcp elects exactly one of them the owner (it alone
// opens Chrome); the rest relay tool calls to it over a Unix socket. See
// docs/adr/004-mcp-server-shared-browser-daemon.md.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	fmcp "github.com/dndungu/ferro/internal/mcp"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run leader-elects (internal/mcp's ADR 004 implementation) and serves the
// MCP tool set on this process's own stdio.
func run() error {
	cfg, err := fmcp.ConfigFromEnv()
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	leader, err := fmcp.NewLeader(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = leader.Close() }()

	server := fmcp.NewServer(leader)
	_ = server.Run(ctx, &sdk.StdioTransport{})

	if o := leader.Owner(); o != nil {
		// T11.6 / ADR 004: the owner must keep serving connected shims even
		// after its own stdio client disconnects — a plain stdio EOF must
		// never close the shared tab or the browser pool. Block here until
		// an explicit stop (ferro-mcp stop) or this process's own signal
		// asks for shutdown; only then does the deferred leader.Close() run.
		select {
		case <-o.StopRequested():
		case <-ctx.Done():
		}
	}
	return nil
}
