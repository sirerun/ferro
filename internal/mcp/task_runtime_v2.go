package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/dndungu/ferro/internal/core"
	"github.com/dndungu/ferro/internal/llm"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// All transports currently authorize the same private installation owner.
// This is deliberately independent of ephemeral MCP client/lease identities.
// A multi-user deployment must replace this with authenticated account identity.
const privateReceiptOwnerV2 = "installation-owner"

func registerTaskToolsV2(s *sdk.Server, c caller) {
	// The raw handler preserves the original wire bytes for the 64 KiB check.
	s.AddTool(&sdk.Tool{Name: "run_task_v2", Description: "Run a bounded read-only browser task. Requires a unique task_id, configured model_profile, exact allowed origins and output_schema. Repeating the same task_id returns its receipt without redispatch. Inspect receipts before retrying uncertain work.", InputSchema: json.RawMessage(`{"type":"object","required":["schema","task_id","goal","model_profile","policy","output_schema"],"properties":{"schema":{"type":"string","enum":["ferro.task/v2"]},"task_id":{"type":"string"},"goal":{"type":"string"},"start_url":{"type":"string"},"model_profile":{"type":"string"},"policy":{"type":"object","required":["mode","origins"],"properties":{"mode":{"type":"string","enum":["read_only"]},"origins":{"type":"array","items":{"type":"string"}}},"additionalProperties":false},"output_schema":{"type":"object"},"limits":{"type":"object"},"replay_key":{"type":"string"},"evidence":{"type":"string","enum":["compact","artifacts"]}},"additionalProperties":false}`)}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		ctx = context.WithValue(ctx, clientKey{}, req.Session.ID())
		text, failed, err := c.Call(ctx, "run_task_v2", req.Params.Arguments)
		if err != nil {
			return nil, err
		}
		result := &sdk.CallToolResult{IsError: failed, Content: []sdk.Content{&sdk.TextContent{Text: text}}}
		if !failed {
			var value any
			if json.Unmarshal([]byte(text), &value) == nil {
				result.StructuredContent = value
			}
		}
		return result, nil
	})
	addRelayTool[snapshotArgs](s, &sdk.Tool{Name: "list_model_profiles", Description: "List configured immutable model profile names and revisions; credentials are never returned."}, c)
	addRelayTool[receiptLookupArgsV2](s, &sdk.Tool{Name: "get_task_receipt", Description: "Recover this installation owner's durable receipt by task_id or execution_id, including after disconnect or restart. Does not execute browser work."}, c)
	addRelayTool[artifactReadArgsV2](s, &sdk.Tool{Name: "read_task_artifact", Description: "Read an owner-authorized artifact chunk (at most 65536 bytes). Data is base64 encoded."}, c)
	addRelayTool[snapshotArgs](s, &sdk.Tool{Name: "cleanup_task_receipts", Description: "Explicitly remove fully reconciled terminal receipts older than 30 days, preserving deduplication tombstones and uncertain work."}, c)
}

type receiptLookupArgsV2 struct {
	TaskID      string `json:"task_id,omitempty"`
	ExecutionID string `json:"execution_id,omitempty"`
}
type artifactReadArgsV2 struct {
	ExecutionID string `json:"execution_id"`
	ArtifactID  string `json:"artifact_id"`
	Offset      int64  `json:"offset,omitempty"`
	Limit       int64  `json:"limit,omitempty"`
}

func decodeTaskControlV2(raw []byte, value any) error {
	if len(raw) > 65536 {
		return fmt.Errorf("request exceeds 65536 bytes")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return fmt.Errorf("invalid request")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("invalid trailing data")
	}
	return nil
}

