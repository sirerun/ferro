# Ferro task contract v2 — CANDIDATE

Coordinator review required. This is an additive, unfrozen G01 candidate; it does not authorize L01–L10 dispatch. No contract lock or hash is emitted before coordinator acceptance.

## Wire types and bounds

The request literal is `ferro.task/v2`; result literal is `ferro.result/v2`. `RunTaskV2Request` carries required `task_id`, `goal`, `profile`, and `policy`; numeric overrides are pointers so an omitted value differs from explicit zero. Goal is at most 16 KiB UTF-8 bytes, schema at most 32 KiB/depth 16, request at most 64 KiB. Task IDs are ASCII `[A-Za-z0-9_-]{1,128}`. Unknown request fields and trailing JSON are rejected. Evidence is `compact` or `artifacts`; omitted means compact.

Pilot ceilings: runtime 90,000 ms; actions 20; model requests 3; repairs 1; planning passes 2; output tokens 2,048; input tokens 12,000; total reserved tokens 24,000. Overrides only tighten the service-configured value. A positive micro-USD reserve is an estimate. Hard-dollar mode is rejected as unsupported.

Policy permits only `read_only` and exact HTTP(S) origins. Credentials, wildcards, query, fragment, and non-root paths are rejected. Hosts are lowercase, trailing DNS dot removed, default ports removed, origins deduplicated and sorted. Service allowlist intersection is deferred to G02. Navigation, snapshot, extraction, scroll, wait and settle are allowed by the L04 wrapper; click, fill, select and key are denied.

Output schema keywords are the existing subset: `$schema`, `title`, `description`, `type`, `properties`, `items`, `required`, `additionalProperties`, `enum`, `minimum`, `maximum`, `minLength`, `maxLength`, `minItems`, `maxItems`. No remote refs. The existing validator remains authoritative for matching and type semantics. Bounds: summary 2 KiB; inline output 16 KiB; artifact and receipt 4 MiB; artifact read chunk 64 KiB; provider body 2 MiB/error body 8 KiB; storage 256 MiB. No automatic eviction. Cleanup only reconciled terminal receipts older than 30 days and leaves key tombstones; exhaustion fails closed.

## Accounting and identity

Nullable usage fields distinguish unknown from zero. Transmission is `not_sent`, `sent_unknown`, or `response_received`. Metadata completion contract is one HTTP request per invocation and no fallback retry; legacy `LLMClient` and `run_task` remain unchanged. Request kinds are planning, repair, extraction, and parse_retry; every attempt counts, with planning and repair ceilings separately enforced by L03. Unknown usage keeps the reservation; reconciliation is once-only. No inferred billed usage from text length.

Replay identity canonicalizes the string-field context (principal, profile revision, model, policy/schema digest, compatibility, layout, caller label) as JSON and hashes SHA-256 hex. Empty required partition values fail closed. Receipt owner is trusted server identity, never a request field. Atomic owner/task admission returns existing state for same digest and conflict for changed digest. Restarted in-flight work is outcome_uncertain and is never redispatched automatically.

## Profile and result contracts

Profiles are immutable revisions from a content digest; endpoint and exact model are service resolved, credentials are references and never serialized. HTTPS is required except localhost HTTP for private profiles. No automatic fallback. Legacy mappings use explicit configured values named `legacy-mcp` and `legacy-chat`.

Stable error/status mapping proposal: malformed/unsupported input `invalid_request` (no retry); unknown profile `profile_not_found` (no retry); policy denial `policy_denied` (no retry); budget exhaustion `budget_exhausted` (no retry); confirmed execution failure `failed` (no automatic retry); lost response after send `outcome_uncertain` (reconcile only); schema mismatch `invalid_output` (no retry); storage/capacity failure `receipt_unavailable` (no dispatch if admission persistence failed). Cost and usage remain independent of outcome. Result success requires validated output; partial output remains partial.

## Candidate Go signatures for L01–L10

Types and implemented G01 signatures are in the owned Go files. Planned implementation signatures (documentation only):

- L01 `NewProfileResolverV2(source ProfileSourceV2, credentials CredentialResolverV2) (ProfileResolverV2, error)`; `ProfileResolverV2.Resolve(context.Context, string) (ResolvedProfileV2, error)`.
- L02 `(*OpenAICompatible).CompleteWithUsage(context.Context, string, string) (core.CompletionV2, error)` implementing `llm.MetadataCompleterV2`; exactly one network attempt.
- L03 `NewTaskBudgetV2(limits core.LimitsV2, started time.Time) (core.BudgetControllerV2, error)`; interface methods are declared in core.
- L04 `NewTaskPolicyDriverV2(driver core.PageDriver, policy TaskPolicyV2, guard TaskPolicyGuardV2, budget core.BudgetControllerV2) (core.PageDriver, error)`.
- L05 `PreflightSchemaV2(raw json.RawMessage) error`; `ValidateResultV2(raw json.RawMessage, value any) error`.
- L06 `ReplayIdentityV2(ctx ReplayContextV2) (string, error)`.
- L07 `OpenReceiptStoreV2(directory string, maxBytes int64) (ReceiptStoreV2, error)`; store also implements `ArtifactSinkV2`.
- L08 `BuildTaskResultV2(ctx context.Context, record ExecutionRecordV2, sink ArtifactSinkV2) (TaskResultV2, error)`.
- L09 consumes request/result/receipt fixtures; no production API in this candidate.
- L10 documents the above signatures and migration without changing legacy wire behavior.

## Review questions

1. Should the G01 MCP request validator defer schema keyword/type checking to L05’s exported core API, or should core expose a private G01 bridge? `core.checkSchema` cannot be called from `mcp`; G01 currently performs bounded syntax/depth/keyword checks in MCP without replacing the core matcher.
2. Exact provider usage field mapping and documentation references require provider selection in L02; no provider has been chosen here.
3. `TaskPolicyGuardV2.Check` is a proposed narrow candidate boundary; coordinator should confirm its scope against captured lease and pairing-generation checks.
