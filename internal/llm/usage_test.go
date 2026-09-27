package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sirerun/ferro/internal/core"
)

func TestUsage_Reported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-request-id", "hdr-id")
		fmt.Fprint(w, `{"id":"resp-id","model":"m2","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":2,"cache_write_tokens":1},"completion_tokens_details":{"reasoning_tokens":1},"cost":0.0000001}}`)
	}))
	defer srv.Close()
	got, err := (&OpenAICompatible{BaseURL: srv.URL, Model: "m1", UsageCostCurrency: "USD"}).CompleteWithUsage(context.Background(), "s", "u")
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "ok" || got.Model != "m2" || got.ProviderRequestID != "resp-id" || got.FinishReason != "stop" || got.Transmission != core.TransmissionResponseReceived {
		t.Fatalf("metadata: %+v", got)
	}
	for name, pair := range map[string][2]*int64{"in": {got.Usage.InputTokens, int64Ptr(12)}, "out": {got.Usage.OutputTokens, int64Ptr(3)}, "total": {got.Usage.TotalTokens, int64Ptr(15)}, "reasoning": {got.Usage.ReasoningTokens, int64Ptr(1)}, "cached": {got.Usage.CacheReadTokens, int64Ptr(2)}, "cachewrite": {got.Usage.CacheWriteTokens, int64Ptr(1)}, "cost": {got.Usage.BilledMicroUSD, int64Ptr(1)}} {
		if !sameInt(pair[0], pair[1]) {
			t.Errorf("%s = %v, want %v", name, pair[0], pair[1])
		}
	}
}

func TestUsage_Missing(t *testing.T) {
	for _, tc := range []struct {
		body  string
		input *int64
	}{{`{"choices":[{"message":{"content":"x"}}]}`, nil}, {`{"choices":[{"message":{"content":"x"}}],"usage":{"prompt_tokens":0}}`, int64Ptr(0)}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
		got, err := (&OpenAICompatible{BaseURL: srv.URL}).CompleteWithUsage(context.Background(), "s", "u")
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !sameInt(got.Usage.InputTokens, tc.input) || got.Usage.OutputTokens != nil {
			t.Fatalf("explicit zero/missing distinction lost: %+v", got.Usage)
		}
	}
}

func TestUsage_HTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		fmt.Fprint(w, `{"error":{"message":"secret body"},"usage":{"prompt_tokens":7}}`)
	}))
	defer srv.Close()
	got, err := (&OpenAICompatible{BaseURL: srv.URL, APIKey: "credential"}).CompleteWithUsage(context.Background(), "s", "u")
	if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "credential") {
		t.Fatalf("diagnostic not safely redacted: %v", err)
	}
	if got.Transmission != core.TransmissionResponseReceived || got.Usage.InputTokens == nil || *got.Usage.InputTokens != 7 {
		t.Fatalf("failure metadata lost: %+v", got)
	}
}

func TestUsage_SchemaFormatRejectionSingleAttempt(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"response_format json_object rejected"},"usage":{"prompt_tokens":5}}`)
	}))
	defer srv.Close()
	got, err := (&OpenAICompatible{BaseURL: srv.URL, UseJSONSchema: true}).CompleteWithUsage(context.Background(), "s", "u")
	if err == nil || calls != 1 || got.Usage.InputTokens == nil || *got.Usage.InputTokens != 5 {
		t.Fatalf("got %+v, err %v, calls %d", got, err, calls)
	}
}

func TestUsage_UsageSurvivesChoiceFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"usage":{"prompt_tokens":9}}`)
	}))
	defer srv.Close()
	got, err := (&OpenAICompatible{BaseURL: srv.URL}).CompleteWithUsage(context.Background(), "s", "u")
	if err == nil || got.Usage.InputTokens == nil || *got.Usage.InputTokens != 9 {
		t.Fatalf("usage not retained: %+v, err %v", got, err)
	}
}

func TestUsage_MetadataSurvivesMalformedContent(t *testing.T) {
	for _, tc := range []struct {
		name, message string
	}{
		{"array content", `{"content":[]}`},
		{"object content", `{"content":{"text":"not a string"}}`},
		{"null content", `{"content":null}`},
		{"missing content", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"id":"response-123","model":"reported-model","choices":[{"message":%s}],"usage":{"prompt_tokens":9,"completion_tokens":4,"cost":0.000002}}`, tc.message)
			}))
			defer srv.Close()
			got, err := (&OpenAICompatible{BaseURL: srv.URL, Model: "configured-model", UsageCostCurrency: "USD"}).CompleteWithUsage(context.Background(), "s", "u")
			if err == nil {
				t.Fatal("malformed content must not be accepted as a completion")
			}
			if got.Usage.InputTokens == nil || *got.Usage.InputTokens != 9 || got.Usage.OutputTokens == nil || *got.Usage.OutputTokens != 4 || got.Usage.BilledMicroUSD == nil || *got.Usage.BilledMicroUSD != 2 {
				t.Fatalf("valid usage was discarded with malformed content: %+v", got.Usage)
			}
			if got.Model != "reported-model" || got.ProviderRequestID != "response-123" {
				t.Fatalf("safe response identity was discarded: %+v", got)
			}
		})
	}
}

func TestUsage_TimeoutAfterSend(t *testing.T) {
	received := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(received); <-release }))
	got, err := (&OpenAICompatible{BaseURL: srv.URL, Timeout: 30 * time.Millisecond}).CompleteWithUsage(context.Background(), "s", "u")
	<-received
	close(release)
	srv.Close()
	if err == nil || got.Transmission != core.TransmissionSentUnknown {
		t.Fatalf("got %+v, err %v", got, err)
	}
}

func TestUsage_ResponseLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, strings.Repeat("x", metadataBodyLimit+1)) }))
	defer srv.Close()
	got, err := (&OpenAICompatible{BaseURL: srv.URL}).CompleteWithUsage(context.Background(), "s", "u")
	if err == nil || got.Transmission != core.TransmissionResponseReceived {
		t.Fatalf("got %+v, err %v", got, err)
	}
}

func TestUsage_LegacySingleRequest(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"response_format rejected"}}`)
	}))
	defer srv.Close()
	_, err := (&OpenAICompatible{BaseURL: srv.URL, UseJSONSchema: true}).CompleteSchema(context.Background(), "s", "u", []byte(`{"type":"object"}`))
	if err == nil || calls != 2 {
		t.Fatalf("legacy fallback calls=%d err=%v", calls, err)
	}
}

