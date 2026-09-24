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
	taskResultInlineBytesV2   = 16 << 10
	taskResultArtifactBytesV2 = 4 << 20
	taskResultSummaryBytesV2  = 2 << 10
)

// BuildTaskResultV2 constructs a bounded result envelope from trusted execution
// evidence. G02 validates successful output against the original task schema
// before setting record.Validation to "valid".
func BuildTaskResultV2(ctx context.Context, record ExecutionRecordV2, sink ArtifactSinkV2) (TaskResultV2, error) {
	if ctx == nil {
		return TaskResultV2{}, fmt.Errorf("build task result: nil context")
	}
	if err := ctx.Err(); err != nil {
		return TaskResultV2{}, fmt.Errorf("build task result: %w", err)
	}
	result := TaskResultV2{
		Schema:          "ferro.result/v2",
		TaskID:          record.TaskID,
		ExecutionID:     record.ExecutionID,
		Status:          record.Status,
		StartedAt:       record.StartedAt,
		EndedAt:         record.EndedAt,
		ModelProfile:    record.Profile,
		ProfileRevision: record.ProfileRevision,
		Model:           record.Model,
		EffectiveLimits: cloneResultLimitsV2(record.Limits),
		Validation:      record.Validation,
		Summary:         truncateResultSummaryV2(record.Summary),
		Usage:           cloneResultUsageV2(record.Usage),
		Budget:          cloneResultBudgetV2(record.Budget),
		SideEffectState: record.SideEffectState,
		Artifacts:       append([]ArtifactV2(nil), record.Artifacts...),
	}
	if record.Error != nil {
		// Detail can contain provider text or credentials. Category, stage and
		// retry are the explicit diagnostic fields; arbitrary detail is omitted.
		safeError := *record.Error
		safeError.Detail = ""
		if result.SideEffectState == SideEffectUnknownV2 && safeError.Retry == "known_not_executed" {
			safeError.Retry = "reconcile_only"
		}
		result.Error = &safeError
	}
	if len(record.PartialResult) > 0 {
		if len(record.PartialResult) > taskResultInlineBytesV2 {
			return TaskResultV2{}, fmt.Errorf("partial result exceeds 16 KiB limit")
		}
		partial, err := compactResultJSONV2(record.PartialResult)
		if err != nil {
			return TaskResultV2{}, fmt.Errorf("validate partial result JSON: %w", err)
		}
		if len(partial) > taskResultInlineBytesV2 {
			return TaskResultV2{}, fmt.Errorf("partial result exceeds serialized inline limit")
		}
		result.PartialResult = partial
	}

	if result.Status == TaskSucceededV2 {
		if record.Validation != "valid" {
			result.Status = TaskFailedV2
			result.Result = nil
			result.ResultArtifactID = ""
			result.Error = &TaskErrorV2{Category: "output_invalid", Stage: "validation", Retry: "never"}
		} else {
			if result.Error != nil || len(record.PartialResult) != 0 || record.ResultArtifactID != "" {
				return TaskResultV2{}, fmt.Errorf("inconsistent successful execution record")
			}
			if len(record.Result) == 0 {
				return TaskResultV2{}, fmt.Errorf("successful execution has no result")
			}
			if !utf8.Valid(record.Result) || !json.Valid(record.Result) {
				return TaskResultV2{}, fmt.Errorf("successful execution result is not valid UTF-8 JSON")
			}
			inline, err := compactResultJSONV2(record.Result)
			if err != nil {
				return TaskResultV2{}, fmt.Errorf("encode successful result: %w", err)
			}
			if len(record.Result) > taskResultArtifactBytesV2 {
				return TaskResultV2{}, fmt.Errorf("accepted result exceeds 4 MiB artifact limit")
			}
			if len(record.Result) <= taskResultInlineBytesV2 && len(inline) <= taskResultInlineBytesV2 {
				result.Result = inline
			} else {
				artifactBytes := record.Result
				if len(record.Result) <= taskResultInlineBytesV2 {
					// The compact form is the JSON representation that would be
					// persisted inline. Preserve its value in the artifact so its
					// digest and size describe the bytes actually stored.
					artifactBytes = inline
				}
				if len(artifactBytes) <= taskResultInlineBytesV2 || len(artifactBytes) > taskResultArtifactBytesV2 {
					return TaskResultV2{}, fmt.Errorf("accepted result cannot fit bounded inline or artifact representation")
				}
				if sink == nil {
					return TaskResultV2{}, fmt.Errorf("artifact sink required for oversized result")
				}
				artifact, err := sink.PutArtifact(ctx, record.Owner, record.ExecutionID, append([]byte(nil), artifactBytes...), "application/json")
				if err != nil {
					return TaskResultV2{}, fmt.Errorf("store oversized task result: %w", err)
				}
				if err := validateBuiltArtifactV2(artifact, artifactBytes); err != nil {
					return TaskResultV2{}, err
				}
				result.ResultArtifactID = artifact.ID
				result.Artifacts = append(result.Artifacts, artifact)
			}
		}
	} else if len(record.Result) > 0 || record.ResultArtifactID != "" {
		return TaskResultV2{}, fmt.Errorf("failed execution record contains accepted result")
	}

	if err := ValidateTaskResultV2(result); err != nil {
		return TaskResultV2{}, fmt.Errorf("validate built task result: %w", err)
	}
	// Receipt persistence serializes RawMessage with HTML escaping. Ensure that
	// this exact serialized inline form remains under the contract byte limit.
	serialized, err := json.Marshal(result)
	if err != nil {
		return TaskResultV2{}, fmt.Errorf("encode built task result: %w", err)
	}
	var persisted TaskResultV2
	if err := json.Unmarshal(serialized, &persisted); err != nil {
		return TaskResultV2{}, fmt.Errorf("decode serialized task result: %w", err)
	}
	if err := ValidateTaskResultV2(persisted); err != nil {
		return TaskResultV2{}, fmt.Errorf("validate serialized task result: %w", err)
	}
	return persisted, nil
}

