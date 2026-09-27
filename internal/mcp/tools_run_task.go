package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/sirerun/ferro"
)

// RunTaskArgs is run_task's MCP argument shape (exported so callers, e.g.
// cmd/ferro-mcp's tests, can decode a run_task result without re-declaring
// it).
type RunTaskArgs struct {
	Goal         string `json:"goal" jsonschema:"the task for the browser agent to accomplish"`
	StartURL     string `json:"start_url,omitempty" jsonschema:"optional URL to navigate to before planning; omit to act on the tab's current page"`
	MaxPlannings int    `json:"max_plannings,omitempty" jsonschema:"cap on replans within this call (ferro default 3); raise it for a goal needing several wait-and-recheck cycles, e.g. an AI reply that streams for a while or truncates and needs 'continue' clicked more than a couple of times"`
	ModelProfile string `json:"model_profile,omitempty" jsonschema:"optional configured model profile: legacy-mcp (default) or legacy-chat"`
}

// RunTaskOutput is run_task's result shape.
type RunTaskOutput struct {
	Result  any              `json:"result"`
	Metrics ferro.RunMetrics `json:"metrics"`
}

// runnerForTaskProfileV1 keeps MCP goal-level work on Ferro's deterministic
// plan executor while allowing callers to reuse the model configured in Chat.
func (o *Owner) runnerForTaskProfileV1(ctx context.Context, name string) (*ferro.Runner, error) {
	switch name {
	case "legacy-mcp":
		if o.cfg.LLMBaseURL == "" || o.cfg.LLMModel == "" {
			return nil, fmt.Errorf("run_task requires FERRO_MCP_LLM_BASE_URL and FERRO_MCP_LLM_MODEL; direct browser tools do not")
		}
		return o.runner, nil
	case "legacy-chat":
		client, err := o.taskProfileClientV1(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("run_task model profile %q is unavailable: %w", name, err)
		}
		opts := []ferro.Option{ferro.WithMaxRepairs(o.cfg.MaxRepairs)}
		if o.cfg.CachePath != "" {
			opts = append(opts, ferro.WithResolutionCache(o.cfg.CachePath))
		}
		return ferro.NewRunner(client, opts...), nil
	default:
		return nil, fmt.Errorf("model_profile must be legacy-mcp or legacy-chat")
	}
}

func (o *Owner) taskProfileClientV1(ctx context.Context, name string) (ferro.LLMClient, error) {
	if name != "legacy-chat" {
		return nil, fmt.Errorf("model profile must be legacy-chat")
	}
	profile, err := o.resolveTaskProfile(ctx, name)
	if err != nil {
		return nil, err
	}
	return &ferro.OpenAICompatible{BaseURL: profile.Endpoint, Model: profile.Model, APIKey: profile.Credential}, nil
}

// runTaskSimple handles the compact goal-only request shape.
func (o *Owner) runTaskSimple(ctx context.Context, args json.RawMessage) (any, error) {
	var in RunTaskArgs
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return nil, fmt.Errorf("decode run_task args: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode run_task args: multiple JSON values")
		}
		return nil, fmt.Errorf("decode run_task args: %w", err)
	}
	if in.Goal == "" {
		return nil, fmt.Errorf("goal is required")
	}

	// ADR 005: gate on Task.StartURL (or the tab's current origin, if
	// empty) before the first LLM planning call is made at all -- an
	// autonomous plan must never even get a snapshot of a non-allowlisted
	// origin, closing off the prompt-injection vector at the source rather
	// than only at the point of action.
	if err := o.checkOrigin(ctx, in.StartURL); err != nil {
		return nil, err
	}

	runner := o.runner
	driver := o.driver
	if chat, ok := ctx.Value(chatTaskKey{}).(chatTask); ok {
		runner = chat.runner
		if chat.readOnly {
			driver = readOnlyDriver{PageDriver: driver}
		}
	} else {
		profile := in.ModelProfile
		if profile == "" {
			profile = "legacy-mcp"
		}
		var err error
		runner, err = o.runnerForTaskProfileV1(ctx, profile)
		if err != nil {
			return nil, err
		}
	}
	o.snap = nil
	o.snapGeneration = 0
	runCtx, cancel := o.actionCtx(ctx)
	defer cancel()
	result, metrics, err := runner.RunDriver(runCtx, driver, o.cfg.MaxElements, ferro.Task{
		Goal:         in.Goal,
		StartURL:     in.StartURL,
		MaxPlannings: in.MaxPlannings,
	})
	if err != nil {
		return nil, err
	}
	return RunTaskOutput{Result: result, Metrics: metrics}, nil
}
