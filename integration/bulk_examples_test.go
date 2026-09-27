package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sirerun/ferro/internal/mcp"
)

// This examples check runs the frozen public validators against the operator
// guide's request and result envelopes, rather than only checking JSON syntax.
func TestBulkExamples(t *testing.T) {
	root := filepath.Join("..", "docs", "examples", "bulk-v2")
	for _, name := range []string{"read-only-request.json", "blocked-request.json", "budget-request.json", "artifact-request.json"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := mcp.ValidateTaskRequest(raw); err != nil {
				t.Fatalf("frozen request validation: %v", err)
			}
		})
	}
	for _, name := range []string{"blocked-result.json", "artifact-result.json"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			var result mcp.TaskResult
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			if err := mcp.ValidateTaskResult(result); err != nil {
				t.Fatalf("frozen result validation: %v", err)
			}
		})
	}
	t.Run("artifact retrieval arguments", func(t *testing.T) {
		var args struct {
			ExecutionID string `json:"execution_id"`
			ArtifactID  string `json:"artifact_id"`
			Offset      int64  `json:"offset"`
			Limit       int64  `json:"limit"`
		}
		raw, err := os.ReadFile(filepath.Join(root, "read-artifact-arguments.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(raw, &args); err != nil {
			t.Fatal(err)
		}
		if args.ExecutionID == "" || args.ArtifactID == "" || args.Offset < 0 || args.Limit <= 0 || args.Limit > 65536 {
			t.Fatalf("invalid bounded artifact read arguments: %+v", args)
		}
	})
}
