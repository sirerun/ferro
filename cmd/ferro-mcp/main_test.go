package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// buildFerroMCP compiles the ferro-mcp binary into a temp dir and returns its
// path. Building the real binary (rather than testing package internals) is
// what T11.1's acceptance criterion asks for: a tools/list and a run_task
// call against the artifact that actually ships.
func buildFerroMCP(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ferro-mcp")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build ferro-mcp: %v\n%s", err, out)
	}
	return bin
}

// fakeLLMServer serves an OpenAI-compatible /v1/chat/completions endpoint
// that always returns plan, so run_task can complete without any real model
// or API key.
func fakeLLMServer(t *testing.T, plan string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": plan}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fixtureServer serves testdata/pages/shop.html plus a /search results page,
// the same shape ferro_test.go's testServer uses, satisfying "against a
// testdata/pages fixture."
func fixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join("..", "..", "testdata", "pages", "shop.html"))
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		_, _ = w.Write([]byte(`<!doctype html><html><body>
			<h1>Results for ` + q + `</h1>
			<a href="/item/1">Coffee 1kg — $15.00</a>
			</body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestFerroMCP_ListToolsAndRunTask exercises the built ferro-mcp binary end
// to end over its own stdio: tools/list must include run_task, and a
// run_task call against a testdata/pages fixture must complete with a
// result plus RunMetrics (T11.1 acceptance).
func TestFerroMCP_ListToolsAndRunTask(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("set FERRO_TEST_BROWSER=1 to run browser tests (requires Chrome)")
	}

	bin := buildFerroMCP(t)
	fixture := fixtureServer(t)
	llm := fakeLLMServer(t, `{"steps":[
		{"kind":"fill","ref":2,"text":"coffee"},
		{"kind":"click","ref":3},
		{"kind":"wait","for":"dom_settle"},
		{"kind":"done","result":"searched"}
	]}`)

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"FERRO_MCP_LLM_BASE_URL="+llm.URL,
		"FERRO_MCP_LLM_MODEL=fake",
		"FERRO_MCP_CHROME_USER_DATA_DIR="+filepath.Join(t.TempDir(), "chrome-profile"),
		"FERRO_MCP_HEADLESS=true",
	)

	client := mcp.NewClient(&mcp.Implementation{Name: "ferro-mcp-test", Version: "0.0.0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = cs.Close() }()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("tools/list returned an empty tools array")
	}
	found := false
	for _, tl := range tools.Tools {
		if tl.Name == "run_task" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tools/list did not include run_task: %+v", tools.Tools)
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "run_task",
		Arguments: map[string]any{
			"goal":      "search for coffee",
			"start_url": fixture.URL,
		},
	})
	if err != nil {
		t.Fatalf("run_task call: %v", err)
	}
	if res.IsError {
		t.Fatalf("run_task returned an error result: %+v", res.Content)
	}
	var out runTaskOutput
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("run_task result content is not text: %+v", res.Content)
	}
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("decode run_task result: %v\n%s", err, text.Text)
	}
	if out.Result == nil {
		t.Error("run_task result.result is nil")
	}
	if out.Metrics.LLMCalls == 0 {
		t.Error("run_task metrics.llm_calls is 0, want at least 1")
	}
}
