# ADR 011: Unversioned read/write MCP task tool

Status: accepted. Date: 2026-09-24. Supersedes the public-tool separation in ADR 009.

## Decision

Expose one goal-level browser task tool named `run_task`. It accepts the
existing simple goal request and optional receipt-backed fields such as
`task_id`, exact `policy.origins`, `output_schema`, limits, and evidence mode.
The simple request remains compatible. Receipt-backed requests use the same
tool name and recover through unversioned receipt and artifact tools.

Browser work is read/write by default. An explicit `read_only` policy remains
available for a task whose goal requires no page changes. All MCP tools are
advertised with `readOnlyHint: false`, so clients do not infer that the MCP
surface is read-only. Existing origin checks, execution budgets, tab
serialization, and receipt deduplication remain in force.

The configured `legacy-mcp` model remains the default. Callers may select
`legacy-chat` to reuse the model and OpenRouter-compatible endpoint saved in
Ferro Chat. The model plans compact browser actions; MCP callers provide the
higher-level goal and receive the task result without relaying each click and
intermediate page snapshot.

## Consequences

The MCP tool list exposes only `run_task` for goal-level browser tasks. Go
symbols and source filenames for task budgets, profiles, policies, receipts,
results, and runtime use unversioned names. The serialized receipt schema and
existing on-disk receipt paths retain their version marker so previously saved
work remains readable. ADR 009's receipt, usage, schema, and retry protections
remain implementation constraints except for its read-only-by-default tool
separation. Current operator instructions live in `docs/bulk-browser-operator.md`.
