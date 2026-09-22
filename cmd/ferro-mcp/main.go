// Command ferro-mcp is a Go MCP server that lets an MCP client (Claude Code
// or any other MCP-speaking agent) drive ferro's browser-automation engine
// against a real, persistent Chrome session — not a copied or ephemeral
// one. It exposes tools (run_task, and the primitive snapshot/navigate/
// click/fill/select/key/scroll/wait/extract) over a single Chrome tab that
// persists across calls, so a multi-turn session (e.g. a chat thread) keeps
// its state.
//
// The default cdp backend uses a dedicated persistent Chrome profile.
// FERRO_MCP_BACKEND=extension instead drives an explicitly paired tab in an
// existing Chrome profile without launching Chrome. Optional authenticated
// remote MCP runs on the local Tailscale interface; see README.
//
// Any number of ferro-mcp processes can point at the same $FERRO_MCP_HOME
// (e.g. one per Claude Code session) without hitting Chrome's per-profile
// launch lock: internal/mcp elects exactly one of them the owner (it alone
// opens Chrome); the rest relay tool calls to it over a Unix socket. See
// docs/adr/004-mcp-server-shared-browser-daemon.md.
//
// Usage:
//
//	ferro-mcp          serve the MCP tool set on stdio (default; auto
//	                    leader-elects per internal/mcp.NewLeader)
//	ferro-mcp serve     run as a service without stdio
//	ferro-mcp status    report the owner's PID and socket path, or "not
//	                    running"
//	ferro-mcp stop      ask the owner to close the browser pool and remove
//	                    the lock/socket files
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	fmcp "github.com/dndungu/ferro/internal/mcp"
)

func main() {
	if err := runCLI(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

// runCLI dispatches ferro-mcp's subcommands using only the stdlib flag
// package (no CLI framework): each subcommand gets its own FlagSet even
// though status/stop currently take no flags of their own, so adding one
// later doesn't require restructuring this dispatch.
func runCLI(args []string) error {
	if len(args) == 0 {
		return serve(false)
	}
	switch args[0] {
	case "serve":
		return serve(true)
	case "status":
		fs := flag.NewFlagSet("status", flag.ExitOnError)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return status()
	case "stop":
		fs := flag.NewFlagSet("stop", flag.ExitOnError)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return stop()
	case "-h", "-help", "--help", "help":
		fmt.Println("usage: ferro-mcp [serve|status|stop]")
		fmt.Println("  (no argument)  serve the MCP tool set on stdio")
		fmt.Println("  serve          run as a service without stdio")
		fmt.Println("  status         report the owner's PID and socket path, or \"not running\"")
		fmt.Println("  stop           ask the owner to close the browser pool and remove the lock/socket")
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q (want: status, stop, or no argument to serve)", args[0])
	}
}

func status() error {
	home, err := fmcp.HomeFromEnv()
	if err != nil {
		return err
	}
	fmt.Println(fmcp.Status(fmcp.Config{Home: home}))
	return nil
}

func stop() error {
	home, err := fmcp.HomeFromEnv()
	if err != nil {
		return err
	}
	fmt.Println(fmcp.Stop(fmcp.Config{Home: home}))
	return nil
}

// serve leader-elects (internal/mcp's ADR 004 implementation) and serves
// the MCP tool set on this process's own stdio.
func serve(daemon bool) error {
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

	if !daemon {
		server := fmcp.NewServer(leader)
		finished := make(chan struct{})
		go func() { defer close(finished); _ = server.Run(ctx, &sdk.StdioTransport{}) }()
		if o := leader.Owner(); o != nil {
			select {
			case <-finished:
			case <-o.StopRequested():
				cancel()
				return nil
			case <-ctx.Done():
				return nil
			}
		} else {
			select {
			case <-finished:
			case <-ctx.Done():
			}
			return nil
		}
	} else if !leader.IsOwner() {
		return fmt.Errorf("a Ferro owner is already running; use ferro-mcp status")
	}

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
