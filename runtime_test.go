package ferro

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/chromedp/chromedp"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type sequenceLLM struct {
	mu      sync.Mutex
	replies []string
	calls   int
}

func (s *sequenceLLM) Complete(_ context.Context, _, _ string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls > len(s.replies) {
		return "", fmt.Errorf("unexpected model call %d", s.calls)
	}
	return s.replies[s.calls-1], nil
}

func runtimeBrowser(t *testing.T) (context.Context, *Browser, string) {
	t.Helper()
	if os.Getenv("FERRO_TEST_BROWSER") != "1" {
		t.Skip("requires FERRO_TEST_BROWSER=1 and Chrome")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><body><input name="q" aria-label="query"><p id="price">12</p><p>Stock is 4</p></body></html>`)
	}))
	t.Cleanup(srv.Close)
	b, err := NewBrowser(BrowserConfig{Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	t.Cleanup(cancel)
	return ctx, b, srv.URL
}

func TestRuntimeReplayAcrossRunners(t *testing.T) {
	ctx, b, url := runtimeBrowser(t)
	cache := filepath.Join(t.TempDir(), "cache.json")
	plan := `{"steps":[{"kind":"fill","ref":1,"text":"coffee"},{"kind":"done","result":"ok"}]}`
	first := &sequenceLLM{replies: []string{plan}}
	task := Task{Goal: "fill query with coffee", StartURL: url, ReplayKey: "search"}
	if _, _, err := Run(ctx, b, first, task, WithResolutionCache(cache)); err != nil {
		t.Fatal(err)
	}
	second := &sequenceLLM{}
	got, metrics, err := Run(ctx, b, second, task, WithResolutionCache(cache))
	if err != nil || got != "ok" || metrics.LLMCalls != 0 || metrics.CacheHits != 1 {
		t.Fatalf("replay: result=%v metrics=%+v error=%v", got, metrics, err)
	}
}

func TestRuntimeSchemaExtraction(t *testing.T) {
	ctx, b, url := runtimeBrowser(t)
	client := &sequenceLLM{replies: []string{
		`{"steps":[{"kind":"extract","schema":{"type":"object","properties":{"stock":{"type":"integer"}},"required":["stock"],"additionalProperties":false}},{"kind":"done","result":"{{extract.last.stock}}"}]}`,
		`{"stock":4}`,
	}}
	got, metrics, err := Run(ctx, b, client, Task{Goal: "get stock", StartURL: url})
	if err != nil || fmt.Sprint(got) != "4" || metrics.LLMCalls != 2 {
		t.Fatalf("extraction: result=%v metrics=%+v error=%v", got, metrics, err)
	}
}

func TestRuntimePartialExtraction(t *testing.T) {
	ctx, b, url := runtimeBrowser(t)
	client := &sequenceLLM{replies: []string{`{"steps":[{"kind":"extract","fields":{"price":"#price","bad":"["}},{"kind":"done","result":"{{extract.last.price}}"}]}`}}
	got, metrics, err := Run(ctx, b, client, Task{Goal: "get price", StartURL: url})
	if err != nil || fmt.Sprint(got) != "12" || metrics.ExtractErrors["bad"] == "" {
		t.Fatalf("partial extraction: %v %v", got, err)
	}
}

func TestRuntimeRunOnAndCancellation(t *testing.T) {
	ctx, b, url := runtimeBrowser(t)
	tab, err := b.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Release()
	first := &sequenceLLM{replies: []string{`{"steps":[{"kind":"fill","ref":1,"text":"retained"},{"kind":"done"}]}`}}
	if _, _, err = RunOn(ctx, tab, first, Task{Goal: "fill", StartURL: url}); err != nil {
		t.Fatal(err)
	}
	second := &sequenceLLM{replies: []string{`{"steps":[{"kind":"extract","fields":{"value":"[1]"}},{"kind":"done","result":"{{extract.last.value}}"}]}`}}
	got, _, err := RunOn(ctx, tab, second, Task{Goal: "read current page"})
	if err != nil || got != "retained" {
		t.Fatalf("caller tab lost: %v %v", got, err)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	waiting := &sequenceLLM{replies: []string{`{"steps":[{"kind":"wait","for":"#never-present"},{"kind":"done"}]}`}}
	if _, _, err = RunOn(short, tab, waiting, Task{Goal: "wait"}, WithMaxRepairs(0)); err == nil {
		t.Fatal("wait ignored deadline")
	}
	third := &sequenceLLM{replies: []string{`{"steps":[{"kind":"done","result":"alive"}]}`}}
	if got, _, err = RunOn(ctx, tab, third, Task{Goal: "check tab"}); err != nil || got != "alive" {
		t.Fatalf("run cancellation killed caller tab: %v %v", got, err)
	}
}

func TestRuntimeSchemaRejectionAndRepairLimit(t *testing.T) {
	ctx, b, url := runtimeBrowser(t)
	invalid := &sequenceLLM{replies: []string{`{"steps":[{"kind":"extract","schema":{"type":"object","required":["stock"]}},{"kind":"done","result":"{{extract.last}}"}]}`, `{}`}}
	_, m, err := Run(ctx, b, invalid, Task{Goal: "get stock", StartURL: url})
	if err == nil || m.ErrorClass != "schema" || m.LLMCalls != 2 {
		t.Fatalf("invalid structured result accepted: %+v %v", m, err)
	}
	client := &sequenceLLM{replies: []string{`{"steps":[{"kind":"click","ref":900},{"kind":"done"}]}`}}
	_, m, err = Run(ctx, b, client, Task{Goal: "missing element", StartURL: url}, WithMaxRepairs(0))
	if err == nil || m.Repairs != 0 || m.LLMCalls != 1 {
		t.Fatalf("repair limit ignored: %+v %v", m, err)
	}
}

func TestRuntimeCacheFailureIsReported(t *testing.T) {
	ctx, b, url := runtimeBrowser(t)
	client := &sequenceLLM{replies: []string{`{"steps":[{"kind":"done","result":"ok"}]}`}}
	got, m, err := Run(ctx, b, client, Task{Goal: "done", StartURL: url, ReplayKey: "test"}, WithResolutionCache(t.TempDir()))
	if err != nil || got != "ok" || len(m.CacheErrors) == 0 {
		t.Fatalf("cache error reporting: %v %+v %v", got, m, err)
	}
}

func TestRuntimeConcurrentRunner(t *testing.T) {
	ctx, b, url := runtimeBrowser(t)
	client := &sequenceLLM{replies: []string{`{"steps":[{"kind":"done","result":"ok"}]}`, `{"steps":[{"kind":"done","result":"ok"}]}`}}
	runner := NewRunner(client)
	tabs := make([]BrowserContext, 2)
	for i := range tabs {
		var err error
		tabs[i], err = b.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tabs[i].Release()
	}
	errs := make(chan error, 2)
	for _, tab := range tabs {
		go func(tab BrowserContext) {
			_, _, err := runner.RunOn(ctx, tab, Task{Goal: "done", StartURL: url})
			errs <- err
		}(tab)
	}
	for range tabs {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

func TestRuntimeFailedReplayInvalidates(t *testing.T) {
	ctx, b, url := runtimeBrowser(t)
	tab, err := b.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Release()
	if err = tab.Navigate(url); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(t.TempDir(), "cache.json")
	task := Task{Goal: "read price", ReplayKey: "price"}
	first := &sequenceLLM{replies: []string{`{"steps":[{"kind":"extract","fields":{"price":"#price"}},{"kind":"done","result":"{{extract.last.price}}"}]}`}}
	if _, _, err = RunOn(ctx, tab, first, task, WithResolutionCache(cache)); err != nil {
		t.Fatal(err)
	}
	if err = chromedp.Run(tab.CDP(), chromedp.Evaluate(`document.getElementById('price').remove()`, nil)); err != nil {
		t.Fatal(err)
	}
	_, m, err := RunOn(ctx, tab, &sequenceLLM{}, task, WithResolutionCache(cache), WithMaxRepairs(0))
	if err == nil || m.ReplayHits != 1 || m.LLMCalls != 0 {
		t.Fatalf("failed replay: %+v %v", m, err)
	}
	fallback := &sequenceLLM{replies: []string{`{"steps":[{"kind":"done","result":"price unavailable"}]}`}}
	got, m, err := RunOn(ctx, tab, fallback, task, WithResolutionCache(cache))
	if err != nil || got != "price unavailable" || m.ReplayHits != 0 || m.LLMCalls != 1 {
		t.Fatalf("stale plan retained: %v %+v %v", got, m, err)
	}
}

func TestRuntimeTaskSchema(t *testing.T) {
	ctx, b, url := runtimeBrowser(t)
	for _, tc := range []struct {
		name, reply string
		valid       bool
	}{
		{"valid", `{"steps":[{"kind":"done","result":{"stock":4}}]}`, true},
		{"wrong type", `{"steps":[{"kind":"done","result":{"stock":"four"}}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &sequenceLLM{replies: []string{tc.reply}}
			_, m, err := Run(ctx, b, client, Task{Goal: "stock", StartURL: url, Schema: json.RawMessage(`{"type":"object","properties":{"stock":{"type":"integer"}},"required":["stock"]}`)})
			if (err == nil) != tc.valid || (!tc.valid && m.ErrorClass != "schema") {
				t.Fatalf("valid=%v metrics=%+v err=%v", tc.valid, m, err)
			}
		})
	}
}
