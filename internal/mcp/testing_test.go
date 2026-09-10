package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// shortTempDir returns a fresh, empty directory suitable for $FERRO_MCP_HOME.
// t.TempDir() nests under Go's per-test temp path (e.g.
// /var/folders/.../T/TestName.../NNN on macOS), which routinely exceeds the
// ~104-byte sun_path limit for Unix domain sockets -- mcp.sock lives
// directly under $FERRO_MCP_HOME (ADR 004), so a long Home breaks the
// listen() call outright. /tmp is short on every platform this runs on.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "fmcp-")
	if err != nil {
		t.Fatalf("create short temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
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

// fixtureServer serves testdata/pages/shop.html plus a /search results page.
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

// searchPlan is a canned plan for the fixtureServer's shop page.
const searchPlan = `{"steps":[
	{"kind":"fill","ref":2,"text":"coffee"},
	{"kind":"click","ref":3},
	{"kind":"wait","for":"dom_settle"},
	{"kind":"done","result":"searched"}
]}`
