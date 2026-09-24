package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSidepanelBrowserIntegration(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("set FERRO_TEST_BROWSER=1 for real extension UI verification")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<!doctype html><html><body><h1>Fixture</h1><input name="q" aria-label="Query"><button aria-label="Apply" onclick="document.querySelector('#result').textContent=document.querySelector('input').value">Apply</button><div id="result">empty</div></body></html>`)
	}))
	defer fixture.Close()
	var calls atomic.Int32
	var waiting atomic.Bool
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/waiting" {
			json.NewEncoder(w).Encode(waiting.Load())
			return
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "WAIT_FOR_CANCEL") {
			waiting.Store(true)
			<-r.Context().Done()
			waiting.Store(false)
			return
		}
		calls.Add(1)
		if strings.Contains(string(body), "PROVIDER_ERROR") {
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "bad key fixture-key"}})
			return
		}
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Error("model did not receive configured key")
		}
		plan := `{"steps":[{"kind":"fill","ref":2,"text":"from sidepanel"},{"kind":"click","ref":3},{"kind":"extract","fields":{"text":"#result"}},{"kind":"done","result":"{{extract.last.text}}"}]}`
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": plan}}}})
	}))
	defer llm.Close()
	o := extensionOwner(t)
	helper := exec.CommandContext(ctx, "node", filepath.Join("..", "..", "extension", "testsupport", "chat-browser.cjs"))
	helper.Cancel = func() error { return helper.Process.Signal(os.Interrupt) }
	helper.WaitDelay = 5 * time.Second
	input, err := json.Marshal(map[string]string{"url": fixture.URL, "base": "http://" + o.bridge.Addr(), "token": o.bridge.Token(), "modelURL": llm.URL, "screenshot": os.Getenv("FERRO_TEST_SCREENSHOT")})
	if err != nil {
		t.Fatal(err)
	}
	helper.Stdin = strings.NewReader(string(input) + "\n")
	out, err := helper.CombinedOutput()
	if err != nil {
		t.Fatalf("sidepanel integration: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "chat verified") {
		t.Fatalf("missing completion: %s", out)
	}
	if calls.Load() < 2 {
		t.Fatal("task did not reach configured model")
	}
}
