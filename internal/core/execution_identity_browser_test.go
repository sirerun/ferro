package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/sirerun/ferro/page"
)

func TestExecutionIdentityFieldsFromAdapter(t *testing.T) {
	if os.Getenv("FERRO_TEST_BROWSER") == "" {
		t.Skip("set FERRO_TEST_BROWSER=1 to run browser tests")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<!doctype html><form action="/submit?q=1" method="post"><input type="email" autocomplete="email" aria-label="Address"></form><a href="/target?q=2">Target</a>`))
	}))
	defer srv.Close()
	script, err := os.ReadFile(filepath.Join("..", "..", "extension", "adapter.js"))
	if err != nil {
		t.Fatal(err)
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Flag("headless", true))
	if executable := os.Getenv("CHROME_PATH"); executable != "" {
		opts = append(opts, chromedp.ExecPath(executable))
	}
	allocator, stop := chromedp.NewExecAllocator(context.Background(), opts...)
	defer stop()
	ctx, cancel := chromedp.NewContext(allocator)
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 20*time.Second)
	defer timeout()
	var raw json.RawMessage
	if err := chromedp.Run(ctx, chromedp.Navigate(srv.URL), chromedp.Evaluate(string(script), nil), chromedp.Evaluate(`FerroAdapter.takeSnapshot(100, true)`, &raw)); err != nil {
		t.Fatal(err)
	}
	var snapshot page.Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	var input, link bool
	for _, element := range snapshot.Elements {
		switch element.Tag {
		case "input":
			input = true
			if element.InputType != "email" || element.Autocomplete != "email" || element.FormAction != srv.URL+"/submit?q=1" || element.FormMethod != "post" {
				t.Fatalf("input identity not preserved: %+v", element)
			}
		case "a":
			link = true
			if element.AbsHREF != srv.URL+"/target?q=2" {
				t.Fatalf("link identity not preserved: %+v", element)
			}
		}
	}
	if !input || !link {
		t.Fatalf("missing controls: %s", raw)
	}
}
