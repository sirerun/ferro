// Command shop is a minimal, runnable demonstration of ferro's public API:
// acquire a browser, run a task with an LLM planner, print the result and
// what it cost. See README.md for the quickstart this mirrors.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/sirerun/ferro"
)

func main() {
	baseURL := os.Getenv("FERRO_LLM_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1" // Ollama's OpenAI-compat endpoint
	}
	model := os.Getenv("FERRO_LLM_MODEL")
	if model == "" {
		model = "qwen2.5:14b"
	}
	startURL := os.Getenv("FERRO_START_URL")
	if startURL == "" {
		startURL = "https://example.com"
	}

	client := &ferro.OpenAICompatible{
		BaseURL: baseURL,
		Model:   model,
	}

	b, err := ferro.NewBrowser(ferro.BrowserConfig{Headless: true})
	if err != nil {
		log.Fatalf("browser: %v", err)
	}
	defer func() { _ = b.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	task := ferro.Task{
		Goal:      "Read the page title and summarize what the site is for in one sentence.",
		StartURL:  startURL,
		ReplayKey: "shop-example-summarize",
	}

	// A resolution cache makes repeat runs against the same site cheaper —
	// once selectors are learned, ref resolution skips the LLM entirely.
	opts := []ferro.Option{ferro.WithResolutionCache(".ferro-cache.json")}

	result, metrics, err := ferro.Run(ctx, b, client, task, opts...)
	if err != nil {
		log.Fatalf("run: %v", err)
	}

	fmt.Printf("result: %v\n", result)
	fmt.Printf("metrics: llm_calls=%d plannings=%d repairs=%d cache_hits=%d est_tokens=%d duration=%s\n",
		metrics.LLMCalls, metrics.Plannings, metrics.Repairs, metrics.CacheHits,
		metrics.EstimatedTokens, metrics.Duration)
}
