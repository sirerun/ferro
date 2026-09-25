package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/dndungu/ferro/internal/core"
)

const (
	taskResultInlineBytes   = 16 << 10
	taskResultArtifactBytes = 4 << 20
	taskResultSummaryBytes  = 2 << 10
)

// BuildTaskResult constructs a bounded result envelope from trusted execution
// evidence. G02 validates successful output against the original task schema
// before setting record.Validation to "valid".
func BuildTaskResult(ctx context.Context, record ExecutionRecord, sink ArtifactSink) (TaskResult, error) {
	if ctx == nil {
		return TaskResult{}, fmt.Errorf("build task result: nil context")
	}
	if err := ctx.Err(); err != nil {
		return TaskResult{}, fmt.Errorf("build task result: %w", err)
	}
	result := TaskResult{
		Schema:          "ferro.result/v2",
		TaskID:          record.TaskID,
		ExecutionID:     record.ExecutionID,
		Status:          record.Status,
		StartedAt:       record.StartedAt,
		EndedAt:         record.EndedAt,
		ModelProfile:    record.Profile,
		ProfileRevision: record.ProfileRevision,
		Model:           record.Model,
		EffectiveLimits: cloneResultLimits(record.Limits),
		Validation:      record.Validation,
		Summary:         truncateResultSummary(record.Summary),
		Usage:           cloneResultUsage(record.Usage),
		Budget:          cloneResultBudget(record.Budget),
		SideEffectState: record.SideEffectState,
		Artifacts:       append([]Artifact(nil), record.Artifacts...),
	}
	if record.Error != nil {
		// Detail can contain provider text or credentials. Category, stage and
		// retry are the explicit diagnostic fields; arbitrary detail is omitted.
		safeError := *record.Error
		safeError.Detail = ""
		if result.SideEffectState == SideEffectUnknown && safeError.Retry == "known_not_executed" {
			safeError.Retry = "reconcile_only"
		}
		result.Error = &safeError
	}
	if len(record.PartialResult) > 0 {
		if len(record.PartialResult) > taskResultInlineBytes {
			return TaskResult{}, fmt.Errorf("partial result exceeds 16 KiB limit")
		}
		partial, err := compactResultJSON(record.PartialResult)
		if err != nil {
			return TaskResult{}, fmt.Errorf("validate partial result JSON: %w", err)
		}
		if len(partial) > taskResultInlineBytes {
			return TaskResult{}, fmt.Errorf("partial result exceeds serialized inline limit")
		}
		result.PartialResult = partial
	}

	if result.Status == TaskSucceeded {
		if record.Validation != "valid" {
			result.Status = TaskFailed
			result.Result = nil
			result.ResultArtifactID = ""
			result.Error = &TaskError{Category: "output_invalid", Stage: "validation", Retry: "never"}
		} else {
			if result.Error != nil || len(record.PartialResult) != 0 || record.ResultArtifactID != "" {
				return TaskResult{}, fmt.Errorf("inconsistent successful execution record")
			}
			if len(record.Result) == 0 {
				return TaskResult{}, fmt.Errorf("successful execution has no result")
			}
			if !utf8.Valid(record.Result) || !json.Valid(record.Result) {
				return TaskResult{}, fmt.Errorf("successful execution result is not valid UTF-8 JSON")
			}
			inline, err := compactResultJSON(record.Result)
			if err != nil {
				return TaskResult{}, fmt.Errorf("encode successful result: %w", err)
			}
			if len(record.Result) > taskResultArtifactBytes {
				return TaskResult{}, fmt.Errorf("accepted result exceeds 4 MiB artifact limit")
			}
			if len(record.Result) <= taskResultInlineBytes && len(inline) <= taskResultInlineBytes {
				result.Result = inline
			} else {
				artifactBytes := record.Result
				if len(record.Result) <= taskResultInlineBytes {
					// The compact form is the JSON representation that would be
					// persisted inline. Preserve its value in the artifact so its
					// digest and size describe the bytes actually stored.
					artifactBytes = inline
				}
				if len(artifactBytes) <= taskResultInlineBytes || len(artifactBytes) > taskResultArtifactBytes {
					return TaskResult{}, fmt.Errorf("accepted result cannot fit bounded inline or artifact representation")
				}
				if sink == nil {
					return TaskResult{}, fmt.Errorf("artifact sink required for oversized result")
				}
				artifact, err := sink.PutArtifact(ctx, record.Owner, record.ExecutionID, append([]byte(nil), artifactBytes...), "application/json")
				if err != nil {
					return TaskResult{}, fmt.Errorf("store oversized task result: %w", err)
				}
				if err := validateBuiltArtifact(artifact, artifactBytes); err != nil {
					return TaskResult{}, err
				}
				result.ResultArtifactID = artifact.ID
				result.Artifacts = append(result.Artifacts, artifact)
			}
		}
	} else if len(record.Result) > 0 || record.ResultArtifactID != "" {
		return TaskResult{}, fmt.Errorf("failed execution record contains accepted result")
	}

	if err := ValidateTaskResult(result); err != nil {
		return TaskResult{}, fmt.Errorf("validate built task result: %w", err)
	}
	// Receipt persistence serializes RawMessage with HTML escaping. Ensure that
	// this exact serialized inline form remains under the contract byte limit.
	serialized, err := json.Marshal(result)
	if err != nil {
		return TaskResult{}, fmt.Errorf("encode built task result: %w", err)
	}
	var persisted TaskResult
	if err := json.Unmarshal(serialized, &persisted); err != nil {
		return TaskResult{}, fmt.Errorf("decode serialized task result: %w", err)
	}
	if err := ValidateTaskResult(persisted); err != nil {
		return TaskResult{}, fmt.Errorf("validate serialized task result: %w", err)
	}
	return persisted, nil
}

