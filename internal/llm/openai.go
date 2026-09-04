package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// OpenAICompatible talks to any /v1/chat/completions endpoint: OpenAI,
// Anthropic-compatible gateways, Ollama, vLLM, LM Studio, OpenRouter.
type OpenAICompatible struct {
	BaseURL string        // e.g. "http://localhost:11434/v1" — no trailing slash
	APIKey  string        // optional; local servers ignore it
	Model   string        // e.g. "qwen2.5:14b"
	Timeout time.Duration // default 120s; planning can be slow on local models

	// Temperature default 0. Planning wants determinism, not creativity.
	Temperature float32

	// MaxTokens bounds the completion length; default 2048. A plan is a
	// short JSON document — leaving this unset lets some gateways (e.g.
	// OpenRouter) default to the model's max output (16k+), which can
	// exceed a metered key's available balance for no benefit.
	MaxTokens int

	client *http.Client
}

func (o *OpenAICompatible) defaults() {
	if o.Timeout <= 0 {
		o.Timeout = 120 * time.Second
	}
	if o.MaxTokens <= 0 {
		o.MaxTokens = 2048
	}
	if o.client == nil {
		o.client = &http.Client{Timeout: o.Timeout}
	}
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float32       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	// Request strict JSON where the endpoint supports it. Servers that
	// don't know the field ignore it; servers that do (OpenAI, vLLM with
	// guided decoding, Ollama with format) return conformant output.
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"` // "json_object"
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Complete implements Client (and, structurally, core.LLMClient).
func (o *OpenAICompatible) Complete(ctx context.Context, system, user string) (string, error) {
	o.defaults()

	req := chatRequest{
		Model: o.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Temperature:    o.Temperature,
		MaxTokens:      o.MaxTokens,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		o.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", fmt.Errorf("llm decode (http %d): %w", resp.StatusCode, err)
	}
	if cr.Error != nil {
		return "", fmt.Errorf("llm error: %s", cr.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm http %d", resp.StatusCode)
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("llm: empty choices")
	}
	return cr.Choices[0].Message.Content, nil
}

// Notes: response_format: json_object is sent unconditionally — servers
// that don't support it ignore the field (Ollama's OpenAI-compat layer
// actually maps it to its own `format: json` mode, a nice free win for
// local models). Temperature is pinned to 0 by default because plan
// generation is a compilation step, not a chat.