func compactResultJSONV2(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON")
	}
	return json.Marshal(json.RawMessage(raw))
}

func validateBuiltArtifactV2(artifact ArtifactV2, data []byte) error {
	sum := sha256.Sum256(data)
	if !validTaskIDV2(artifact.ID) || artifact.SHA256 != hex.EncodeToString(sum[:]) || artifact.Size != int64(len(data)) || artifact.MediaType != "application/json" {
		return fmt.Errorf("artifact sink returned inconsistent metadata")
	}
	if artifact.Size <= taskResultInlineBytesV2 || artifact.Size > taskResultArtifactBytesV2 {
		return fmt.Errorf("artifact sink returned size outside bounds")
	}
	if !utf8.Valid(data) || !json.Valid(data) {
		return fmt.Errorf("artifact payload is not valid UTF-8 JSON")
	}
	return nil
}

func truncateResultSummaryV2(summary string) string {
	if !utf8.ValidString(summary) {
		summary = strings.ToValidUTF8(summary, "\uFFFD")
	}
	if len(summary) <= taskResultSummaryBytesV2 {
		return summary
	}
	end := taskResultSummaryBytesV2
	for end > 0 && !utf8.ValidString(summary[:end]) {
		end--
	}
	return summary[:end]
}

func cloneResultUsageV2(usage core.RequestUsageV2) core.RequestUsageV2 {
	usage.InputTokens = cloneResultInt64V2(usage.InputTokens)
	usage.OutputTokens = cloneResultInt64V2(usage.OutputTokens)
	usage.TotalTokens = cloneResultInt64V2(usage.TotalTokens)
	usage.ReasoningTokens = cloneResultInt64V2(usage.ReasoningTokens)
	usage.CacheReadTokens = cloneResultInt64V2(usage.CacheReadTokens)
	usage.CacheWriteTokens = cloneResultInt64V2(usage.CacheWriteTokens)
	usage.BilledMicroUSD = cloneResultInt64V2(usage.BilledMicroUSD)
	return usage
}

func cloneResultBudgetV2(budget core.BudgetSnapshotV2) core.BudgetSnapshotV2 {
	budget.ReportedUsage = cloneResultUsageV2(budget.ReportedUsage)
	budget.ReservedMicroUSD = cloneResultInt64V2(budget.ReservedMicroUSD)
	budget.UnresolvedMicroUSD = cloneResultInt64V2(budget.UnresolvedMicroUSD)
	return budget
}

func cloneResultLimitsV2(limits core.LimitsV2) core.LimitsV2 {
	limits.ReserveMicroUSD = cloneResultInt64V2(limits.ReserveMicroUSD)
	return limits
}

func cloneResultInt64V2(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
