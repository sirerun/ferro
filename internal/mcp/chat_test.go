package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirerun/ferro/internal/core"
)

func chatRequest(t *testing.T, o *Owner, route string, body any, mutate func(*http.Request)) (int, string) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", "http://"+o.bridge.Addr()+"/chat/"+route, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+o.bridge.Token())
	req.Header.Set("X-Ferro-Chat-Session", "test-chat-session-1234")
	if mutate != nil {
		mutate(req)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(out)
}
func TestChatAuthenticationAndSettings(t *testing.T) {
	o := extensionOwner(t)
	for _, tc := range []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"missing token", func(r *http.Request) { r.Header.Del("Authorization") }},
		{"wrong token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }},
		{"web origin", func(r *http.Request) { r.Header.Set("Origin", "https://attacker.test") }},
		{"rebound host", func(r *http.Request) { r.Host = "attacker.test" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := chatRequest(t, o, "status", map[string]any{}, tc.mutate)
			if status != 401 {
				t.Fatalf("got %d", status)
			}
		})
	}
	if status, _ := chatRequest(t, o, "status", map[string]any{}, nil); status != 200 {
		t.Fatal(status)
	}
	key := "test-secret-not-for-logs"
	save := map[string]any{"save": true, "base_url": "https://openrouter.ai/api/v1", "model": "test-model", "api_key": key}
	status, out := chatRequest(t, o, "settings", save, nil)
	if status != 200 || strings.Contains(out, key) || !strings.Contains(out, `"has_key":true`) {
		t.Fatalf("save status %d; key redaction or presence failed", status)
	}
	info, err := os.Stat(filepath.Join(o.cfg.Home, "chat-model.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("key file permissions %o", info.Mode().Perm())
	}
	// Changing endpoint must never silently forward the previous key.
	delete(save, "api_key")
	save["base_url"] = "https://other-provider.test/v1"
	status, out = chatRequest(t, o, "settings", save, nil)
	if status != 200 || !strings.Contains(out, `"has_key":false`) {
		t.Fatalf("old key followed endpoint change: %s", out)
	}
	o.cfg.LLMAPIKey = "environment-key" // no concurrent request while mutating fixture configuration
	m, err := o.chatModel()
	if err != nil {
		t.Fatal(err)
	}
	if m.APIKey != "" {
		t.Fatal("cleared persisted key fell back to environment")
	}
	for _, url := range []string{"http://remote.test/v1", "https://user:secret@example.test/v1", "https://example.test/v1?key=secret"} {
		save["base_url"] = url
		if status, _ := chatRequest(t, o, "settings", save, nil); status != 400 {
			t.Fatalf("accepted invalid endpoint %s", url)
		}
	}
	if status, _ := chatRequest(t, o, "origins", map[string]any{"origins": []string{"https://example.test/path"}}, nil); status != 400 {
		t.Fatal("accepted path as origin")
	}
	if status, _ := chatRequest(t, o, "origins", map[string]any{"origins": []string{"https://example.test"}}, nil); status != 200 {
		t.Fatal(status)
	}
	if err := o.allow.Check("https://example.test"); err != nil {
		t.Fatal(err)
	}
	if status, _ := chatRequest(t, o, "origins", map[string]any{"origins": []string{}}, nil); status != 200 {
		t.Fatal(status)
	}
	if err := o.allow.Check("https://example.test"); err == nil {
		t.Fatal("revoked origin remained authorized")
	}
	// Accepted task IDs cannot be retried even if the tab was disconnected.
	run := map[string]any{"id": "unique-request-12345", "goal": "read the page"}
	if status, _ := chatRequest(t, o, "run", run, nil); status != 200 {
		t.Fatal(status)
	}
	if status, _ := chatRequest(t, o, "run", run, nil); status != 409 {
		t.Fatal("replayed accepted task")
	}
}
func TestChatReadOnlyBlocksMutations(t *testing.T) {
	d := readOnlyDriver{}
	checks := []func() error{
		func() error { return d.Click(context.Background(), "button") },
		func() error { return d.Fill(context.Background(), "input", "text") },
		func() error { _, e := d.Select(context.Background(), "select", "choice"); return e },
		func() error { return d.Key(context.Background(), "Enter") },
	}
	for _, check := range checks {
		var stop *core.StopError
		if !errors.As(check(), &stop) || stop.Code != "read_only" {
			t.Fatal("mutation not stopped")
		}
	}
}
