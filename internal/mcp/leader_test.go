package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestLeaderElection_TwoProcessesRaceToOwn is T11.2's acceptance test:
// starting two ferro-mcp "processes" (Leader instances, standing in for two
// separate OS processes as far as flock/socket semantics are concerned —
// they still race for a real flock and talk over a real Unix socket)
// against the same FERRO_MCP_HOME results in exactly one Chrome process
// under the owner's profile, and both answer a run_task call with the same
// result.
func TestLeaderElection_TwoProcessesRaceToOwn(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("set FERRO_TEST_BROWSER=1 to run browser tests (requires Chrome)")
	}

	home := shortTempDir(t)
	fixture := fixtureServer(t)
	llm := fakeLLMServer(t, searchPlan)

	// T11.5: run_task is gated on Task.StartURL (ADR 005) -- allowlist the
	// fixture's origin so this test exercises leader election, not the
	// allowlist deny path.
	if err := os.WriteFile(filepath.Join(home, "allowlist.json"),
		[]byte(`["`+fixture.URL+`"]`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Home:       home,
		LLMBaseURL: llm.URL,
		LLMModel:   "fake",
		Headless:   true,
		StartURL:   fixture.URL,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Race two leaders to flock the same lock file. Exactly one must win;
	// NewOwner (which launches Chrome) is only ever called by the winner,
	// so this structurally guarantees exactly one Chrome process, not just
	// asserts it after the fact.
	var wg sync.WaitGroup
	leaders := make([]*Leader, 2)
	errs := make([]error, 2)
	wg.Add(2)
	for i := range leaders {
		i := i
		go func() {
			defer wg.Done()
			leaders[i], errs[i] = NewLeader(ctx, cfg)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("leader %d: NewLeader: %v", i, err)
		}
	}
	defer func() {
		for _, l := range leaders {
			_ = l.Close()
		}
	}()

	owners := 0
	for _, l := range leaders {
		if l.IsOwner() {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("owners = %d, want exactly 1", owners)
	}

	args, err := json.Marshal(RunTaskArgs{Goal: "search for coffee", StartURL: fixture.URL})
	if err != nil {
		t.Fatal(err)
	}

	var results [2]any
	for i, l := range leaders {
		text, isError, err := l.Call(ctx, "run_task", args)
		if err != nil {
			t.Fatalf("leader %d: Call: %v", i, err)
		}
		if isError {
			t.Fatalf("leader %d: run_task returned an error result: %s", i, text)
		}
		var out RunTaskOutput
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("leader %d: decode result: %v\n%s", i, err, text)
		}
		if out.Result == nil {
			t.Errorf("leader %d: result.result is nil", i)
		}
		results[i] = out.Result
	}
	// Both calls drove the same shared tab through the same canned plan
	// (each with its own StartURL, so each re-navigates fresh) -- the
	// task-level result must match even though per-call metrics (duration)
	// won't.
	if results[0] != results[1] {
		t.Errorf("results differ across owner/shim: %v vs %v", results[0], results[1])
	}
}

// TestAcquireLock_ExclusiveAndReleasable is a fast, Chrome-free unit test of
// the flock primitive leader election is built on: two callers racing for
// the same lock file, only one wins; after the winner releases, the lock is
// acquirable again.
func TestAcquireLock_ExclusiveAndReleasable(t *testing.T) {
	dir := shortTempDir(t)
	path := dir + "/mcp.lock"

	f1, ok1, err := acquireLock(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if !ok1 {
		t.Fatal("first acquire did not win an uncontested lock")
	}

	_, ok2, err := acquireLock(path)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if ok2 {
		t.Fatal("second acquire won a lock already held by the first")
	}

	if err := releaseLock(f1); err != nil {
		t.Fatalf("release: %v", err)
	}

	f3, ok3, err := acquireLock(path)
	if err != nil {
		t.Fatalf("third acquire: %v", err)
	}
	if !ok3 {
		t.Fatal("third acquire did not win the lock after release")
	}
	_ = releaseLock(f3)
}
