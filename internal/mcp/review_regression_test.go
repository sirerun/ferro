package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sirerun/ferro/internal/core"
	"github.com/sirerun/ferro/internal/extbridge"
)

func TestGuardedCDPRunnerReusesSelectors(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("requires Chrome")
	}
	fixture := gatingFixtureServer(t)
	llm := fakeLLMServer(t, `{"steps":[{"kind":"click","ref":4},{"kind":"done","result":"ok"}]}`)
	home := shortTempDir(t)
	if err := os.WriteFile(filepath.Join(home, "allowlist.json"), []byte(`["`+fixture.URL+`"]`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	o, err := NewOwner(ctx, Config{Home: home, Headless: true, LLMBaseURL: llm.URL, LLMModel: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	args, _ := json.Marshal(RunTaskArgs{Goal: "click button", StartURL: fixture.URL})
	for i := 0; i < 2; i++ {
		text, bad, err := o.Call(ctx, "run_task", args)
		if err != nil || bad {
			t.Fatalf("run %d: %s %v", i, text, err)
		}
		var out RunTaskOutput
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatal(err)
		}
		if i == 1 && out.Metrics.CacheHits == 0 {
			t.Fatalf("wrapped CDP driver lost selector cache: %+v", out.Metrics)
		}
	}
	// Cache validation is a page read and must still obey policy revocation.
	if err := os.Remove(filepath.Join(home, "allowlist.json")); err != nil {
		t.Fatal(err)
	}
	runCtx, stop := o.actionCtx(ctx)
	defer stop()
	if _, err := o.driver.(core.SelectorValidator).SelectorMatches(runCtx, "input", &core.Element{}); err == nil {
		t.Fatal("cache validation bypassed revoked allowlist")
	}
}

func TestExtensionToolsUseOneAuthorizationLookup(t *testing.T) {
	o := extensionOwner(t)
	if err := os.WriteFile(o.cfg.AllowlistPath(), []byte(`["https://example.test"]`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	request := func(path string, body []byte) (*http.Response, error) {
		method := "POST"
		if path == "/next" {
			method = "GET"
		}
		req, _ := http.NewRequestWithContext(ctx, method, "http://"+o.bridge.Addr()+path, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+o.bridge.Token())
		req.Header.Set(extbridge.TabIDHeader, "42")
		return http.DefaultClient.Do(req)
	}
	res, err := request("/pair", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	ops := make(chan string, 20)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			res, err := request("/next", nil)
			if err != nil {
				return
			}
			var next struct {
				ID     string            `json:"id"`
				Action extbridge.Command `json:"action"`
			}
			err = json.NewDecoder(res.Body).Decode(&next)
			res.Body.Close()
			if err != nil {
				return
			}
			ops <- next.Action.Op
			reply := map[string]any{"id": next.ID}
			switch next.Action.Op {
			case "location":
				reply["result"] = "https://example.test/"
			case "snapshot":
				reply["snapshot"] = &core.Snapshot{URL: "https://example.test/", Elements: []core.Element{{Ref: 1, Tag: "input", Name: "q"}}}
			}
			body, _ := json.Marshal(reply)
			res, err = request("/reply", body)
			if err != nil {
				return
			}
			res.Body.Close()
		}
	}()
	defer func() { cancel(); <-done }()
	call := func(tool, args string) {
		t.Helper()
		text, bad, err := o.Call(ctx, tool, json.RawMessage(args))
		if err != nil || bad {
			t.Fatalf("%s: %s %v", tool, text, err)
		}
	}
	call("acquire_tab", `{}`)
	call("navigate", `{"url":"https://example.test/"}`)
	call("snapshot", `{}`)
	call("fill", `{"ref":1,"text":"hello"}`)
	var got []string
	for len(ops) > 0 {
		got = append(got, <-ops)
	}
	want := []string{"navigate", "location", "snapshot", "location", "fill"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trips: %v, want %v", got, want)
	}
	if err := os.Remove(o.cfg.AllowlistPath()); err != nil {
		t.Fatal(err)
	}
	text, bad, err := o.Call(ctx, "fill", json.RawMessage(`{"ref":1,"text":"blocked"}`))
	if err != nil || !bad {
		t.Fatalf("revoked policy ignored: %s %v", text, err)
	}
	if len(ops) != 1 || <-ops != "location" {
		t.Fatal("revocation should read only the URL, never dispatch fill")
	}
}
