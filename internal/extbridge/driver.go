package extbridge

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/dndungu/ferro/internal/core"
)

// Command is the resolved, version-one driver wire format. It deliberately
// differs from a planner Action: refs have already become CSS selectors.
type Command struct {
	Op          string            `json:"op"`
	URL         string            `json:"url,omitempty"`
	Selector    string            `json:"selector,omitempty"`
	Text        string            `json:"text,omitempty"`
	Value       string            `json:"value,omitempty"`
	To          string            `json:"to,omitempty"`
	Fields      map[string]string `json:"fields,omitempty"`
	MaxElements int               `json:"maxElements,omitempty"`
	BudgetMS    int64             `json:"budgetMs,omitempty"`
	DeadlineMS  int64             `json:"deadlineMs,omitempty"`
	Origin      string            `json:"origin,omitempty"`
}

type ExtensionDriver struct {
	Bridge *Bridge
	// CheckURL authorizes content reads and effects at the current/target URL.
	CheckURL func(string) error
}

var _ core.PageDriver = (*ExtensionDriver)(nil)

func (d *ExtensionDriver) exchange(ctx context.Context, a Command) (Reply, error) {
	if err := ctx.Err(); err != nil {
		return Reply{}, err
	}
	if !d.Bridge.Connected() {
		return Reply{}, &core.StopError{Code: "disconnected", Message: "pair a Chrome tab using the Ferro extension"}
	}
	deadline := time.Now().Add(5 * time.Second)
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	a.DeadlineMS = deadline.UnixMilli()
	a.BudgetMS = time.Until(deadline).Milliseconds()
	callCtx, cancel := context.WithDeadline(ctx, time.UnixMilli(a.DeadlineMS))
	defer cancel()
	reply, err := d.Bridge.Enqueue(callCtx, a)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return reply, &core.StopError{Code: "disconnected", Message: "extension did not pick up the command before its deadline"}
		}
		return reply, err
	}
	if reply.Blocked != "" {
		code := reply.Code
		if code == "" {
			code = "blocked"
		}
		return reply, &core.StopError{Code: code, Message: reply.Blocked}
	}
	if reply.Error != "" {
		if reply.Code != "" {
			return reply, &core.StopError{Code: reply.Code, Message: reply.Error}
		}
		return reply, fmt.Errorf("extension %s: %s", a.Op, reply.Error)
	}
	return reply, nil
}
func (d *ExtensionDriver) Location(ctx context.Context) (string, error) {
	r, err := d.exchange(ctx, Command{Op: "location"})
	if err != nil {
		return "", err
	}
	v, ok := r.Result.(string)
	if !ok {
		return "", fmt.Errorf("extension location: expected URL")
	}
	return v, nil
}
func (d *ExtensionDriver) command(ctx context.Context, a Command) (Reply, error) {
	ctx = d.Bridge.Pin(ctx)
	raw, err := d.Location(ctx)
	if err != nil {
		return Reply{}, err
	}
	if a.Op == "navigate" {
		raw = a.URL
	}
	if d.CheckURL != nil {
		if err := d.CheckURL(raw); err != nil {
			return Reply{}, err
		}
	}
	// Rechecked by the content script immediately before touching the DOM.
	a.Origin = rawOrigin(raw)
	return d.exchange(ctx, a)
}
func (d *ExtensionDriver) Navigate(ctx context.Context, url string) error {
	_, e := d.command(ctx, Command{Op: "navigate", URL: url})
	return e
}
func (d *ExtensionDriver) Click(ctx context.Context, sel string) error {
	_, e := d.command(ctx, Command{Op: "click", Selector: sel})
	return e
}
func (d *ExtensionDriver) Fill(ctx context.Context, sel, text string) error {
	_, e := d.command(ctx, Command{Op: "fill", Selector: sel, Text: text})
	return e
}
func (d *ExtensionDriver) Select(ctx context.Context, sel, value string) (string, error) {
	_, e := d.command(ctx, Command{Op: "select", Selector: sel, Value: value})
	return "ok", e
}
func (d *ExtensionDriver) Key(ctx context.Context, key string) error {
	_, e := d.command(ctx, Command{Op: "key", Text: key})
	return e
}
func (d *ExtensionDriver) Scroll(ctx context.Context, to string) error {
	_, e := d.command(ctx, Command{Op: "scroll", To: to})
	return e
}
func (d *ExtensionDriver) WaitVisible(ctx context.Context, sel string) error {
	_, e := d.command(ctx, Command{Op: "wait_visible", Selector: sel})
	return e
}
func (d *ExtensionDriver) Settle(ctx context.Context) error {
	_, e := d.command(ctx, Command{Op: "settle"})
	return e
}
func (d *ExtensionDriver) ExtractField(ctx context.Context, sel string) (string, error) {
	r, e := d.command(ctx, Command{Op: "extract", Fields: map[string]string{"value": sel}})
	if e != nil {
		return "", e
	}
	obj, ok := r.Result.(map[string]any)
	if !ok {
		return "", fmt.Errorf("extension extract: expected object")
	}
	value, ok := obj["value"].(string)
	if !ok {
		return "", fmt.Errorf("extension extract: missing value")
	}
	return value, nil
}
func (d *ExtensionDriver) ExtractText(ctx context.Context) (string, error) {
	r, e := d.command(ctx, Command{Op: "extract"})
	if e != nil {
		return "", e
	}
	v, ok := r.Result.(string)
	if !ok {
		return "", fmt.Errorf("extension extract: expected text")
	}
	return v, nil
}
func (d *ExtensionDriver) Snapshot(ctx context.Context, max int) (*core.Snapshot, error) {
	r, e := d.command(ctx, Command{Op: "snapshot", MaxElements: max})
	if e != nil {
		return nil, e
	}
	if r.Snapshot == nil {
		return nil, fmt.Errorf("extension snapshot: missing snapshot")
	}
	return r.Snapshot, nil
}

func rawOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
