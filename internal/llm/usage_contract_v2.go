package llm

import (
	"context"
	"github.com/dndungu/ferro/internal/core"
)

// MetadataCompleterV2 issues exactly one HTTP request per invocation and performs no fallback retry.
type MetadataCompleterV2 interface {
	CompleteWithUsage(context.Context, string, string) (core.CompletionV2, error)
}
