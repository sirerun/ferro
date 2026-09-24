# Bounded read-only task operator guide

This guide describes the additive `ferro.task/v2` MCP tools. The existing `run_task` tool and direct browser tools keep their legacy wire behavior. The v2 task tools are local, bounded, read-only execution; they are not a general browser scripting interface or a hosted multi-tenant service. See the [frozen contract](contracts/bulk-v2.md) for field-level rules and the [accepted decision](adr/009-bounded-task-contract.md) for design boundaries.

**Qualification status:** the contract and implementation are under fixture qualification. A passing contract/example check is not evidence that a browser installation, live site, account, provider, or deployment has been qualified. Do not treat these examples as proof of an installed or live workflow.

## Tools and lifecycle

Use `list_model_profiles` to see the configured profile names and immutable revisions available to this installation. It returns public descriptions and read-only capability; it does not return endpoints, credential references, or credential values. The initial runtime exposes the compatibility names `legacy-mcp` and `legacy-chat`, when each can be resolved. `legacy-mcp` uses the existing MCP model settings from `FERRO_MCP_LLM_BASE_URL`, `FERRO_MCP_LLM_MODEL`, and optional `FERRO_MCP_LLM_API_KEY`. `legacy-chat` uses the saved chat model in the private `chat-model.json` under the configured Ferro home; when that file is absent, it uses the same MCP settings. These are mappings to existing settings, not arbitrary per-request profiles or automatic model selection. Configure the existing settings through the supported installation process; never place a key in a task request or example.

Submit one `run_task_v2` request with a unique `task_id`, a concise goal, a configured `model_profile`, an output schema, and an explicit read-only policy with exact allowed origins. The optional `start_url` must fall within those origins. Optional limits can tighten the service limits; they cannot raise them. `replay_key` is only a caller label, not authorization or a guarantee that a cached result will be reused. Omitted evidence mode means `compact`; use `artifacts` when you need retrievable large evidence or output.

V2 allows navigation, snapshots, extraction, scrolling, waiting, and settling. Click, fill, select, and key actions are denied, including if suggested by an initial, repaired, or replayed plan. Origin checks constrain Ferro's browser operations; they do not claim network-level egress confinement. A task holds the installation's browser/tab lease while it runs. The lease and pairing generation are transient execution authority; the stable receipt owner is the private installation owner. A client/session identifier is not a durable receipt identity and is not an authorization credential.

Each model request is admitted against the request, token, runtime, repair, and planning limits. Calls have no automatic provider retry. Actual usage and transmission state are recorded separately: a missing usage number means **unknown**, while an explicit `0` means the provider reported zero. A missing cost is not a zero-cost assertion. If cost reporting is unavailable, the receipt keeps it unknown; do not derive cost from text length or infer a dollar amount from token counts. The implementation does not enforce a monetary reserve or hard-dollar ceiling.

## Receipts, retries, and restart recovery

The receipt is persisted before browser work is dispatched. If the initial response is lost, recover it with `get_task_receipt` using the original `task_id`; this lookup works even when the caller never received an `execution_id`. If you received an execution ID, lookup by that ID is also supported. Repeating a task ID with the same canonical request returns the existing receipt without redispatch. Reusing the ID with changed input returns a conflict.

After a process restart, an admitted/running operation whose completion cannot be established is reconciled as `outcome_uncertain`. Ferro does not automatically rerun it. The caller or external orchestrator owns retry scheduling: first inspect the receipt; retry only when the recorded outcome and `retry` policy establish that execution is safe. For uncertain work, `reconcile_only` means recover and reconcile the existing operation, not submit it again. This keeps orchestration policy outside the browser runtime; an external scheduler may decide how to schedule later work, but must follow the receipt and retry state.

Terminal result statuses distinguish success, failure, blocked, cancelled, budget exhaustion, and uncertain outcome. Successful output is schema-validated and appears in exactly one accepted location: inline `result` up to 16 KiB, or `result_artifact_id` for a larger result within the 4 MiB artifact bound. `partial_result` is never accepted output. Large artifacts are described by an ID, media type, size, and SHA-256 digest. Fetch them in chunks of at most 64 KiB with `read_task_artifact`; its `data` value is base64 encoded. Decode chunks and verify the complete byte count and digest before using them as a complete artifact. Do not mistake an inline summary or preview for the full output.

