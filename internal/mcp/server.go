package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewServer builds an MCP server with the full ferro-mcp tool set
// registered against c. Both the owner and a shim call this with the same
// tool definitions (ADR 004: "Both roles run an identical mcp.NewServer...
// with the same registered tools... an MCP client cannot tell which role it
// talked to") — they differ only in what c.Call actually does: an Owner
// answers directly, a Leader acting as a shim relays over the socket.
func NewServer(c caller) *sdk.Server {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	c = &identifiedCaller{caller: c, namespace: hex.EncodeToString(nonce[:])}
	server := sdk.NewServer(&sdk.Implementation{Name: "ferro-mcp", Version: "0.1.0"}, nil)
	registerTaskTools(server, c)
	registerPrimitiveTools(server, c)
	registerSessionTools(server, c)
	return server
}

// addRelayTool registers one MCP tool whose handler marshals its typed
// arguments to JSON, hands them to c.Call, and wraps the answer back into an
// MCP result. Args carries the tool's declared shape (used by the SDK to
// generate its JSON input schema via reflection, same as the prototype's
// runTaskArgs).
func addRelayTool[Args any](server *sdk.Server, tool *sdk.Tool, c caller) {
	if tool.Annotations == nil {
		tool.Annotations = &sdk.ToolAnnotations{ReadOnlyHint: false}
	}
	sdk.AddTool(server, tool, func(ctx context.Context, req *sdk.CallToolRequest, args Args) (*sdk.CallToolResult, any, error) {
		argsJSON, err := json.Marshal(args)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal %s args: %w", tool.Name, err)
		}
		ctx = context.WithValue(ctx, clientKey{}, req.Session.ID())
		text, isError, err := c.Call(ctx, tool.Name, argsJSON)
		if err != nil {
			// A transport-level failure (e.g. the relay socket is gone and
			// promotion also failed) — distinct from a tool-level error,
			// which is reported as a normal (IsError) result below.
			return nil, nil, err
		}
		result := &sdk.CallToolResult{
			IsError: isError,
			Content: []sdk.Content{&sdk.TextContent{Text: text}},
		}
		if isError {
			return result, nil, nil
		}
		var out any
		_ = json.Unmarshal([]byte(text), &out)
		return result, out, nil
	})
}