func compactResultJSON(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON")
	}
	return json.Marshal(json.RawMessage(raw))
}

func validateBuiltArtifact(artifact Artifact, data []byte) error {
	sum := sha256.Sum256(data)
	if !validTaskID(artifact.ID) || artifact.SHA256 != hex.EncodeToString(sum[:]) || artifact.Size != int64(len(data)) || artifact.MediaType != "application/json" {
		return fmt.Errorf("artifact sink returned inconsistent metadata")
	}
	if artifact.Size <= taskResultInlineBytes || artifact.Size > taskResultArtifactBytes {
		return fmt.Errorf("artifact sink returned size outside bounds")
	}
	if !utf8.Valid(data) || !json.Valid(data) {
		return fmt.Errorf("artifact payload is not valid UTF-8 JSON")
	}
	return nil
}

func truncateResultSummary(summary string) string {
	if !utf8.ValidString(summary) {
		summary = strings.ToValidUTF8(summary, "\uFFFD")
	}
	if len(summary) <= taskResultSummaryBytes {
		return summary
	}
	end := taskResultSummaryBytes
	for end > 0 && !utf8.ValidString(summary[:end]) {
		end--
	}
	return summary[:end]
}

func cloneResultUsage(usage core.RequestUsage) core.RequestUsage {
	usage.InputTokens = cloneResultInt64(usage.InputTokens)
	usage.OutputTokens = cloneResultInt64(usage.OutputTokens)
	usage.TotalTokens = cloneResultInt64(usage.TotalTokens)
	usage.ReasoningTokens = cloneResultInt64(usage.ReasoningTokens)
	usage.CacheReadTokens = cloneResultInt64(usage.CacheReadTokens)
	usage.CacheWriteTokens = cloneResultInt64(usage.CacheWriteTokens)
	usage.BilledMicroUSD = cloneResultInt64(usage.BilledMicroUSD)
	return usage
}

func cloneResultBudget(budget core.BudgetSnapshot) core.BudgetSnapshot {
	budget.ReportedUsage = cloneResultUsage(budget.ReportedUsage)
	budget.ReservedMicroUSD = cloneResultInt64(budget.ReservedMicroUSD)
	budget.UnresolvedMicroUSD = cloneResultInt64(budget.UnresolvedMicroUSD)
	return budget
}

func cloneResultLimits(limits core.Limits) core.Limits {
	limits.ReserveMicroUSD = cloneResultInt64(limits.ReserveMicroUSD)
	return limits
}

func cloneResultInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
