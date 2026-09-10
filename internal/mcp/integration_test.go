package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// buildFerroMCPBinary compiles cmd/ferro-mcp into a temp dir and returns its
// path. This test drives two real ferro-mcp processes over real stdio and
// the real $FERRO_MCP_HOME/mcp.sock -- not internal/mcp's Owner type called
// directly in-process -- because T11.9 exists specifically to prove the
// socket-relay path in leader.go/owner.go actually drives the shared tab,
// not just that Owner's methods work when called directly (already covered
// by gating_test.go and tools_primitive_test.go).
func buildFerroMCPBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ferro-mcp")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/ferro-mcp")
	cmd.Dir = filepath.Join("..", "..") // internal/mcp -> module root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build ./cmd/ferro-mcp: %v\n%s", err, out)
	}
	return bin
}

// TestFerroMCP_SharedTabIntegration is T11.9's acceptance test. Two real
// ferro-mcp processes -- an owner and a shim -- never share any Go-level
// state; they only talk to their own MCP client over stdio, and to each
// other over the Unix socket ADR 004 describes. Driving
// navigate/snapshot/fill/click/wait/extract and one run_task call entirely
// from the SHIM's stdio must move the OWNER's shared tab: this test
// confirms that both from the shim's own results (it sees the page it
// navigated to) and, independently, by taking a snapshot on the OWNER's
// stdio afterward and checking it sees the same page the shim navigated
// to -- proof this is one shared tab, not two independent browsers.
func TestFerroMCP_SharedTabIntegration(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("set FERRO_TEST_BROWSER=1 to run browser tests (requires Chrome)")
	}

	bin := buildFerroMCPBinary(t)
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

	// Owner: started first so it wins the flock. Its own client connection
	// is used only to read its state back at the end -- every action is
	// driven from the shim below.
	cmdOwner := exec.Command(bin)
	cmdOwner.Env = env
	cmdOwner.Stderr = os.Stderr
	clientOwner := sdk.NewClient(&sdk.Implementation{Name: "owner-client", Version: "0.0.0"}, nil)
	csOwner, err := clientOwner.Connect(ctx, &sdk.CommandTransport{Command: cmdOwner, TerminateDuration: 200 * time.Millisecond}, nil)
	if err != nil {
		t.Fatalf("connect owner: %v", err)
	}
	defer func() { _ = csOwner.Close() }()

	// Shim: starts once the owner already holds the flock, so it finds the
	// lock held and relays instead of becoming the owner itself.
	cmdShim := exec.Command(bin)
	cmdShim.Env = env
	cmdShim.Stderr = os.Stderr
	clientShim := sdk.NewClient(&sdk.Implementation{Name: "shim-client", Version: "0.0.0"}, nil)
	csShim, err := clientShim.Connect(ctx, &sdk.CommandTransport{Command: cmdShim, TerminateDuration: 200 * time.Millisecond}, nil)
	if err != nil {
		t.Fatalf("connect shim: %v", err)
	}
	defer func() { _ = csShim.Close() }()

	call := func(cs *sdk.ClientSession, name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			t.Fatalf("%s returned an error result: %+v", name, res.Content)
		}
		text, ok := res.Content[0].(*sdk.TextContent)
		if !ok {
			t.Fatalf("%s: result content is not text: %+v", name, res.Content)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
			t.Fatalf("%s: decode result: %v\n%s", name, err, text.Text)
		}
		return out
	}

	// Drive navigate + snapshot + click + extract (plus fill/wait to
	// actually reach a results page) entirely from the shim's stdio.
	call(csShim, "navigate", map[string]any{"url": fixture.URL})
	snap := call(csShim, "snapshot", map[string]any{})
	elements, _ := snap["elements"].([]any)
	var searchRef, goRef float64
	for _, raw := range elements {
		el, _ := raw.(map[string]any)
		switch el["tag"] {
		case "input":
			searchRef, _ = el["ref"].(float64)
		case "button":
			goRef, _ = el["ref"].(float64)
		}
	}
	if searchRef == 0 || goRef == 0 {
		t.Fatalf("shim's snapshot is missing the input/button refs: %+v", snap)
	}

	call(csShim, "fill", map[string]any{"ref": searchRef, "text": "coffee"})
	call(csShim, "click", map[string]any{"ref": goRef})
	call(csShim, "wait", map[string]any{"for": "dom_settle"})
	extracted := call(csShim, "extract", map[string]any{"fields": map[string]any{"heading": "h1"}})
	if got, _ := extracted["heading"].(string); got != "Results for coffee" {
		t.Fatalf("extract via shim = %q, want %q", got, "Results for coffee")
	}

	// Prove it's the SAME tab: the owner's own stdio, which never issued a
	// single navigate/click of its own, must see the results page the shim
	// navigated to.
	ownerSnap := call(csOwner, "snapshot", map[string]any{})
	if url, _ := ownerSnap["url"].(string); !strings.Contains(url, "/search") {
		t.Fatalf("owner's own snapshot url = %q, want it to reflect the shim's navigation (contain /search) -- "+
			"if it doesn't, the shim drove a different tab than the owner's, not the shared one ADR 004 requires", url)
	}

	// One run_task call, also from the shim's stdio, gated by the same
	// allowlist and relayed the same way.
	runTaskOut := call(csShim, "run_task", map[string]any{
		"goal":      "search for coffee",
		"start_url": fixture.URL,
	})
	if runTaskOut["result"] == nil {
		t.Error("run_task via shim: result is nil")
	}
}
