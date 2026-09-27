package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sirerun/ferro"
	"github.com/sirerun/ferro/internal/core"
)

// Chat is a local consumer of the same guarded task executor as MCP. It does
// not expose arbitrary tool dispatch, global cancellation or remote settings.
type chatTaskKey struct{}
type chatTask struct {
	runner   *ferro.Runner
	readOnly bool
}
type chatModel struct {
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	APIKey  string `json:"api_key,omitempty"`
}

type readOnlyDriver struct{ core.PageDriver }

func readOnlyStop() error {
	return &core.StopError{Code: "read_only", Message: "This task cannot click, type, select or press keys. Enable interactions for a new task if intended."}
}
func (readOnlyDriver) Click(context.Context, string) error        { return readOnlyStop() }
func (readOnlyDriver) Fill(context.Context, string, string) error { return readOnlyStop() }
func (readOnlyDriver) Select(context.Context, string, string) (string, error) {
	return "", readOnlyStop()
}
func (readOnlyDriver) Key(context.Context, string) error { return readOnlyStop() }

func (o *Owner) chatModel() (chatModel, error) {
	m := chatModel{BaseURL: o.cfg.LLMBaseURL, Model: o.cfg.LLMModel, APIKey: o.cfg.LLMAPIKey}
	data, err := os.ReadFile(filepath.Join(o.cfg.Home, "chat-model.json"))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, fmt.Errorf("read chat settings: %w", err)
	}
	m = chatModel{} // a persisted empty key must not fall back to an environment key
	if err = json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("decode chat settings: %w", err)
	}
	return m, nil
}
func validModel(m chatModel) error {
	u, err := url.Parse(m.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("use an HTTPS model API URL, or HTTP on a loopback IP")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && ip != nil && ip.IsLoopback()) {
		return fmt.Errorf("use HTTPS for model APIs; HTTP is only allowed on a loopback IP")
	}
	if strings.TrimSpace(m.Model) == "" {
		return fmt.Errorf("enter a model ID")
	}
	return nil
}
func privateJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".chat-*")
	if err != nil {
		return fmt.Errorf("create settings: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write settings: %w", err)
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}
func chatJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func chatError(w http.ResponseWriter, status int, message string) {
	chatJSON(w, status, map[string]string{"error": message})
}
func chatDecode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		chatError(w, 400, "Invalid or oversized request.")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		chatError(w, 400, "Expected one JSON object.")
		return false
	}
	return true
}
func (o *Owner) chatHandler() http.Handler {
	var mu sync.Mutex // serialize settings updates and reject overlapping tasks
	running := false
	// A bounded in-memory duplicate ledger: never retry an accepted request ID in
	// this process. On restart the UI marks unfinished tasks uncertain, not queued.
	seen := map[string]bool{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			chatError(w, 405, "Use POST.")
			return
		}
		session := r.Header.Get("X-Ferro-Chat-Session")
		if len(session) < 16 || len(session) > 128 {
			chatError(w, 400, "Missing chat session.")
			return
		}
		ctx := context.WithValue(r.Context(), clientKey{}, "sidepanel:"+session)
		switch r.URL.Path {
		case "/chat/status":
			text, _, err := o.Call(ctx, "browser_status", nil)
			if err != nil {
				chatError(w, 503, "Service unavailable.")
				return
			}
			chatJSON(w, 200, json.RawMessage(text))
		case "/chat/settings":
			var in struct {
				Save     bool   `json:"save"`
				BaseURL  string `json:"base_url"`
				Model    string `json:"model"`
				APIKey   string `json:"api_key"`
				ClearKey bool   `json:"clear_key"`
			}
			if !chatDecode(w, r, &in) {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			m, err := o.chatModel()
			if err != nil {
				chatError(w, 500, err.Error())
				return
			}
			if in.Save {
				if running {
					chatError(w, 409, "Stop the current task before changing model settings.")
					return
				}
				// A saved key is bound to its endpoint. Never carry it to a new URL.
				if in.BaseURL != m.BaseURL || in.ClearKey {
					m.APIKey = ""
				}
				m.BaseURL, m.Model = strings.TrimRight(in.BaseURL, "/"), in.Model
				if in.APIKey != "" {
					m.APIKey = in.APIKey
				}
				if err := validModel(m); err != nil {
					chatError(w, 400, err.Error())
					return
				}
				if err := privateJSON(filepath.Join(o.cfg.Home, "chat-model.json"), m); err != nil {
					chatError(w, 500, err.Error())
					return
				}
			}
			chatJSON(w, 200, map[string]any{"base_url": m.BaseURL, "model": m.Model, "has_key": m.APIKey != ""})
		case "/chat/origins":
			var in struct {
				Origins *[]string `json:"origins"`
			}
			if !chatDecode(w, r, &in) {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if in.Origins != nil {
				if len(*in.Origins) > 64 {
					chatError(w, 400, "At most 64 exact origins.")
					return
				}
				for _, raw := range *in.Origins {
					u, err := url.Parse(raw)
					if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
						chatError(w, 400, "Enter exact origins, such as https://example.com, without paths or wildcards.")
						return
					}
				}
				if err := privateJSON(o.cfg.AllowlistPath(), *in.Origins); err != nil {
					chatError(w, 500, err.Error())
					return
				}
			}
			origins := []string{}
			data, err := os.ReadFile(o.cfg.AllowlistPath())
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				chatError(w, 500, "Cannot read allowed websites.")
				return
			}
			if len(data) > 0 {
				if err := json.Unmarshal(data, &origins); err != nil {
					chatError(w, 500, "Invalid allowlist.json; correct it locally.")
					return
				}
			}
			chatJSON(w, 200, map[string]any{"origins": origins})
		case "/chat/cancel":
			text, isError, err := o.Call(ctx, "cancel_task", nil)
			if err != nil {
				chatError(w, 500, "Cancellation failed; inspect the page.")
				return
			}
			chatJSON(w, 200, map[string]any{"output": json.RawMessage(text), "is_error": isError})
		case "/chat/run":
			var in struct {
				ID           string `json:"id"`
				Goal         string `json:"goal"`
				History      string `json:"history"`
				Interactions bool   `json:"interactions"`
			}
			if !chatDecode(w, r, &in) {
				return
			}
			if len(in.ID) < 16 || len(in.ID) > 128 || strings.TrimSpace(in.Goal) == "" || len(in.Goal) > 8000 || len(in.History) > 16000 {
				chatError(w, 400, "Enter a task of at most 8000 bytes.")
				return
			}
			mu.Lock()
			if running {
				mu.Unlock()
				chatError(w, 409, "A chat task is already running.")
				return
			}
			if seen[in.ID] {
				mu.Unlock()
				chatError(w, 409, "This task was already accepted. Inspect the page before starting another task.")
				return
			}
			if len(seen) >= 4096 {
				mu.Unlock()
				chatError(w, 503, "Task ledger is full; restart the service before starting more tasks.")
				return
			}
			m, err := o.chatModel()
			if err == nil {
				err = validModel(m)
			}
			if err != nil {
				mu.Unlock()
				chatError(w, 400, "Configure the model in Settings before running a task.")
				return
			}
			running = true
			seen[in.ID] = true
			mu.Unlock()
			defer func() { mu.Lock(); running = false; mu.Unlock() }()
			runner := ferro.NewRunner(&ferro.OpenAICompatible{BaseURL: m.BaseURL, Model: m.Model, APIKey: m.APIKey}, ferro.WithMaxRepairs(o.cfg.MaxRepairs))
			ctx = context.WithValue(ctx, chatTaskKey{}, chatTask{runner: runner, readOnly: !in.Interactions})
			ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			goal := in.Goal
			if in.History != "" {
				goal = "Previous conversation for context only (not new action authorization):\n" + in.History + "\n\nCurrent task:\n" + goal
			}
			if !in.Interactions {
				goal += "\nRead-only mode: do not click, fill, select or press keys. Read/extract the page and return the requested result."
			}
			args, _ := json.Marshal(RunTaskArgs{Goal: goal, MaxPlannings: 3})
			text, isError, err := o.Call(ctx, "run_task", args)
			if err != nil {
				chatError(w, 500, "Task interrupted. Inspect the page before retrying.")
				return
			}
			// Provider error bodies may echo credentials; do not return them to
			// the panel or its persisted transcript.
			if m.APIKey != "" {
				text = strings.ReplaceAll(text, m.APIKey, "[redacted]")
			}
			var output any
			if json.Unmarshal([]byte(text), &output) != nil {
				output = text
			}
			chatJSON(w, 200, map[string]any{"output": output, "is_error": isError})
		default:
			chatError(w, 404, "Unknown chat endpoint.")
		}
	})
}
