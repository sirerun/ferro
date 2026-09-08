package llm

import (
	"context"
	"encoding/json"
	"fmt"
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
