package mcp

import (
	"context"
	"encoding/json"
)

// relayRequest and relayResponse are ferro's own line-delimited JSON
// encoding spoken between a shim process and the owner's Unix socket (ADR
// 004). This is never MCP JSON-RPC — MCP is spoken only between each
// process and its own client, on that process's own stdio.
type relayRequest struct {
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args,omitempty"`
}

type relayResponse struct {
	Text    string `json:"text"`
	IsError bool   `json:"is_error,omitempty"`
}

// controlStatus and controlStop are pseudo tool names the owner's socket
// server handles directly (ferro-mcp status/stop) rather than dispatching
// to a registered MCP tool.
const (
	controlStatus = "__status__"
	controlStop   = "__stop__"
)

// caller is how a registered MCP tool handler gets its answer: directly
// from an Owner in-process, or relayed to the owner over the Unix socket by
// a Leader acting as a shim. Both Owner and Leader implement it.
type caller interface {
	Call(ctx context.Context, tool string, args json.RawMessage) (text string, isError bool, err error)
}