`cleanup_task_receipts` is explicit operator cleanup. It removes only fully reconciled terminal receipts older than 30 days and preserves deduplication tombstones. It does not clear active or unreconciled work. Storage is bounded; when capacity is exhausted, new admissions fail closed instead of evicting active receipts or artifacts. A second process cannot open the same receipt store concurrently.

## Request and result examples

The [read-only request](examples/bulk-v2/read-only-request.json) demonstrates a compact title extraction. The [blocked request](examples/bulk-v2/blocked-request.json) is valid input whose execution may be blocked by the current service origin allowlist or browser state; an example request cannot promise that a particular installation will allow it. A denied task has no accepted result, as shown in the [blocked result shape](examples/bulk-v2/blocked-result.json). The [budget request](examples/bulk-v2/budget-request.json) tightens the action limit; if the run cannot proceed within its budget, inspect its terminal receipt and do not treat a missing result as success.

For large output, the [artifact request](examples/bulk-v2/artifact-request.json) selects artifact evidence. The [artifact result shape](examples/bulk-v2/artifact-result.json) illustrates an accepted artifact-backed result, and [artifact read arguments](examples/bulk-v2/read-artifact-arguments.json) show a bounded chunk request. These examples show the frozen envelope and tool arguments; they do not contain a real provider response or claim that an artifact exists in an installation.

## Migration checklist

1. Keep existing `run_task`, chat, and direct-tool clients unchanged while adopting the additive v2 tools.
2. Configure and verify the intended existing `legacy-mcp` or `legacy-chat` model settings; use `list_model_profiles` to record the public profile revision. Do not copy credentials into requests, logs, or examples.
3. Start with a unique task ID, exact origins, a restrictive output schema, and limits no higher than the service defaults. Preserve the original request for conflict-safe recovery.
4. Add receipt lookup by `task_id` before any caller retry path. Make the external scheduler own retry timing; never automatically resubmit uncertain work.
5. Handle `result` and `result_artifact_id` as mutually exclusive accepted outputs. Add bounded artifact chunk retrieval and digest verification before enabling artifact evidence.
6. Record usage fields with presence semantics: absent/null is unknown, explicit zero is zero. Do not assume a hard-dollar ceiling.
7. Treat fixture checks as contract checks only. Complete the separate fixture/browser qualification and release gates before describing a deployment as verified.

## Rollback checklist

1. Stop submitting new v2 tasks from the caller or scheduler; do not delete receipts to make a retry appear new.
2. Look up each known task ID and reconcile admitted, running, or uncertain work. Do not replay a task whose outcome remains uncertain.
3. Preserve receipt and artifact storage for audit and recovery. Use explicit cleanup only for eligible reconciled terminal records.
4. Route future work through the existing legacy workflow only after confirming that doing so will not duplicate a v2 operation. Legacy behavior remains available, but it is not a substitute for recovering v2 receipts.
5. Revert caller-side v2 adoption independently of installed configuration. This guide makes no installation, settings, or live browser changes.

## Limits and qualification

Defaults cap runtime at 90 seconds, actions at 20, model requests at 3, repairs at 1, planning passes at 2, output tokens at 2,048, input tokens at 12,000, and cumulative reserved tokens at 24,000. Requests may tighten these values. Goals are limited to 16 KiB; schemas to 32 KiB and depth 16. Provider request bodies, inline output, artifacts, receipts, and retrieval chunks have separate bounds in the contract. These limits and examples are local contract behavior; they do not establish hosted tenant isolation, service-wide capacity, account authorization outside the private installation, or network egress controls.

Before enabling this workflow for operators, review the frozen contract examples with the [contract checker](contracts/check_bulk_v2.py), run the relative-link check, and complete the assigned browser qualification. Keep any qualification report tied to the exact source revision tested. No live installation or account action is part of this documentation change.
