package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dndungu/ferro"
)

// RunTaskArgs is run_task's MCP argument shape (exported so callers, e.g.
// cmd/ferro-mcp's tests, can decode a run_task result without re-declaring
// it).
type RunTaskArgs struct {
	Goal         string `json:"goal" jsonschema:"the task for the browser agent to accomplish"`
	StartURL     string `json:"start_url,omitempty" jsonschema:"optional URL to navigate to before planning; omit to act on the tab's current page"`
	MaxPlannings int    `json:"max_plannings,omitempty" jsonschema:"cap on replans within this call (ferro default 3); raise it for a goal needing several wait-and-recheck cycles, e.g. an AI reply that streams for a while or truncates and needs 'continue' clicked more than a couple of times"`
}

// RunTaskOutput is run_task's result shape.
type RunTaskOutput struct {
	Result  any              `json:"result"`
	Metrics ferro.RunMetrics `json:"metrics"`
}

func registerRunTaskTool(server *sdk.Server, c caller) {
	addRelayTool[RunTaskArgs](server, &sdk.Tool{
		Name: "run_task",
		Description: "Drive the shared browser tab toward a natural-language goal. " +
			"ferro asks the model once for a declarative plan, then executes it " +
			"deterministically; the model is consulted again only to repair a " +
			"failed step or replan. The tab persists across calls, so a goal can " +
			"continue where the previous one left off (e.g. the next turn of a chat). " +
			"Gated by the origin allowlist: the goal's start_url (or the tab's " +
			"current page, if omitted) must be allowlisted before planning begins.",
	}, c)
}

// runTask is the Owner-side implementation dispatched to by Owner.dispatch.
func (o *Owner) runTask(ctx context.Context, args json.RawMessage) (any, error) {
	var in RunTaskArgs
	if err := json.Unmarshal(args, &in); err != nil {
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
	} else if o.cfg.LLMBaseURL == "" || o.cfg.LLMModel == "" {
		return nil, fmt.Errorf("run_task requires FERRO_MCP_LLM_BASE_URL and FERRO_MCP_LLM_MODEL; direct browser tools do not")
	}
	o.snap = nil
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