func (o *Owner) taskControlV2(ctx context.Context, tool string, raw json.RawMessage) (any, error) {
	if tool == "list_model_profiles" {
		var out []ProfileDescriptionV2
		for _, name := range []string{"legacy-mcp", "legacy-chat"} {
			if p, err := o.resolveTaskProfileV2(ctx, name); err == nil {
				out = append(out, DescribeProfileV2(p.Profile))
			}
		}
		return map[string]any{"profiles": out}, nil
	}
	if o.receipts == nil {
		return nil, fmt.Errorf("receipt storage unavailable")
	}
	switch tool {
	case "get_task_receipt":
		var in receiptLookupArgsV2
		if err := decodeTaskControlV2(raw, &in); err != nil {
			return nil, err
		}
		if (in.TaskID == "") == (in.ExecutionID == "") {
			return nil, fmt.Errorf("supply exactly one of task_id or execution_id")
		}
		if in.TaskID != "" {
			return o.receipts.Lookup(ctx, privateReceiptOwnerV2, in.TaskID)
		}
		return o.receipts.Get(ctx, privateReceiptOwnerV2, in.ExecutionID)
	case "read_task_artifact":
		var in artifactReadArgsV2
		if err := decodeTaskControlV2(raw, &in); err != nil {
			return nil, err
		}
		if in.Limit == 0 {
			in.Limit = 65536
		}
		data, err := o.receipts.ReadArtifact(ctx, privateReceiptOwnerV2, in.ExecutionID, in.ArtifactID, in.Offset, in.Limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"data": data, "offset": in.Offset, "bytes": len(data)}, nil
	case "cleanup_task_receipts":
		var in snapshotArgs
		if err := decodeTaskControlV2(raw, &in); err != nil {
			return nil, err
		}
		if err := o.receipts.Cleanup(ctx, time.Now().UTC()); err != nil {
			return nil, err
		}
		return map[string]string{"status": "complete"}, nil
	}
	return nil, fmt.Errorf("unknown receipt tool")
}

type configuredProfileV2 struct {
	profile    ProfileV2
	credential string
}

func (p configuredProfileV2) LoadProfile(_ context.Context, name string) (ProfileV2, error) {
	if name != p.profile.Name {
		return ProfileV2{}, fmt.Errorf("profile missing")
	}
	return p.profile, nil
}
func (p configuredProfileV2) ResolveCredential(_ context.Context, ref string) (string, error) {
	if ref != "configured-key" {
		return "", fmt.Errorf("credential missing")
	}
	return p.credential, nil
}
func (o *Owner) resolveTaskProfileV2(ctx context.Context, name string) (ResolvedProfileV2, error) {
	m := chatModel{BaseURL: o.cfg.LLMBaseURL, Model: o.cfg.LLMModel, APIKey: o.cfg.LLMAPIKey}
	if name == "legacy-chat" {
		var err error
		m, err = o.chatModel()
		if err != nil {
			return ResolvedProfileV2{}, fmt.Errorf("profile unavailable")
		}
	} else if name != "legacy-mcp" {
		return ResolvedProfileV2{}, fmt.Errorf("profile unavailable")
	}
	ref := ""
	if m.APIKey != "" {
		ref = "configured-key"
	}
	limits := core.DefaultLimitsV2()
	// A deployment's transport deadline also bounds the published execution
	// profile, so receipts and profile revisions describe the real ceiling.
	if o.cfg.BlockTimeout > 0 && o.cfg.BlockTimeout.Milliseconds() < limits.RuntimeMS {
		limits.RuntimeMS = o.cfg.BlockTimeout.Milliseconds()
	}
	p, err := NewLegacyProfileV2(name, m.BaseURL, m.Model, ref, limits)
	if err != nil {
		return ResolvedProfileV2{}, fmt.Errorf("profile unavailable; configure the model first")
	}
	source := configuredProfileV2{p, m.APIKey}
	resolver, err := NewProfileResolverV2(source, source)
	if err != nil {
		return ResolvedProfileV2{}, err
	}
	return resolver.Resolve(ctx, name)
}

type executionGuardV2 struct {
	owner      *Owner
	who        string
	generation uint64
	origins    []string
}

func (g executionGuardV2) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o := g.owner
	o.mu.Lock()
	leased := o.leaseOwner
	until := o.leaseUntil
	o.mu.Unlock()
	if leased != "" && (leased != g.who || !time.Now().Before(until)) {
		return &core.StopError{Code: "tab_busy", Message: "tab lease changed or expired"}
	}
	if o.bridge != nil {
		if o.bridge.Generation() != g.generation {
			return &core.StopError{Code: "pairing_changed", Message: "tab pairing changed"}
		}
		if !o.bridge.Connected() {
			return &core.StopError{Code: "disconnected", Message: "tab disconnected"}
		}
	}
	for _, origin := range g.origins {
		if err := o.allow.Check(origin); err != nil {
			return &core.StopError{Code: "origin_denied", Message: "service origin permission revoked"}
		}
	}
	return nil
}

