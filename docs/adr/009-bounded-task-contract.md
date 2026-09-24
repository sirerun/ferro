# ADR 009: Bounded task contract v2 

Status: accepted for local bounded read-only implementation. Date: 2026-09-24.

## Decision

Define additive `ferro.task/v2` and `ferro.result/v2` contracts in the existing core, MCP and LLM packages. Core owns shared limits, nullable provider usage, completion/transmission metadata, budget interfaces and replay context. MCP owns wire types, profile/credential resolver interfaces, receipt/artifact interfaces and request validation. LLM may depend on core. Core does not depend on MCP or LLM. Legacy public `LLMClient` and `run_task` remain unchanged.

Requests are bounded, reject unknown/trailing JSON, use pointer numeric overrides, require read-only policy and exact canonical origins, and cannot exceed service limits. Hard-dollar mode is refused. Provider usage may be unknown; sent-but-unanswered requests retain reservations. Receipt admission is owner-scoped and digest keyed; uncertain work is never replayed automatically.

## Consequences

L01–L10 can implement against additive types after review. G01 does not create placeholder implementations for later lanes. The accepted lock hashes shared contract declarations, fixtures and exact lane-owned implementation signatures. Existing core schema matching remains the only schema engine.

## Resolved decisions

core.ValidateSchemaShapeV2 bridges bounded schema preflight to the existing private validator. L05 consumes that helper without replacing validation semantics. Provider metadata uses one HTTP attempt and explicit optional cost-currency mapping. Monetary reserve admission remains unsupported without a verified rate contract. Output larger than the inline limit uses an explicit accepted-result artifact reference, not a truncated or invalid inline success.

All tool schemas are generic MCP consumer contracts. External schedulers own durable queues and batch orchestration; Ferro owns bounded browser execution and receipts. No particular orchestrator is an implementation dependency. Hosted account authority and direct mutation approval remain separate reviewed extensions.
