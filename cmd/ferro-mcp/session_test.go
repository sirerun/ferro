package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// searchPlan is a canned plan for the fixtureServer's shop page (same shape
// as main_test.go's inline literal). Never actually invoked in this file's
// test (it never calls run_task), just needed so ConfigFromEnv's required
// LLM env vars have somewhere real to point.
const searchPlan = `{"steps":[
	{"kind":"fill","ref":2,"text":"coffee"},
	{"kind":"click","ref":3},
	{"kind":"wait","for":"dom_settle"},
	{"kind":"done","result":"searched"}
]}`

// processAlive reports whether pid still exists, using signal 0 (no-op
// delivery; ESRCH means the process is gone).
func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// waitForFile polls for path to exist, up to timeout.
func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not appear within %s", path, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestOwnerSurvivesOwnStdioDisconnect is T11.6's acceptance test: the
// owner's own stdio client disconnecting must not close the shared tab or
// the browser pool -- the owner keeps serving other connected shims. It
// also confirms $FERRO_MCP_CHROME_USER_DATA_DIR (default
// $FERRO_MCP_HOME/chrome-profile) stays the one profile actually used
// across both processes and across the disconnect.
//
// The owner's own stdio is driven directly via os/exec pipes rather than
// through mcp.Client -- the SDK's Client.Close() deliberately escalates to
// SIGTERM/SIGKILL if the process doesn't exit soon after stdin closes (by
// design, for a normal client shutting down a server it owns), which would
// mask exactly the behavior this test needs to observe: does the owner
// process, left alone, choose to keep running past its own stdio EOF.
func TestOwnerSurvivesOwnStdioDisconnect(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("set FERRO_TEST_BROWSER=1 to run browser tests (requires Chrome)")
	}

	bin := buildFerroMCP(t)
	fixture := fixtureServer(t)
	llm := fakeLLMServer(t, searchPlan)

	home := shortTempDir(t)
	if err := os.WriteFile(filepath.Join(home, "allowlist.json"), []byte(`["`+fixture.URL+`"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"FERRO_MCP_LLM_BASE_URL="+llm.URL,
		"FERRO_MCP_LLM_MODEL=fake",
		"FERRO_MCP_HOME="+home,
		"FERRO_MCP_HEADLESS=true",
	)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Process O: started first (and given time to win leader election
	// below), so it becomes the owner. Its stdin is held directly so this
	// test can close it without triggering the SDK client's kill escalation.
	cmdOwner := exec.Command(bin)
	cmdOwner.Env = env
	cmdOwner.Stderr = os.Stderr
	cmdOwner.Stdout = io.Discard
	ownerStdin, err := cmdOwner.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmdOwner.Start(); err != nil {
		t.Fatalf("start owner: %v", err)
	}
	t.Cleanup(func() {
		if cmdOwner.Process != nil {
			_ = cmdOwner.Process.Kill()
		}
	})
	ownerPID := cmdOwner.Process.Pid

	// The owner's socket appears only once it has won the flock and called
	// ServeSocketBackground -- its existence is proof the owner is up.
	waitForFile(t, filepath.Join(home, "mcp.sock"), 15*time.Second)

	// Process S: starts once O is confirmed up, so it finds the lock held
	// and becomes a shim relaying to O.
	cmdShim := exec.Command(bin)
	cmdShim.Env = env
	cmdShim.Stderr = os.Stderr
	t.Cleanup(func() {
		if cmdShim.Process != nil {
			_ = cmdShim.Process.Kill()
		}
	})
	clientShim := mcp.NewClient(&mcp.Implementation{Name: "shim-client", Version: "0.0.0"}, nil)
	csShim, err := clientShim.Connect(ctx, &mcp.CommandTransport{Command: cmdShim, TerminateDuration: 200 * time.Millisecond}, nil)
	if err != nil {
		t.Fatalf("connect shim: %v", err)
	}
	defer func() { _ = csShim.Close() }()

	callSnapshot := func() {
		t.Helper()
		res, err := csShim.CallTool(ctx, &mcp.CallToolParams{Name: "navigate", Arguments: map[string]any{"url": fixture.URL}})
		if err != nil || res.IsError {
			t.Fatalf("navigate via shim: err=%v res=%+v", err, res)
		}
		res, err = csShim.CallTool(ctx, &mcp.CallToolParams{Name: "snapshot", Arguments: map[string]any{}})
		if err != nil {
			t.Fatalf("snapshot via shim: %v", err)
		}
		if res.IsError {
			t.Fatalf("snapshot via shim returned an error: %+v", res.Content)
		}
	}

	// Baseline: the shim can drive the shared tab while the owner's own
	// stdio is still open.
	callSnapshot()

	// Disconnect the owner's own stdio (EOF), without ever sending it a
	// signal. Per T11.6, this must close only that connection -- the owner
	// process, the browser pool, and the shared tab must all survive.
	if err := ownerStdin.Close(); err != nil {
		t.Fatalf("close owner stdin: %v", err)
	}

	// Give the owner's server.Run a moment to observe the EOF and return
	// from Run before asserting it didn't then tear anything down.
	time.Sleep(500 * time.Millisecond)

	if !processAlive(ownerPID) {
		t.Fatal("owner process exited after its own stdio disconnected -- it must keep running to serve remaining shims")
	}

	// The shim must still be able to drive the same shared tab afterward.
	callSnapshot()

	if !processAlive(ownerPID) {
		t.Fatal("owner process died during/after the shim's post-disconnect call")
	}

	// FERRO_MCP_CHROME_USER_DATA_DIR (default $FERRO_MCP_HOME/chrome-profile)
	// must be the one profile directory actually used -- confirm it exists
	// and is non-empty, i.e. Chrome actually launched against it.
	profile := filepath.Join(home, "chrome-profile")
	entries, err := os.ReadDir(profile)
	if err != nil {
		t.Fatalf("read chrome-profile dir: %v", err)
	}
	if len(entries) == 0 {
		t.Errorf("%s is empty; Chrome does not appear to have launched against the expected profile", profile)
	}
}
