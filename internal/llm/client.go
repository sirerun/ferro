// Package llm holds LLM client implementations that satisfy
// core.LLMClient. Kept as its own internal package so adding a second
// backend (a native Anthropic client, a gRPC gateway, ...) never touches
// the engine.
package llm

import "context"

// Client mirrors core.LLMClient structurally: any type here that implements
// Complete satisfies the engine's LLMClient interface without this package
// importing core (avoids an import cycle and keeps llm reusable on its
// own).
type Client interface {
	// Complete returns raw text for a chat-style prompt. Implementations
	// should request JSON-constrained decoding where available.
	Complete(ctx context.Context, system, user string) (string, error)
}
