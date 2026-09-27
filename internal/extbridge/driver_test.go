package extbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/sirerun/ferro/internal/core"
)

func TestCanceledQueueIsNeverDispatched(t *testing.T) {
	b, err := New(WithPollTimeout(20 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Stop(context.Background()) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = b.Enqueue(ctx, Command{Op: "click", Selector: "#purchase"})
	req, _ := http.NewRequest("GET", "http://"+b.Addr()+"/next", nil)
	req.Header.Set("Authorization", "Bearer "+b.Token())
	req.Header.Set(TabIDHeader, "42")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatalf("canceled click dispatched: HTTP %d", res.StatusCode)
	}
}
func TestDriverWireAndTerminalStates(t *testing.T) {
	for _, state := range []string{"success", "blocked", "login_required", "outcome_uncertain"} {
		t.Run(state, func(t *testing.T) {
			b, _ := New()
			if err := b.Start("127.0.0.1:0"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			t.Cleanup(func() { _ = b.Stop(context.Background()) })
			if err := b.pair("42"); err != nil {
				t.Fatal(err)
			}
			polled := make(chan Command, 4)
			workerDone := make(chan struct{})
			go func() {
				defer close(workerDone)
				for {
					req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+b.Addr()+"/next", nil)
					req.Header.Set("Authorization", "Bearer "+b.Token())
					req.Header.Set(TabIDHeader, "42")
					res, err := http.DefaultClient.Do(req)
					if err != nil {
						return
					}
					var next nextResponse
					err = json.NewDecoder(res.Body).Decode(&next)
					res.Body.Close()
					if err != nil {
						return
					}
					polled <- next.Action
					reply := replyRequest{ID: next.ID}
					if next.Action.Op == "location" {
						reply.Result = "https://example.test/page"
					} else if state == "success" {
						reply.Result = map[string]any{"value": "actual page value"}
					} else if state == "outcome_uncertain" {
						reply.Error = "input dispatched"
						reply.Code = state
					} else {
						reply.Blocked = "human intervention"
						reply.Code = state
					}
					body, _ := json.Marshal(reply)
					post, _ := http.NewRequestWithContext(ctx, "POST", "http://"+b.Addr()+"/reply", bytes.NewReader(body))
					post.Header.Set("Authorization", "Bearer "+b.Token())
					response, err := http.DefaultClient.Do(post)
					if err != nil {
						return
					}
					io.Copy(io.Discard, response.Body)
					response.Body.Close()
				}
			}()
			d := &ExtensionDriver{Bridge: b, CheckURL: func(raw string) error {
				if raw != "https://example.test/page" {
					t.Errorf("unexpected authorization URL %s", raw)
				}
				return nil
			}}
			value, err := d.ExtractField(ctx, "#price")
			if state == "success" {
				if err != nil || value != "actual page value" {
					t.Fatalf("value=%q err=%v", value, err)
				}
			} else {
				var stop *core.StopError
				if !errors.As(err, &stop) || stop.Code != state {
					t.Fatalf("want %s, got %v", state, err)
				}
			}
			<-polled
			a := <-polled
			if a.Op != "extract" || a.Fields["value"] != "#price" || a.Origin != "https://example.test" || a.DeadlineMS == 0 {
				t.Fatalf("bad wire command %+v", a)
			}
			cancel()
			<-workerDone
		})
	}
}
func TestDriverDisconnectedAndUnsafeBind(t *testing.T) {
	b, _ := New()
	d := &ExtensionDriver{Bridge: b}
	_, err := d.Location(context.Background())
	var stopped *core.StopError
	if !errors.As(err, &stopped) || stopped.Code != "disconnected" {
		t.Fatalf("%v", err)
	}
	for _, addr := range []string{"0.0.0.0:0", "192.168.1.1:0", ":0", "localhost:0"} {
		if err := b.Start(addr); err == nil {
			t.Fatalf("accepted %s", addr)
		}
	}
}

func TestPairingChangeInvalidatesPinnedTask(t *testing.T) {
	b, _ := New()
	if err := b.pair("old"); err != nil {
		t.Fatal(err)
	}
	ctx := b.Pin(context.Background())
	b.mu.Lock()
	b.pairedTab = ""
	b.generation++
	b.mu.Unlock()
	if err := b.pair("new"); err != nil {
		t.Fatal(err)
	}
	_, err := b.Enqueue(ctx, Command{Op: "click", Selector: "#purchase"})
	var stopped *core.StopError
	if !errors.As(err, &stopped) || stopped.Code != "pairing_changed" {
		t.Fatalf("old task reached new pairing: %v", err)
	}
	if len(b.queue) != 0 {
		t.Fatal("old command queued")
	}
}
