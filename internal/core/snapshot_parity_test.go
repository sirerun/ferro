package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestSnapshotParity_Golden is one half of T12.2's ref-numbering parity
// test (ADR 006 decision #3; docs/plan.md T12.2's acc line). It loads the
// fixture shared with the extension's Node-based test
// (extension/testdata/fixture.html, extension/snapshot.test.cjs)
// into a real headless Chrome tab, runs the production TakeSnapshot
// against it, and asserts the result against a checked-in golden file
// (testdata/fixture.golden.json) that both this test and the Node test
// compare against independently -- this is the shared source of truth the
// two implementations must agree on.
//
// Why a checked-in golden rather than shelling out to Node (or vice
// versa): it keeps each side's test runnable and CI-gateable on its own
// (`go test ./internal/core/...`, `node --test extension/`) without one
// runtime depending on the other being installed, while still pinning
// both to the exact same expected output. The tradeoff, matching
// docs/plan.md's Operating Procedure: any change to snapshot.go's
// selection/numbering rules OR to the fixture must regenerate this golden
// (FERRO_UPDATE_GOLDEN=1) and the regeneration must be re-verified by
// BOTH this test and the Node test before it's trusted.
//
// Chrome, not a chromedp-independent unit test: snapshot.go's algorithm
// depends on getComputedStyle/getBoundingClientRect (display:none,
// visibility:hidden, opacity, zero-size, off-screen filtering) -- there is
// no meaningful way to unit-test that without a real layout engine, and
// existing tests in this package (executor_browser_test.go) already
// establish the pattern of a browser-gated test using chromedp against a
// local page for exactly this reason. This test follows that pattern.
func TestSnapshotParity_Golden(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("set FERRO_TEST_BROWSER=1 to run browser tests (requires Chrome)")
	}

	fixture, err := filepath.Abs(filepath.Join("..", "..", "extension", "testdata", "fixture.html"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fixture); err != nil {
		t.Fatalf("fixture not found at %s: %v", fixture, err)
	}

	// Window size is pinned and MUST match extension/snapshot.test.cjs's
	// Chrome launch flags exactly: snapshot.go's viewport filter
	// (rect.top > innerHeight + 50) is scroll/viewport-size-dependent, so a
	// mismatched window size between the two sides would itself produce a
	// spurious parity failure unrelated to any real algorithm drift.
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
			chromedp.WindowSize(1280, 3000))...)
	defer cancelAlloc()
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()

	tctx, cancelTimeout := context.WithTimeout(ctx, 15*time.Second)
	defer cancelTimeout()
	if err := chromedp.Run(tctx, chromedp.Navigate("file://"+fixture)); err != nil {
		t.Fatal(err)
	}

	// maxElements matches the Node side's takeSnapshot(200) call exactly --
	// see extension/snapshot.test.cjs. Any drift between these two
	// constants would itself be a parity bug, so both are pinned to the
	// same literal value with a comment pointing at the other side.
	snap, err := TakeSnapshot(ctx, 200)
	if err != nil {
		t.Fatal(err)
	}

	got := comparablePart{Title: snap.Title, Elements: snap.Elements, Truncated: snap.Truncated}
	gotJSON, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	goldenPath := filepath.Join("testdata", "fixture.golden.json")
	if os.Getenv("FERRO_UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(goldenPath, append(gotJSON, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated golden file %s", goldenPath)
		return
	}

	wantJSON, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s (regenerate with FERRO_UPDATE_GOLDEN=1): %v", goldenPath, err)
	}

	var gotVal, wantVal any
	if err := json.Unmarshal(gotJSON, &gotVal); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wantJSON, &wantVal); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotVal, wantVal) {
		t.Errorf("Go-side snapshot does not match golden.\ngot:\n%s\nwant:\n%s", gotJSON, wantJSON)
	}
}

// comparablePart is the subset of Snapshot that's meaningful to compare
// across machines/checkouts: URL is excluded because chromedp.Location
// returns an absolute file:// path that differs per checkout (worktree
// path, home directory), which would make the golden file spuriously
// fail on any machine other than the one that generated it.
type comparablePart struct {
	Title     string    `json:"title"`
	Elements  []Element `json:"elements"`
	Truncated bool      `json:"truncated,omitempty"`
}
