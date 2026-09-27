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

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirerun/ferro/internal/core"
	"github.com/sirerun/ferro/internal/llm"
)

// All transports currently authorize the same private installation owner.
// This is deliberately independent of ephemeral MCP client/lease identities.
// A multi-user deployment must replace this with authenticated account identity.
const privateReceiptOwner = "installation-owner"

func registerTaskTools(s *sdk.Server, c caller) {
	// The raw handler preserves the original wire bytes for the 64 KiB check.
	s.AddTool(&sdk.Tool{Name: "run_task", Description: "Run a natural-language browser task. Browser interaction is read/write by default: Ferro can navigate, click, type, select, and press keys on the paired tab. The goal, tab, origin allowlist, and execution budgets still constrain the work. Use task_id, policy.origins, output_schema, limits, and evidence for durable receipt recovery and bounded structured output; these are optional for the simple goal-only request. policy.mode can explicitly be read_only when a task must not change the page. Set model_profile to legacy-chat to use the provider and key saved in Ferro Chat settings.", Annotations: &sdk.ToolAnnotations{ReadOnlyHint: false}, InputSchema: json.RawMessage(`{"type":"object","required":["goal"],"properties":{"schema":{"type":"string","enum":["ferro.task/v2"]},"task_id":{"type":"string"},"goal":{"type":"string"},"start_url":{"type":"string"},"max_plannings":{"type":"integer","minimum":0},"model_profile":{"type":"string"},"policy":{"type":"object","required":["origins"],"properties":{"mode":{"type":"string","enum":["read_write","read_only"]},"origins":{"type":"array","items":{"type":"string"}}},"additionalProperties":false},"output_schema":{"type":"object"},"limits":{"type":"object"},"replay_key":{"type":"string"},"evidence":{"type":"string","enum":["compact","artifacts"]}},"additionalProperties":false}`)}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		ctx = context.WithValue(ctx, clientKey{}, req.Session.ID())
		text, failed, err := c.Call(ctx, "run_task", req.Params.Arguments)
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
	addRelayTool[receiptLookupArgs](s, &sdk.Tool{Name: "get_task_receipt", Description: "Recover this installation owner's durable receipt by task_id or execution_id, including after disconnect or restart. Does not execute browser work."}, c)
	addRelayTool[artifactReadArgs](s, &sdk.Tool{Name: "read_task_artifact", Description: "Read an owner-authorized artifact chunk (at most 65536 bytes). Data is base64 encoded."}, c)
	addRelayTool[snapshotArgs](s, &sdk.Tool{Name: "cleanup_task_receipts", Description: "Explicitly remove fully reconciled terminal receipts older than 30 days, preserving deduplication tombstones and uncertain work."}, c)
}

type receiptLookupArgs struct {
	TaskID      string `json:"task_id,omitempty"`
	ExecutionID string `json:"execution_id,omitempty"`
}
type artifactReadArgs struct {
	ExecutionID string `json:"execution_id"`
	ArtifactID  string `json:"artifact_id"`
	Offset      int64  `json:"offset,omitempty"`
	Limit       int64  `json:"limit,omitempty"`
}

func decodeTaskControl(raw []byte, value any) error {
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

func (o *Owner) taskControl(ctx context.Context, tool string, raw json.RawMessage) (any, error) {
	if tool == "list_model_profiles" {
		var out []ProfileDescription
		for _, name := range []string{"legacy-mcp", "legacy-chat"} {
			if p, err := o.resolveTaskProfile(ctx, name); err == nil {
				out = append(out, DescribeProfile(p.Profile))
			}
		}
		return map[string]any{"profiles": out}, nil
	}
	if o.receipts == nil {
		return nil, fmt.Errorf("receipt storage unavailable")
	}
	switch tool {
	case "get_task_receipt":
		var in receiptLookupArgs
		if err := decodeTaskControl(raw, &in); err != nil {
			return nil, err
		}
		if (in.TaskID == "") == (in.ExecutionID == "") {
			return nil, fmt.Errorf("supply exactly one of task_id or execution_id")
		}
		if in.TaskID != "" {
			return o.receipts.Lookup(ctx, privateReceiptOwner, in.TaskID)
		}
		return o.receipts.Get(ctx, privateReceiptOwner, in.ExecutionID)
	case "read_task_artifact":
		var in artifactReadArgs
		if err := decodeTaskControl(raw, &in); err != nil {
			return nil, err
		}
		if in.Limit == 0 {
			in.Limit = 65536
		}
		data, err := o.receipts.ReadArtifact(ctx, privateReceiptOwner, in.ExecutionID, in.ArtifactID, in.Offset, in.Limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"data": data, "offset": in.Offset, "bytes": len(data)}, nil
	case "cleanup_task_receipts":
		var in snapshotArgs
		if err := decodeTaskControl(raw, &in); err != nil {
			return nil, err
		}
		if err := o.receipts.Cleanup(ctx, time.Now().UTC()); err != nil {
			return nil, err
		}
		return map[string]string{"status": "complete"}, nil
	}
	return nil, fmt.Errorf("unknown receipt tool")
}

type configuredProfile struct {
	profile    Profile
	credential string
}

func (p configuredProfile) LoadProfile(_ context.Context, name string) (Profile, error) {
	if name != p.profile.Name {
		return Profile{}, fmt.Errorf("profile missing")
	}
	return p.profile, nil
}
func (p configuredProfile) ResolveCredential(_ context.Context, ref string) (string, error) {
	if ref != "configured-key" {
		return "", fmt.Errorf("credential missing")
	}
	return p.credential, nil
}
func (o *Owner) resolveTaskProfile(ctx context.Context, name string) (ResolvedProfile, error) {
	m := chatModel{BaseURL: o.cfg.LLMBaseURL, Model: o.cfg.LLMModel, APIKey: o.cfg.LLMAPIKey}
	if name == "legacy-chat" {
		var err error
		m, err = o.chatModel()
		if err != nil {
			return ResolvedProfile{}, fmt.Errorf("profile unavailable")
		}
	} else if name != "legacy-mcp" {
		return ResolvedProfile{}, fmt.Errorf("profile unavailable")
	}
	ref := ""
	if m.APIKey != "" {
		ref = "configured-key"
	}
	limits := core.DefaultLimits()
	// A deployment's transport deadline also bounds the published execution
	// profile, so receipts and profile revisions describe the real ceiling.
	if o.cfg.BlockTimeout > 0 && o.cfg.BlockTimeout.Milliseconds() < limits.RuntimeMS {
		limits.RuntimeMS = o.cfg.BlockTimeout.Milliseconds()
	}
	p, err := NewLegacyProfile(name, m.BaseURL, m.Model, ref, limits)
	if err != nil {
		return ResolvedProfile{}, fmt.Errorf("profile unavailable; configure the model first")
	}
	source := configuredProfile{p, m.APIKey}
	resolver, err := NewProfileResolver(source, source)
	if err != nil {
		return ResolvedProfile{}, err
	}
	return resolver.Resolve(ctx, name)
}

type executionGuard struct {
	owner      *Owner
	who        string
	generation uint64
	origins    []string
}

func (g executionGuard) Check(ctx context.Context) error {
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

func (o *Owner) runTaskWithReceipt(ctx context.Context, raw json.RawMessage) (any, error) {
	in, err := ValidateTaskRequest(raw)
	if err != nil {
		return nil, err
	}
	if o.receipts == nil {
		return nil, fmt.Errorf("receipt storage unavailable")
	}
	digest, err := canonicalRequestDigest(in)
	if err != nil {
		return nil, err
	}
	// Deduplication is resolved before configuration or browser availability.
	if previous, lookupErr := o.receipts.Lookup(ctx, privateReceiptOwner, in.TaskID); lookupErr == nil {
		if previous.RequestDigest != digest {
			return nil, ErrReceiptConflict
		}
		return receiptResponse(previous), nil
	} else if !errors.Is(lookupErr, ErrReceiptNotFound) {
		return nil, lookupErr
	}
	profile, err := o.resolveTaskProfile(ctx, in.ModelProfile)
	if err != nil {
		return nil, err
	}
	limits, err := in.Limits.Apply(profile.Profile.Limits)
	if err != nil {
		return nil, err
	}
	started := time.Now().UTC()
	budget, err := core.NewTaskBudget(limits, started)
	if err != nil {
		return nil, err
	}
	receipt, created, err := o.receipts.Admit(ctx, privateReceiptOwner, in, digest)
	if err != nil {
		return nil, err
	}
	if !created {
		return receiptResponse(receipt), nil
	}
	record := ExecutionRecord{Owner: privateReceiptOwner, TaskID: in.TaskID, ExecutionID: receipt.ExecutionID, Profile: profile.Profile.Name, ProfileRevision: profile.Profile.Revision, Model: profile.Model, Limits: limits, StartedAt: started, Status: TaskFailed, Validation: "not_run", SideEffectState: SideEffectNone}
	var sideEffects sideEffectTracker
	runCtx, actionCancel := o.actionCtx(ctx)
	defer actionCancel()
	runCtx, cancel := context.WithDeadline(runCtx, started.Add(time.Duration(limits.RuntimeMS)*time.Millisecond))
	defer cancel()
	generation := uint64(0)
	if o.bridge != nil {
		generation = o.bridge.Generation()
		runCtx = o.bridge.Pin(runCtx)
	}
	guard := executionGuard{o, clientIdentity(ctx), generation, in.Policy.Origins}
	execute := func() (any, error) {
		if err := guard.Check(runCtx); err != nil {
			return nil, err
		}
		driver, err := NewTaskPolicyDriver(o.driver, *in.Policy, guard, budget, func(state SideEffectState, completed bool) {
			sideEffects.observe(state, completed)
		})
		if err != nil {
			return nil, err
		}
		client := &llm.OpenAICompatible{BaseURL: profile.Endpoint, Model: profile.Model, APIKey: profile.Credential, MaxTokens: int(limits.MaxOutputTokens)}
		bounded, err := core.NewBudgetedClient(client, budget, limits)
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
			schemaBytes, err := canonicalJSONBytes(in.OutputSchema)
			if err != nil {
				return nil, err
			}
			layoutBytes, err := json.Marshal(snapshot)
			if err != nil {
				return nil, err
			}
			replayKey, err = core.ReplayIdentity(core.ReplayContext{Principal: fmt.Sprintf("%s/%s/%d", privateReceiptOwner, o.replayEpoch, generation), ProfileRevision: profile.Profile.Revision, Model: profile.Model, PolicyDigest: digestBytes(policyBytes), SchemaDigest: digestBytes(schemaBytes), Compatibility: "read-only-v2.1", Layout: digestBytes(layoutBytes), CallerLabel: in.ReplayLabel})
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
		runErr = core.ValidateResult(in.OutputSchema, value)
		if runErr == nil {
			record.Result, runErr = json.Marshal(value)
		}
		if runErr == nil {
			record.Status = TaskSucceeded
			record.Validation = "valid"
		}
	}
	if runErr != nil {
		record.Status, record.Error, record.SideEffectState = classifyTaskFailure(runErr)
		record.SideEffectState = mergeSideEffectState(record.SideEffectState, sideEffects.state())
		record.Validation = "invalid"
		record.Result = nil
	} else {
		record.SideEffectState = mergeSideEffectState(record.SideEffectState, sideEffects.state())
	}
	record.EndedAt = time.Now().UTC()
	record.Budget = budget.Snapshot()
	record.Usage = record.Budget.ReportedUsage
	// Caller cancellation must not discard the durable outcome.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer finishCancel()
	result, err := BuildTaskResult(finishCtx, record, o.receipts)
	if err != nil {
		record.Status = TaskFailed
		record.Validation = "invalid"
		record.Result = nil
		record.PartialResult = nil
		record.ResultArtifactID = ""
		record.Artifacts = nil
		record.Error = &TaskError{Category: "storage_error", Stage: "finalize", Retry: "reconcile_only", Detail: "result could not be stored"}
		result, err = BuildTaskResult(finishCtx, record, o.receipts)
		if err != nil {
			return nil, fmt.Errorf("storage_error: inspect receipt before retrying")
		}
	}
	if err = o.receipts.Finalize(finishCtx, privateReceiptOwner, receipt.ExecutionID, result); err != nil {
		return nil, fmt.Errorf("storage_error: inspect receipt before retrying")
	}
	return result, nil
}

func receiptResponse(r Receipt) any {
	if r.Result != nil {
		return *r.Result
	}
	return r
}
func digestBytes(b []byte) string { d := sha256.Sum256(b); return hex.EncodeToString(d[:]) }
func classifyTaskFailure(err error) (TaskStatus, *TaskError, SideEffectState) {
	status := TaskFailed
	e := &TaskError{Category: "output_invalid", Stage: "execution", Retry: "never", Detail: "task could not produce valid output"}
	effects := SideEffectNone
	switch {
	case errors.Is(err, context.Canceled):
		status = TaskCancelled
		e.Category = "deadline_exceeded"
		e.Detail = "task cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		status = TaskBudgetExhausted
		e.Category = "deadline_exceeded"
		e.Detail = "task deadline reached"
	case errors.Is(err, core.ErrBudgetExhausted):
		status = TaskBudgetExhausted
		e = nil
	default:
		var stopped *core.StopError
		if errors.As(err, &stopped) {
			e.Category = stopped.Code
			e.Detail = "browser or provider stopped the task"
			status = TaskBlocked
			if !stableErrorCategories[e.Category] {
				e.Category = "capability_unsupported"
			}
			if stopped.Code == "provider_error" {
				status = TaskFailed
			}
			if stopped.Code == "pairing_changed" || stopped.Code == "disconnected" || stopped.Code == "outcome_uncertain" {
				status = TaskOutcomeUncertain
				effects = SideEffectUnknown
				e.Retry = "reconcile_only"
			}
		}
	}
	return status, e, effects
}

func mergeSideEffectState(a, b SideEffectState) SideEffectState {
	if a == SideEffectUnknown || b == SideEffectUnknown {
		return SideEffectUnknown
	}
	if a == SideEffectConfirmed || b == SideEffectConfirmed {
		return SideEffectConfirmed
	}
	return SideEffectNone
}

type sideEffectTracker struct {
	pending   bool
	unknown   bool
	confirmed bool
}

func (t *sideEffectTracker) observe(state SideEffectState, completed bool) {
	if !completed {
		t.pending = true
		return
	}
	t.pending = false
	if state == SideEffectUnknown {
		t.unknown = true
	} else if state == SideEffectConfirmed {
		t.confirmed = true
	}
}

func (t sideEffectTracker) state() SideEffectState {
	if t.pending || t.unknown {
		return SideEffectUnknown
	}
	if t.confirmed {
		return SideEffectConfirmed
	}
	return SideEffectNone
}

func (o *Owner) initializeTasks() error {
	store, err := OpenReceiptStore(filepath.Join(o.cfg.Home, "tasks-v2"), 256<<20)
	if err != nil {
		return fmt.Errorf("open task receipt store: %w", err)
	}
	o.receipts = store
	o.taskCaches = map[string]*core.ResolutionCache{}
	o.replayEpoch = fmt.Sprintf("%d", time.Now().UnixNano())
	return nil
}
