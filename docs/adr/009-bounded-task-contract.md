# ADR 009: Bounded task contract v2 (candidate)

Status: proposed; coordinator review pending. Date: 2026-09-24.

## Decision

Define additive `ferro.task/v2` and `ferro.result/v2` contracts in the existing core, MCP and LLM packages. Core owns shared limits, nullable provider usage, completion/transmission metadata, budget interfaces and replay context. MCP owns wire types, profile/credential resolver interfaces, receipt/artifact interfaces and request validation. LLM may depend on core. Core does not depend on MCP or LLM. Legacy public `LLMClient` and `run_task` remain unchanged.

Requests are bounded, reject unknown/trailing JSON, use pointer numeric overrides, require read-only policy and exact canonical origins, and cannot exceed service limits. Hard-dollar mode is refused. Provider usage may be unknown; sent-but-unanswered requests retain reservations. Receipt admission is owner-scoped and digest keyed; uncertain work is never replayed automatically.

## Consequences

L01–L10 can implement against additive types after review. G01 does not create placeholder implementations for later lanes. A contract lock/hash is intentionally deferred. Existing core schema matching remains the only schema engine.

## Open decision

The MCP package cannot call core’s private `checkSchema`. L05 owns the planned exported `PreflightSchemaV2` / `ValidateResultV2`, so coordinator review must settle how G01 wire validation invokes keyword/type preflight without transferring L05 implementation ownership.