func TestUsage_RedirectSingleHit(t *testing.T) {
	second := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		second++
		fmt.Fprint(w, `{"choices":[{"message":{"content":"unexpected"}}]}`)
	}))
	defer target.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer first.Close()
	got, err := (&OpenAICompatible{BaseURL: first.URL}).CompleteWithUsage(context.Background(), "s", "u")
	if err == nil || second != 0 || got.Transmission != core.TransmissionResponseReceived {
		t.Fatalf("got %+v, err %v, redirect hits=%d", got, err, second)
	}
}

func TestUsage_BadUsageAndBody(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"negative token", `{"usage":{"prompt_tokens":-1},"choices":[{"message":{"content":"x"}}]}`},
		{"fractional token", `{"usage":{"prompt_tokens":1.5},"choices":[{"message":{"content":"x"}}]}`},
		{"malformed", `{"usage":{"prompt_tokens":4}`},
		{"huge error body", `"` + strings.Repeat("z", metadataErrorBodyLimit+1) + `"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := http.StatusOK
			if tc.name == "huge error body" {
				status = 500
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); fmt.Fprint(w, tc.body) }))
			defer srv.Close()
			got, err := (&OpenAICompatible{BaseURL: srv.URL}).CompleteWithUsage(context.Background(), "s", "u")
			if err == nil || got.Transmission != core.TransmissionResponseReceived {
				t.Fatalf("got %+v err %v", got, err)
			}
		})
	}
}

func TestUsage_CostRoundingAndOverflow(t *testing.T) {
	cases := []struct {
		cost string
		want int64
		ok   bool
	}{{"0.000001", 1, true}, {"0.0000001", 1, true}, {"1.0000001", 1000001, true}, {"-1", 0, false}, {"9223372036854.775808", 0, false}}
	for _, tc := range cases {
		t.Run(tc.cost, func(t *testing.T) {
			usage := core.RequestUsage{}
			err := decodeMetadataUsage(&usage, &metadataUsage{Cost: []byte(tc.cost)}, true)
			if (err == nil) != tc.ok {
				t.Fatalf("err=%v", err)
			}
			if tc.ok && (usage.BilledMicroUSD == nil || *usage.BilledMicroUSD != tc.want) {
				t.Fatalf("cost=%v want %d", usage.BilledMicroUSD, tc.want)
			}
		})
	}
}

func TestUsage_CostOptOutIgnoresCostShape(t *testing.T) {
	for _, cost := range []string{`{"amount":0.02,"currency":"credits"}`, `"not-a-decimal"`} {
		t.Run(cost, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":7,"completion_tokens":2,"cost":%s}}`, cost)
			}))
			defer srv.Close()
			got, err := (&OpenAICompatible{BaseURL: srv.URL}).CompleteWithUsage(context.Background(), "s", "u")
			if err != nil || got.Text != "ok" || got.Usage.InputTokens == nil || *got.Usage.InputTokens != 7 || got.Usage.OutputTokens == nil || *got.Usage.OutputTokens != 2 || got.Usage.BilledMicroUSD != nil {
				t.Fatalf("cost opt-out did not preserve token completion: %+v err=%v", got, err)
			}
		})
	}
}

func TestUsage_InvalidOptInCostRetainsTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"resp","model":"m","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":7,"completion_tokens":2,"cost":{"amount":0.02,"currency":"credits"}}}`)
	}))
	defer srv.Close()
	got, err := (&OpenAICompatible{BaseURL: srv.URL, UsageCostCurrency: "USD"}).CompleteWithUsage(context.Background(), "s", "u")
	if err == nil || got.Text != "" || got.Usage.InputTokens == nil || *got.Usage.InputTokens != 7 || got.Usage.OutputTokens == nil || *got.Usage.OutputTokens != 2 {
		t.Fatalf("invalid opted-in cost did not fail with valid tokens retained: %+v err=%v", got, err)
	}
}

func TestUsage_CurrencyRejectedBeforeSend(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	got, err := (&OpenAICompatible{BaseURL: srv.URL, UsageCostCurrency: "EUR"}).CompleteWithUsage(context.Background(), "s", "u")
	if err == nil || calls != 0 || got.Transmission != core.TransmissionNotSent {
		t.Fatalf("got %+v err=%v calls=%d", got, err, calls)
	}
}

func int64Ptr(v int64) *int64 { return &v }
func sameInt(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
