# ADR 002: Planner output is schema-validated, not prompt-disciplined

## Status
Accepted

## Date
2026-09-04

## Context
The planner prompt in `internal/core/runner.go` asks the model for one
exact JSON envelope (`{"steps": [...]}` with flat step objects). Bug #4
showed the model drifting the envelope (steps nested under the kind name,
a wrapping `plan` key, `actions` instead of `steps`). `parsePlan` accepted
the JSON, `Plan.Validate` then reported "plan has no steps", and the run
failed with a message that did not name the real problem. Prompt text is
not a contract: every model reads it a little differently.

## Decision
- The plan shape is expressed once as a JSON Schema constant in
  `internal/core` and used in two ways:
  1. Sent to the endpoint as `response_format: {"type": "json_schema",
     "json_schema": {..., "strict": true}}` when the client supports it.
     The `LLMClient` interface gains an optional capability interface
     (`SchemaCompleter`) so backends that cannot enforce a schema still
     work through the plain `Complete` path.
  2. Applied locally to every planner and repair response before
     `json.Unmarshal`, regardless of backend. A response that fails the
     schema produces a typed `ErrPlanShape` error naming the offending
     path, and counts as one planner retry (bounded, default 1) with the
     validator's message appended to the prompt.
- Silent tolerance is limited to the two existing cases (markdown fences,
  leading prose). No further "repair the envelope" heuristics.

## Consequences
- Positive: envelope drift fails loudly with a precise message, is
  measurable in `RunMetrics`, and endpoints with structured output enforce
  it server-side at zero prompt cost.
- Negative: one more dependency decision (a JSON Schema validator) or a
  hand-written validator for the small fixed schema. The plan chooses a
  hand-written validator to stay standard-library only, per the Go
  conventions in use.
