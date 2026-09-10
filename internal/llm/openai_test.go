package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestConcurrentClientAndSchema(t *testing.T) {
	for _, structured := range []bool{false, true} {
		t.Run(fmt.Sprint(structured), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request chatRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					http.Error(w, "invalid", 400)
					return
				}
				if request.MaxTokens != 2048 {
					t.Errorf("max tokens = %d", request.MaxTokens)
				}
				expected := "json_object"
				if structured {
					expected = "json_schema"
				}
				if request.ResponseFormat.Type != expected {
					t.Errorf("format = %s", request.ResponseFormat.Type)
				}
				fmt.Fprint(w, `{"choices":[{"message":{"content":"{}"}}]}`)
			}))
			defer srv.Close()
			client := &OpenAICompatible{BaseURL: srv.URL + "/", Model: "fixture", UseJSONSchema: structured}
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if _, err := client.CompleteSchema(context.Background(), "s", "u", json.RawMessage(`{"type":"object"}`)); err != nil {
						t.Error(err)
					}
				}()
			}
			wg.Wait()
			if client.Timeout != 0 || client.MaxTokens != 0 {
				t.Fatal("client mutated configuration")
			}
		})
	}
}

// TestOpenAI_SchemaFallback covers the endpoint-fallback landmine: a gateway
// that advertises response_format:json_schema in its API but rejects it at
// request time with an HTTP 400. The client should retry that one request
// with json_object, then remember the downgrade so later requests on the
// same client never pay the 400 round trip again.
func TestOpenAI_SchemaFallback(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		var req chatRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, req.ResponseFormat.Type)

		if len(calls) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"Invalid parameter: 'response_format' of type 'json_schema' is not supported with this model."}}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{}"}}]}`)
	}))
	defer srv.Close()

	client := &OpenAICompatible{BaseURL: srv.URL, Model: "fixture", UseJSONSchema: true}

	// Call 1: json_schema 400s, retries with json_object within the same
	// CompleteSchema call and succeeds.
	if _, err := client.CompleteSchema(context.Background(), "s", "u", json.RawMessage(`{"type":"object"}`)); err != nil {
		t.Fatalf("first CompleteSchema: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("requests after first call = %d, want 2: %v", len(calls), calls)
	}
	if calls[0] != "json_schema" {
		t.Fatalf("request 1 format = %s, want json_schema", calls[0])
	}
	if calls[1] != "json_object" {
		t.Fatalf("request 2 (fallback retry) format = %s, want json_object", calls[1])
	}

	// Call 2: the downgrade is remembered, so this request skips json_schema
	// entirely — one request, already json_object.
	if _, err := client.CompleteSchema(context.Background(), "s", "u", json.RawMessage(`{"type":"object"}`)); err != nil {
		t.Fatalf("second CompleteSchema: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("total requests = %d, want 3: %v", len(calls), calls)
	}
	if calls[2] != "json_object" {
		t.Fatalf("request 3 format = %s, want json_object", calls[2])
	}
}
