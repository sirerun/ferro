package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFerroMCP_ExtensionBackendIntegration(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("set FERRO_TEST_BROWSER=1; requires recent Chrome with Extensions.loadUnpacked")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/blocked" {
			w.Write([]byte(`<html><body><div role="alert">Verify you are human</div><input type="password"></body></html>`))
			return
		}
		w.Write([]byte(`<!doctype html><html><body><h1>Fixture</h1><input name="q" aria-label="Query"><button aria-label="Apply" onclick="document.querySelector('#result').textContent=document.querySelector('input').value">Apply</button><select name="color"><option value="red">Red</option><option value="blue">Blue</option></select><div id="result">empty</div></body></html>`))
	}))
	defer fixture.Close()
	llm := fakeLLMServer(t, `{"steps":[{"kind":"fill","ref":2,"text":"from model"},{"kind":"click","ref":3},{"kind":"extract","fields":{"text":"#result"}},{"kind":"done","result":"{{extract.last.text}}"}]}`)
	home := shortTempDir(t)
	if err := os.WriteFile(filepath.Join(home, "allowlist.json"), []byte(`["`+fixture.URL+`"]`), 0600); err != nil {
		t.Fatal(err)
	}
	o, err := NewOwner(ctx, Config{Home: home, Backend: "extension", BridgeAddr: "127.0.0.1:0", LLMBaseURL: llm.URL, LLMModel: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	helper := exec.CommandContext(ctx, "node", filepath.Join("..", "..", "extension", "testsupport", "paired-browser.cjs"))
	helper.Stderr = os.Stderr
	helper.Cancel = func() error { return helper.Process.Signal(os.Interrupt) }
	helper.WaitDelay = 5 * time.Second
	stdin, err := helper.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = helper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		if err := helper.Wait(); err != nil {
			t.Errorf("extension helper: %v", err)
		}
	}()
	if err = json.NewEncoder(stdin).Encode(map[string]string{"url": fixture.URL, "base": "http://" + o.bridge.Addr(), "token": o.bridge.Token()}); err != nil {
		t.Fatal(err)
	}
	scan := bufio.NewScanner(stdout)
	if !scan.Scan() || scan.Text() != "paired" {
		t.Fatal("extension did not pair")
	}
	// Real MCP transport and real extension. The model endpoint alone is a fixture.
	srv := httptest.NewServer(remoteHandler(o, "integration-token", ""))
	defer srv.Close()
	client := sdk.NewClient(&sdk.Implementation{Name: "extension-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp", HTTPClient: &http.Client{Transport: authTransport{"integration-token"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, args map[string]any, wantError bool) string {
		t.Helper()
		r, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		text := r.Content[0].(*sdk.TextContent).Text
		if r.IsError != wantError {
			t.Fatalf("%s isError=%v: %s", name, r.IsError, text)
		}
		return text
	}
	call("acquire_tab", map[string]any{}, false)
	call("navigate", map[string]any{"url": fixture.URL}, false)
	snap := call("snapshot", map[string]any{}, false)
	if !strings.Contains(snap, "Query") {
		t.Fatalf("bad snapshot %s", snap)
	}
	call("fill", map[string]any{"ref": 2, "text": "direct"}, false)
	call("key", map[string]any{"text": "!"}, false)
	call("click", map[string]any{"ref": 3}, false)
	call("select", map[string]any{"ref": 4, "value": "blue"}, false)
	call("key", map[string]any{"text": "Tab"}, false)
	call("scroll", map[string]any{"to": "bottom"}, false)
	call("wait", map[string]any{"for": "dom_settle"}, false)
	if text := call("extract", map[string]any{"fields": map[string]string{"text": "#result", "color": "select"}}, false); !strings.Contains(text, "direct!") || !strings.Contains(text, "blue") {
		t.Fatalf("wrong extraction %s", text)
	}
	result := call("run_task", map[string]any{"goal": "fill and submit", "start_url": fixture.URL}, false)
	if !strings.Contains(result, "from model") {
		t.Fatalf("model did not act: %s", result)
	}
	call("navigate", map[string]any{"url": "https://not-allowed.invalid"}, true)
	call("navigate", map[string]any{"url": fixture.URL + "/blocked"}, false)
	if text := call("snapshot", map[string]any{}, true); !strings.Contains(text, "login_required") && !strings.Contains(text, "blocked") {
		t.Fatalf("not blocked: %s", text)
	}
	if text := call("run_task", map[string]any{"goal": "click something"}, true); !strings.Contains(text, "login_required") && !strings.Contains(text, "blocked") {
		t.Fatalf("run_task not blocked: %s", text)
	}
	call("release_tab", map[string]any{}, false)
}
