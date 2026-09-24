package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type clientKey struct{}

const maxLeaseSeconds = 900

func clientIdentity(ctx context.Context) string {
	v, _ := ctx.Value(clientKey{}).(string)
	if v == "" {
		return "local"
	}
	return v
}

type identifiedCaller struct {
	caller
	namespace string
}

func (c *identifiedCaller) Call(ctx context.Context, t string, a json.RawMessage) (string, bool, error) {
	return c.caller.Call(context.WithValue(ctx, clientKey{}, c.namespace+":"+clientIdentity(ctx)), t, a)
}

type leaseArgs struct {
	Seconds int `json:"seconds,omitempty" jsonschema:"lease duration from 1 to 900 seconds (default 300); call acquire_tab again to renew"`
}

func registerSessionTools(s *sdk.Server, c caller) {
	addRelayTool[leaseArgs](s, &sdk.Tool{Name: "acquire_tab", Description: "Reserve the paired tab across a sequence of direct tool calls. Required in extension mode. Renew with another acquire_tab; release_tab when finished."}, c)
	addRelayTool[snapshotArgs](s, &sdk.Tool{Name: "release_tab", Description: "Release this MCP session's tab lease and clear its snapshot refs."}, c)
	addRelayTool[snapshotArgs](s, &sdk.Tool{Name: "browser_status", Description: "Report connection, busy and lease state without reading page content or credentials."}, c)
	addRelayTool[snapshotArgs](s, &sdk.Tool{Name: "cancel_task", Description: "Cancel this MCP session's active task. An already dispatched action may have taken effect; inspect before retrying."}, c)
}
func stopResult(code, message string) (string, bool, error) {
	b, _ := json.Marshal(map[string]string{"status": code, "message": message})
	return string(b), true, nil
}
func (o *Owner) lease(who, tool string, args json.RawMessage) (string, bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if time.Now().After(o.leaseUntil) {
		o.leaseOwner = ""
		o.snap = nil
		o.snapGeneration = 0
		o.extracted = map[string]any{}
	}
	if o.leaseOwner != "" && o.leaseOwner != who {
		return stopResult("tab_busy", "another MCP session owns the tab")
	}
	if tool == "release_tab" {
		o.leaseOwner = ""
		o.snap = nil
		o.snapGeneration = 0
		o.extracted = map[string]any{}
		return `{"status":"released"}`, false, nil
	}
	var in leaseArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", false, err
		}
	}
	if in.Seconds == 0 {
		in.Seconds = 300
	}
	if in.Seconds < 1 || in.Seconds > maxLeaseSeconds {
		return stopResult("invalid_lease", "seconds must be between 1 and 900")
	}
	o.leaseOwner = who
	o.leaseUntil = time.Now().Add(time.Duration(in.Seconds) * time.Second)
	b, _ := json.Marshal(map[string]any{"status": "acquired", "expires_at": o.leaseUntil})
	return string(b), false, nil
}
func (o *Owner) control(ctx context.Context, tool string) (string, bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if tool == "cancel_task" {
		if o.activeCancel == nil {
			return `{"status":"idle"}`, false, nil
		}
		if o.activeOwner != clientIdentity(ctx) {
			return stopResult("tab_busy", "only the session running this task may cancel it")
		}
		o.activeCancel()
		return `{"status":"cancellation_requested"}`, false, nil
	}
	connected := true
	tab := ""
	bridge := ""
	if o.bridge != nil {
		connected = o.bridge.Connected()
		tab = o.bridge.PairedTab()
		bridge = o.bridge.Addr()
	}
	b, err := json.Marshal(map[string]any{"backend": o.cfg.Backend, "connected": connected, "paired_tab": tab, "bridge": bridge, "remote": o.remoteAddr, "busy": o.activeCancel != nil, "leased": o.leaseOwner != "" && time.Now().Before(o.leaseUntil)})
	if err != nil {
		return "", false, fmt.Errorf("encode status: %w", err)
	}
	return string(b), false, nil
}