func (o *Owner) runTaskV2(ctx context.Context, raw json.RawMessage) (any, error) {
	in, err := ValidateTaskRequestV2(raw)
	if err != nil {
		return nil, err
	}
	if o.receipts == nil {
		return nil, fmt.Errorf("receipt storage unavailable")
	}
	digest, err := canonicalRequestDigestV2(in)
	if err != nil {
		return nil, err
	}
	// Deduplication is resolved before configuration or browser availability.
	if previous, lookupErr := o.receipts.Lookup(ctx, privateReceiptOwnerV2, in.TaskID); lookupErr == nil {
		if previous.RequestDigest != digest {
			return nil, ErrReceiptConflictV2
		}
		return receiptResponseV2(previous), nil
	} else if !errors.Is(lookupErr, ErrReceiptNotFoundV2) {
		return nil, lookupErr
	}
	profile, err := o.resolveTaskProfileV2(ctx, in.ModelProfile)
	if err != nil {
		return nil, err
	}
	limits, err := in.Limits.ApplyV2(profile.Profile.Limits)
	if err != nil {
		return nil, err
	}
	started := time.Now().UTC()
	budget, err := core.NewTaskBudgetV2(limits, started)
	if err != nil {
		return nil, err
	}
	receipt, created, err := o.receipts.Admit(ctx, privateReceiptOwnerV2, in, digest)
	if err != nil {
		return nil, err
	}
	if !created {
		return receiptResponseV2(receipt), nil
	}
	record := ExecutionRecordV2{Owner: privateReceiptOwnerV2, TaskID: in.TaskID, ExecutionID: receipt.ExecutionID, Profile: profile.Profile.Name, ProfileRevision: profile.Profile.Revision, Model: profile.Model, Limits: limits, StartedAt: started, Status: TaskFailedV2, Validation: "not_run", SideEffectState: SideEffectNoneV2}
	runCtx, actionCancel := o.actionCtx(ctx)
	defer actionCancel()
	runCtx, cancel := context.WithDeadline(runCtx, started.Add(time.Duration(limits.RuntimeMS)*time.Millisecond))
	defer cancel()
	generation := uint64(0)
	if o.bridge != nil {
		generation = o.bridge.Generation()
		runCtx = o.bridge.Pin(runCtx)
	}
	guard := executionGuardV2{o, clientIdentity(ctx), generation, in.Policy.Origins}
	execute := func() (any, error) {
		if err := guard.Check(runCtx); err != nil {
			return nil, err
		}
		driver, err := NewTaskPolicyDriverV2(o.driver, *in.Policy, guard, budget)
		if err != nil {
			return nil, err
		}
		client := &llm.OpenAICompatible{BaseURL: profile.Endpoint, Model: profile.Model, APIKey: profile.Credential, MaxTokens: int(limits.MaxOutputTokens)}
		bounded, err := core.NewBudgetedClientV2(client, budget, limits)
		if err != nil {
			return nil, err
		}
		cache := core.NewResolutionCache("")
		replayKey := ""
		if in.ReplayLabel != "" {
			// Navigate before fingerprinting so the identity describes the execution page.
			if in.StartURL != "" {
				if err := driver.Navigate(runCtx, in.StartURL); err != nil {
					return nil, err
				}
			}
			snapshot, err := driver.Snapshot(runCtx, o.cfg.MaxElements)
			if err != nil {
				return nil, err
			}
			policyBytes, err := json.Marshal(in.Policy)
			if err != nil {
				return nil, err
			}
			schemaBytes, err := canonicalJSONBytesV2(in.OutputSchema)
			if err != nil {
				return nil, err
			}
			layoutBytes, err := json.Marshal(snapshot)
			if err != nil {
				return nil, err
			}
			replayKey, err = core.ReplayIdentityV2(core.ReplayContextV2{Principal: fmt.Sprintf("%s/%s/%d", privateReceiptOwnerV2, o.replayEpoch, generation), ProfileRevision: profile.Profile.Revision, Model: profile.Model, PolicyDigest: digestBytesV2(policyBytes), SchemaDigest: digestBytesV2(schemaBytes), Compatibility: "read-only-v2.1", Layout: digestBytesV2(layoutBytes), CallerLabel: in.ReplayLabel})
			if err != nil {
				return nil, err
			}
			if cached := o.taskCaches[replayKey]; cached != nil {
				cache = cached
			} else {
				if len(o.taskCaches) >= 32 {
					o.taskCaches = map[string]*core.ResolutionCache{}
				}
				o.taskCaches[replayKey] = cache
			}
		}
		runner := &core.Runner{LLM: bounded, MaxRepairs: int(limits.Repairs), Executor: core.NewExecutor(core.WaitStrategy{}).WithCache(cache)}
		startURL := in.StartURL
		if in.ReplayLabel != "" {
			startURL = ""
		}
		value, _, err := runner.RunDriver(runCtx, driver, o.cfg.MaxElements, core.Task{Goal: in.Goal, StartURL: startURL, Schema: in.OutputSchema, ReplayKey: replayKey, MaxPlannings: int(limits.PlanningPasses), FreshReplayOnly: true})
		if err == nil {
			err = guard.Check(runCtx)
		}
		return value, err
	}
	o.snap = nil
	o.snapGeneration = 0
	value, runErr := execute()
	if runErr == nil {
		runErr = core.ValidateResultV2(in.OutputSchema, value)
		if runErr == nil {
			record.Result, runErr = json.Marshal(value)
		}
		if runErr == nil {
			record.Status = TaskSucceededV2
			record.Validation = "valid"
		}
	}
	if runErr != nil {
		record.Status, record.Error, record.SideEffectState = classifyTaskFailureV2(runErr)
		record.Validation = "invalid"
		record.Result = nil
	}
	record.EndedAt = time.Now().UTC()
	record.Budget = budget.Snapshot()
	record.Usage = record.Budget.ReportedUsage
	// Caller cancellation must not discard the durable outcome.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	result, err := BuildTaskResultV2(finishCtx, record, o.receipts)
	if err != nil {
		record.Status = TaskFailedV2
		record.Validation = "invalid"
		record.Result = nil
		record.PartialResult = nil
		record.ResultArtifactID = ""
		record.Artifacts = nil
		record.Error = &TaskErrorV2{Category: "storage_error", Stage: "finalize", Retry: "reconcile_only", Detail: "result could not be stored"}
		result, err = BuildTaskResultV2(finishCtx, record, o.receipts)
		if err != nil {
			return nil, fmt.Errorf("storage_error: inspect receipt before retrying")
		}
	}
	if err = o.receipts.Finalize(finishCtx, privateReceiptOwnerV2, receipt.ExecutionID, result); err != nil {
		return nil, fmt.Errorf("storage_error: inspect receipt before retrying")
	}
	return result, nil
}

