# ADR 003: Complete the runner's replay and extraction contracts

Accepted, 2026-09-07.

The public API advertised ReplayKey, persisted selector learning, schema
extraction, and cache-hit metrics that were not wired into execution. Regression
tests demonstrated fresh planning on repeat runs, unmarshalable map keys,
unresolved extraction templates, and rejected bare-step repairs.

A Runner shares one synchronized selector/plan cache but creates independent
executor state for each task. Disk format version 1 stores selector records as
an array (CacheKey is a struct) and plan records under task/snapshot hashes.
Only successful, unrepaired plans without secret fills are cached. Snapshot
changes prevent positional-ref replay. Failure invalidates the cached plan;
repair resumes at the failed step and does not restart previous side effects.

Every run flushes atomically. Persistence warnings are metrics, not task errors:
reporting a successful action as failed could cause the caller to repeat it.
One process owns each cache file; independent writers are not merged or locked.

Schema extraction transfers control to the runner after a deterministic page
text read. One model call structures the value; local validation precedes the
next action. Non-object schemas use a result envelope for compatibility with
JSON-object endpoints. Final-result templates preserve types and Task.Schema is
enforced. The supported JSON Schema subset is explicitly listed in README;
unsupported keywords fail instead of silently weakening validation.

ADR 002's local shape checks and bounded planner correction are implemented.
The optional HTTP schema mode sends strict:false because arbitrary result and
schema objects are part of the existing API; it is opt-in to retain compatibility
with endpoints accepting json_object only. Local checks run for all endpoints.
A future restricted plan vocabulary can enable strict:true without claiming
unsupported server-side enforcement today.

Tests use local Chrome fixtures and deterministic model replies; passing them
proves wiring and failure behavior, not reliability of a specific live model.
