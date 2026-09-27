package llm

import (
	"context"
	"github.com/sirerun/ferro/internal/core"
)

// MetadataCompleter issues exactly one HTTP request per invocation and performs no fallback retry.
type MetadataCompleter interface {
	CompleteWithUsage(context.Context, string, string) (core.Completion, error)
}
