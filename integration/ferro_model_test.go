//go:build integration

// Package integration holds the real-model smoke suite: it exercises
// ferro's public API against a live LLM endpoint instead of the fakeLLM
// used by the root package's tests. It never runs as part of `go build
// ./...` or `go test ./...` — only `go test -tags=integration ./integration/...`.
//
// This is a skeleton (kazi work plan E5): a fixture suite measuring plan
// validity rate against a real small model is still TODO. What's here
// wires the plumbing (env-gated skip, a real browser + real LLM client)
// so that suite has somewhere to land.
package integration

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/sirerun/ferro"
)

// TestRealModel_SmokeGoto is a minimal real-model smoke test: point a real
// OpenAI-compatible endpoint at a static page and confirm it can produce a
// valid plan that reaches "done". Skips cleanly unless both env vars are
// set, per E5-T1's contract.
func TestRealModel_SmokeGoto(t *testing.T) {
	baseURL := os.Getenv("FERRO_MODEL_URL")
	model := os.Getenv("FERRO_MODEL_NAME")
	if baseURL == "" || model == "" {
		t.Skip("set FERRO_MODEL_URL and FERRO_MODEL_NAME to run the real-model suite")
	}

	srv := httptest.NewServer(nil)
	t.Cleanup(srv.Close)

	client := &ferro.OpenAICompatible{BaseURL: baseURL, Model: model}

	b, err := ferro.NewBrowser(ferro.BrowserConfig{Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// TODO(E5-T2/E5-T3): replace this with the full fixture suite (15-25
	// tasks spanning the action vocabulary) and record plan validity rate,
	// per-task LLM calls, and failure taxonomy per the kazi work plan.
	_, metrics, err := ferro.Run(ctx, b, client, ferro.Task{
		Goal:     "Say done immediately; no action is required on this blank page.",
		StartURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("real-model run: %v", err)
	}
	if metrics.LLMCalls == 0 {
		t.Error("expected at least one LLM call")
	}
}
