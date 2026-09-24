package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dndungu/ferro/internal/core"
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

	// UsageCostCurrency explicitly opts into monetary usage decoding. Only USD
	// is supported; empty leaves provider cost unknown.
	UsageCostCurrency string

	// UseJSONSchema opts into response_format=json_schema. Keep false for
	// servers supporting only json_object. Local plan validation always runs.
	UseJSONSchema bool

	// schemaFallback latches once an endpoint 400s on a response_format:
	// json_schema request and the error body mentions response_format —
	// some OpenAI-compatible gateways advertise the field but reject it at
	// request time. CompleteSchema retries that single request with
	// json_object and then remembers the downgrade for the rest of this
	// client's lifetime, so later calls skip json_schema entirely instead
	// of paying the 400 round trip again. Safe for concurrent use.
	schemaFallback atomic.Bool
}

var _ MetadataCompleterV2 = (*OpenAICompatible)(nil)

const metadataBodyLimit = 2 << 20
const metadataErrorBodyLimit = 8 << 10

type metadataResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *metadataUsage `json:"usage"`
}
type metadataUsage struct {
	PromptTokens     *json.Number `json:"prompt_tokens"`
	CompletionTokens *json.Number `json:"completion_tokens"`
	TotalTokens      *json.Number `json:"total_tokens"`
	PromptDetails    *struct {
		CachedTokens     *json.Number `json:"cached_tokens"`
		CacheWriteTokens *json.Number `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails *struct {
		ReasoningTokens *json.Number `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	Cost *json.Number `json:"cost"`
}

// CompleteWithUsage makes one metadata-bearing provider attempt. Unlike the
// legacy schema path, it never retries or follows redirects.
func (o *OpenAICompatible) CompleteWithUsage(ctx context.Context, system, user string) (core.CompletionV2, error) {
	result := core.CompletionV2{Transmission: core.TransmissionNotSentV2}
	if o.UsageCostCurrency != "" && o.UsageCostCurrency != "USD" {
		return result, fmt.Errorf("llm: unsupported usage cost currency")
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	maxTokens := o.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	req := chatRequest{Model: o.Model, Messages: []chatMessage{{Role: "system", Content: system}, {Role: "user", Content: user}}, Temperature: o.Temperature, MaxTokens: maxTokens, ResponseFormat: &responseFormat{Type: "json_object"}}
	body, err := json.Marshal(req)
	if err != nil {
		return result, fmt.Errorf("llm: encode request: %w", err)
	}
	u := strings.TrimRight(o.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return result, fmt.Errorf("llm: construct provider request")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.APIKey)
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	result.Transmission = core.TransmissionSentUnknownV2
	resp, err := client.Do(httpReq)
	if err != nil {
		return result, fmt.Errorf("llm: provider transport failed")
	}
	defer func() { _ = resp.Body.Close() }()
	result.Transmission = core.TransmissionResponseReceivedV2
	result.ProviderRequestID = boundedString(resp.Header.Get("x-request-id"), 256)
	limit := int64(metadataBodyLimit)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		limit = metadataErrorBodyLimit
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if readErr != nil {
		var partial metadataResponse
		if json.Unmarshal(data, &partial) == nil {
			result.Model = boundedString(partial.Model, 256)
			if result.Model == "" {
				result.Model = boundedString(o.Model, 256)
			}
			if partial.ID != "" {
				result.ProviderRequestID = boundedString(partial.ID, 256)
			}
			if partial.Usage != nil {
				_ = decodeMetadataUsage(&result.Usage, partial.Usage, o.UsageCostCurrency == "USD")
			}
		}
		return result, fmt.Errorf("llm: read provider response failed (http %d)", resp.StatusCode)
	}
	if int64(len(data)) > limit {
		return result, fmt.Errorf("llm: provider response exceeds %d bytes", limit)
	}
	var decoded metadataResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return result, fmt.Errorf("llm: decode provider response")
	}
	result.Model = boundedString(decoded.Model, 256)
	if result.Model == "" {
		result.Model = boundedString(o.Model, 256)
	}
	if decoded.ID != "" {
		result.ProviderRequestID = boundedString(decoded.ID, 256)
	}
	if decoded.Usage != nil {
		if err := decodeMetadataUsage(&result.Usage, decoded.Usage, o.UsageCostCurrency == "USD"); err != nil {
			return result, err
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("llm: provider returned http %d", resp.StatusCode)
	}
	if len(decoded.Choices) == 0 {
		return result, fmt.Errorf("llm: provider response has no choices")
	}
	result.Text = decoded.Choices[0].Message.Content
	if decoded.Choices[0].FinishReason != nil {
		result.FinishReason = boundedString(*decoded.Choices[0].FinishReason, 128)
	}
	return result, nil
}

func boundedString(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

func decodeMetadataUsage(dst *core.RequestUsageV2, u *metadataUsage, parseCost bool) error {
	type field struct {
		name string
		src  *json.Number
		dst  **int64
	}
	fields := []field{{"prompt_tokens", u.PromptTokens, &dst.InputTokens}, {"completion_tokens", u.CompletionTokens, &dst.OutputTokens}, {"total_tokens", u.TotalTokens, &dst.TotalTokens}}
	if u.PromptDetails != nil {
		fields = append(fields, field{"cached_tokens", u.PromptDetails.CachedTokens, &dst.CacheReadTokens}, field{"cache_write_tokens", u.PromptDetails.CacheWriteTokens, &dst.CacheWriteTokens})
	}
	if u.CompletionDetails != nil {
		fields = append(fields, field{"reasoning_tokens", u.CompletionDetails.ReasoningTokens, &dst.ReasoningTokens})
	}
	for _, f := range fields {
		if f.src == nil {
			continue
		}
		r, ok := boundedDecimalRat(f.src.String())
		if !ok || !r.IsInt() || r.Sign() < 0 || !r.Num().IsInt64() {
			return fmt.Errorf("llm: invalid provider usage field %s", f.name)
		}
		v := r.Num().Int64()
		copy := v
		*f.dst = &copy
	}
	if !parseCost || u.Cost == nil {
		return nil
	}
	r, ok := boundedDecimalRat(u.Cost.String())
	if !ok || r.Sign() < 0 {
		return fmt.Errorf("llm: invalid provider usage cost")
	}
	micro := new(big.Rat).Mul(r, big.NewRat(1_000_000, 1))
	n := new(big.Int).Quo(micro.Num(), micro.Denom())
	if new(big.Int).Rem(micro.Num(), micro.Denom()).Sign() != 0 {
		n.Add(n, big.NewInt(1))
	}
	if !n.IsInt64() || n.Sign() < 0 {
		return fmt.Errorf("llm: provider usage cost overflows micro-USD")
	}
	v := n.Int64()
	dst.BilledMicroUSD = &v
	return nil
}

// boundedDecimalRat limits exponent expansion while keeping provider decimal
// parsing exact. Values outside this range cannot represent token counters;
// monetary overflow is reported rather than allocating unbounded integers.
func boundedDecimalRat(s string) (*big.Rat, bool) {
	if len(s) > 256 {
		return nil, false
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exponent, err := strconv.Atoi(s[i+1:])
		if err != nil || exponent > 100 || exponent < -100 {
			return nil, false
		}
	}
	return new(big.Rat).SetString(s)
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
	Type       string `json:"type"`
	JSONSchema any    `json:"json_schema,omitempty"`
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
	return o.complete(ctx, system, user, nil)
}

// CompleteSchema requests the plan schema when explicitly enabled. Endpoints
// without this feature retain the existing JSON-object protocol. If this
// client already fell back off json_schema (see schemaFallback), the
// request skips straight to the json_object protocol.
func (o *OpenAICompatible) CompleteSchema(ctx context.Context, system, user string, schema json.RawMessage) (string, error) {
	if !o.UseJSONSchema || o.schemaFallback.Load() {
		return o.Complete(ctx, system, user)
	}
	return o.complete(ctx, system, user, schema)
}

// complete sends one chat-completions request. When schema is set and the
// endpoint responds 400 with a body mentioning response_format — a gateway
// advertising json_schema support it then rejects at request time — it
// retries once with json_object and latches schemaFallback so later
// CompleteSchema calls on this client skip json_schema entirely.
func (o *OpenAICompatible) complete(ctx context.Context, system, user string, schema json.RawMessage) (string, error) {
	body, status, err := o.doRequest(ctx, system, user, schema)
	if err != nil {
		return "", err
	}
	if len(schema) > 0 && status == http.StatusBadRequest && bytes.Contains(body, []byte("response_format")) {
		o.schemaFallback.Store(true)
		body, status, err = o.doRequest(ctx, system, user, nil)
		if err != nil {
			return "", err
		}
	}

	var cr chatResponse
	if err := json.Unmarshal(body, &cr); err != nil {
		return "", fmt.Errorf("llm decode (http %d): %w", status, err)
	}
	if cr.Error != nil {
		return "", fmt.Errorf("llm error: %s", cr.Error.Message)
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("llm http %d", status)
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("llm: empty choices")
	}
	return cr.Choices[0].Message.Content, nil
}

// doRequest performs one HTTP round trip and returns the raw response body
// and status code, leaving interpretation to the caller so complete can
// inspect a 400 body before deciding whether to retry.
func (o *OpenAICompatible) doRequest(ctx context.Context, system, user string, schema json.RawMessage) ([]byte, int, error) {
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	maxTokens := o.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	client := &http.Client{Timeout: timeout}

	req := chatRequest{
		Model: o.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Temperature:    o.Temperature,
		MaxTokens:      maxTokens,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}

	if len(schema) > 0 {
		req.ResponseFormat = &responseFormat{Type: "json_schema", JSONSchema: map[string]any{"name": "ferro_plan", "strict": false, "schema": schema}}
	}
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, 0, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(o.BaseURL, "/")+"/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("llm: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("llm read body (http %d): %w", resp.StatusCode, err)
	}
	return respBody, resp.StatusCode, nil
}

// Notes: response_format: json_object is sent unconditionally — servers
// that don't support it ignore the field (Ollama's OpenAI-compat layer
// actually maps it to its own `format: json` mode, a nice free win for
// local models). Temperature is pinned to 0 by default because plan
// generation is a compilation step, not a chat.
