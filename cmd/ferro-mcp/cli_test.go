package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestFerroMCP_StatusAndStop is T11.7's acceptance test: `ferro-mcp status`
// reports "not running" before any instance starts, reports the owner's PID
// once one is running, and `ferro-mcp stop` makes it report "not running"
// again.
func TestFerroMCP_StatusAndStop(t *testing.T) {
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

	runCLI := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "FERRO_MCP_HOME="+home)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ferro-mcp %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	// Before any instance starts: status/stop must never require the LLM
	// env vars ConfigFromEnv demands (HomeFromEnv is all they need).
	if got := runCLI("status"); got != "not running" {
		t.Fatalf(`status before any instance starts = %q, want "not running"`, got)
	}

	cmdOwner := exec.Command(bin)
	cmdOwner.Env = append(os.Environ(),
		"FERRO_MCP_LLM_BASE_URL="+llm.URL,
		"FERRO_MCP_LLM_MODEL=fake",
		"FERRO_MCP_HOME="+home,
		"FERRO_MCP_HEADLESS=true",
	)
	cmdOwner.Stderr = os.Stderr
	if err := cmdOwner.Start(); err != nil {
		t.Fatalf("start owner: %v", err)
	}
	// cmdOwner.Wait() must be called exactly once (Go's exec package
	// forbids calling it twice), but both the happy path below and the
	// cleanup fallback need to observe when the owner has exited. waitOwner
	// makes that safe: sync.Once ensures exactly one goroutine ever calls
	// Wait(), and every caller -- whichever happens to run it, or one that
	// arrives later -- blocks on the same Once until it's done, then reads
	// the cached result. An earlier version used a single-buffered channel
	// both places read from directly; whichever read second blocked
	// forever once the first had drained it.
	var (
		waitOnce sync.Once
		waitErr  error
	)
	waitOwner := func() error {
		waitOnce.Do(func() { waitErr = cmdOwner.Wait() })
		return waitErr
	}
	// waitOwnerTimeout reports whether the owner exited within d, alongside
	// its exit error if so.
	waitOwnerTimeout := func(d time.Duration) (exited bool, err error) {
		done := make(chan error, 1)
		go func() { done <- waitOwner() }()
		select {
		case err := <-done:
			return true, err
		case <-time.After(d):
			return false, nil
		}
	}
	// Fallback only: the happy path below issues "ferro-mcp stop" and
	// confirms exit itself. If the test fails before that, this still
	// terminates the owner gracefully (SIGTERM, not SIGKILL) so its Chrome
	// subprocess exits with it instead of being orphaned.
	t.Cleanup(func() {
		if exited, _ := waitOwnerTimeout(200 * time.Millisecond); exited {
			return
		}
		_ = cmdOwner.Process.Signal(syscall.SIGTERM)
		if exited, _ := waitOwnerTimeout(5 * time.Second); exited {
			return
		}
		_ = cmdOwner.Process.Kill()
		_, _ = waitOwnerTimeout(5 * time.Second)
	})

	// The socket appears only once NewLeader has won the flock and the
	// owner is fully up (browser launched, ServeSocketBackground called).
	waitForFile(t, filepath.Join(home, "mcp.sock"), 15*time.Second)

	wantPID := "pid=" + strconv.Itoa(cmdOwner.Process.Pid)
	if got := runCLI("status"); !strings.Contains(got, wantPID) {
		t.Fatalf("status while running = %q, want it to contain %q", got, wantPID)
	}

	if got := runCLI("stop"); got != "stopped" {
		t.Fatalf(`stop = %q, want "stopped"`, got)
	}

	exited, err := waitOwnerTimeout(10 * time.Second)
	if !exited {
		t.Fatal("owner process did not exit within 10s of being stopped")
	}
	if err != nil {
		t.Fatalf("owner process exited with error after stop: %v", err)
	}

	if got := runCLI("status"); got != "not running" {
		t.Fatalf(`status after stop = %q, want "not running"`, got)
	}
}