func receiptResponseV2(r ReceiptV2) any {
	if r.Result != nil {
		return *r.Result
	}
	return r
}
func digestBytesV2(b []byte) string { d := sha256.Sum256(b); return hex.EncodeToString(d[:]) }
func classifyTaskFailureV2(err error) (TaskStatusV2, *TaskErrorV2, SideEffectStateV2) {
	status := TaskFailedV2
	e := &TaskErrorV2{Category: "output_invalid", Stage: "execution", Retry: "never", Detail: "task could not produce valid output"}
	effects := SideEffectNoneV2
	switch {
	case errors.Is(err, context.Canceled):
		status = TaskCancelledV2
		e.Category = "deadline_exceeded"
		e.Detail = "task cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		status = TaskBudgetExhaustedV2
		e.Category = "deadline_exceeded"
		e.Detail = "task deadline reached"
	case errors.Is(err, core.ErrBudgetExhaustedV2):
		status = TaskBudgetExhaustedV2
		e = nil
	default:
		var stopped *core.StopError
		if errors.As(err, &stopped) {
			e.Category = stopped.Code
			e.Detail = "browser or provider stopped the task"
			status = TaskBlockedV2
			if !stableErrorCategoriesV2[e.Category] {
				e.Category = "capability_unsupported"
			}
			if stopped.Code == "provider_error" {
				status = TaskFailedV2
			}
			if stopped.Code == "pairing_changed" || stopped.Code == "disconnected" || stopped.Code == "outcome_uncertain" {
				status = TaskOutcomeUncertainV2
				effects = SideEffectUnknownV2
				e.Retry = "reconcile_only"
			}
		}
	}
	return status, e, effects
}

func (o *Owner) initializeTasksV2() error {
	store, err := OpenReceiptStoreV2(filepath.Join(o.cfg.Home, "tasks-v2"), 256<<20)
	if err != nil {
		return fmt.Errorf("open task receipt store: %w", err)
	}
	o.receipts = store
	o.taskCaches = map[string]*core.ResolutionCache{}
	o.replayEpoch = fmt.Sprintf("%d", time.Now().UnixNano())
	return nil
}
