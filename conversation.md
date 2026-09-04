# Chat with Ox Alpha — ferro design conversation

Source: https://oxalpha.com/chat (Ox Alpha - Stealth Reasoning Model)

---

**User:** Why is AI browser automation so slow? What can be done to make it faster? I was thinking of creating a Go program to automate browsers using AI. I want it fast and token efficient. What do you think?

**Ox Alpha:** Good question — and a Go-based tool is a reasonable idea. Let me break down why it's slow and what you can do about it.

### Why AI browser automation is slow

1. **The LLM is usually the bottleneck.** Most tools (like browser-use, Playwright + GPT agents) call an LLM for every single action: look at page → decide → click → look again → decide. That's 2–10 seconds of inference per click, plus a full page snapshot in the context each time.
2. **Token bloat.** DOM/accessibility tree snapshots can be 10k–50k tokens per step. At 20 steps, you're burning enormous context, which also slows inference and inflates cost.
3. **Sequential loops.** Screenshot → LLM → act → screenshot → LLM... everything waits on the slowest link.
4. **Browser overhead.** Launching, navigating, waiting for network idle, waiting for hydration on JS-heavy SPAs.

### How to make it fast

1. **Only use the LLM where needed.** This is the big one. A hybrid architecture:
   - Deterministic code handles navigation, waits, retries, form filling from known selectors.
   - LLM is used once to plan or extract a schema, or only when the deterministic path fails (a fallback, not the driver).
2. **Use structured extraction, not step-by-step agents.** Instead of "click here, then there," ask the LLM: "Given this page, return JSON with the checkout button selector, the price field, etc." Then execute in pure code. One LLM call instead of 20.
3. **Reduce the context.** Send a compact accessibility tree or filtered DOM (visible, interactive elements only) rather than full HTML. Cap elements, use stable IDs.
4. **Use a fast model for perception.** Small/fast models for element identification, big models only for complex planning. Route between them.
5. **Parallelize in Go.** Go shines here:
   - Goroutines for concurrent page operations across tabs/sessions
   - A worker pool of browser contexts
   - Channels to pipeline: while the LLM plans step N+1, execute step N
6. **Reuse.** Cache selectors and plans per site. Warm browser pools (chromedp keeps CDP connections alive). Skip screenshots unless needed — text is cheaper than vision.
7. **Use CDP directly.** chromedp talks raw Chrome DevTools Protocol — no WebDriver layer, no Node/Playwright dependency. Rod is another solid option.

**Suggested architecture**
```
Go orchestrator
├─ Browser pool (chromedp, warm contexts)
├─ Page compiler: DOM → compact accessibility snapshot
├─ LLM layer: plan/extract once → JSON action list
├─ Deterministic executor: runs actions, handles waits/retries
└─ Fallback: only re-invoke LLM on failure, with error context
```

The key mental shift: don't build an agent that asks the AI what to do at every step — build a compiler that asks the AI for a plan and executes it mechanically. That's where you get both speed and token efficiency.

Watch out for: dynamic content changing mid-plan (add re-validation), anti-bot detection (headless Chrome is detectable), and brittle selector caching.

---

**User:** Is there a Go framework/tool that already is a "a compiler that asks the AI for a plan and executes it mechanically"?

**Ox Alpha:** Honest answer: not exactly — the plan-compile-execute pattern you want doesn't have a mature Go implementation yet. But there are pieces close to it, and some projects worth studying:

**Closest existing tools (not Go)**
- **Stagehand (Browserbase, TypeScript)** — probably the closest to your idea. Instead of an agent loop, it offers `act()`, `extract()`, and `observe()` primitives: the AI resolves what to act on (returns a selector/method), then executes deterministically, and caches results for repeat runs. This is the design worth copying.
- **browser-use (Python)** — popular but it is the slow agent-loop model you're trying to avoid.
- **Skyvern** — vision-based agent, also loop-heavy.

**Go ecosystem — pieces, not the whole thing**
- **chromedp / go-rod** — the browser layer. Rod even has a rod-utils/Helium flavor, but neither has AI planning built in. Rod's setup (screenshots, element handles, CDP control) is clean and a good foundation.
- **LangChainGo** — has agents and tool-calling, but it's a generic agent framework — you'd still build the browser compiler on top.
- **Eino (CloudWeGo)** — Go LLM orchestration framework from ByteDance, has graph/pipeline abstractions that fit a plan→execute flow nicely.
- **Genkit Go (Firebase)** — structured output/JSON schema support is good for the "one-shot plan" step.

**My take**

You're not going to find this off the shelf in Go — which is arguably an opportunity, not a blocker. The architecture is small if you scope it right:
- chromedp or rod → browser + DOM access
- One LLM call with structured output → plan as a JSON array of typed actions (`click{ref}`, `fill{ref, text}`, `extract{schema}`, `goto{url}`, `wait{...}`)
- A `Plan` → `Executor` in Go — this is ~300–500 lines, and Go's type system makes the action types pleasant (`type Action interface{}`)
- DOM compiler → numbered interactive elements (ref IDs), the trick Stagehand and browser-use both use
- Failure handler → on selector failure, re-snapshot + one repair LLM call, not a full replan

If you build that, you've essentially written "Stagehand in Go."

---

**User:** Draft an RFC.

**Ox Alpha:**

# RFC: ferro — A Token-Efficient AI Browser Automation Framework for Go

Status: Draft
Author: [Your name]
Created: [Date]
Target version: v0.1.0

## 1. Abstract

ferro is a Go library for AI-driven browser automation that treats the LLM as a compiler, not an agent. Instead of invoking a model at every step of a browsing session, ferro asks the model for a declarative plan (JSON), then executes that plan deterministically on Chrome via CDP. Re-planning occurs only on failure. The design targets two goals: low latency (one or few LLM calls per task) and token efficiency (compact page serialization, schema-constrained plans, cached resolutions).

## 2. Motivation

Current AI browser tools (browser-use, Skyvern, most LangChain agents) follow an agent loop:

```
observe → LLM → act → observe → LLM → act → ...
```

This has known costs:
- **Latency:** 2–10s of inference per elementary action. A 20-step task takes minutes.
- **Cost/context bloat:** Full DOM or accessibility snapshots (10–50k tokens) are re-sent every step.
- **Reliability:** Each LLM call is a fresh chance to hallucinate an action.

Existing Go tooling covers only parts of the stack: chromedp and go-rod provide fast CDP control with no AI layer; LangChainGo/Eino provide LLM orchestration with no browser semantics. No Go library implements the plan-compile-execute pattern (cf. Stagehand in TypeScript).

We propose a small, composable library that fills this gap.

## 3. Design Principles

- The LLM plans once; Go executes. Deterministic code owns waits, retries, navigation, and form input.
- Text over vision by default. Screenshots only on explicit request or as a fallback.
- Compact page representation. Only interactive/visible elements, with stable integer ref IDs.
- Schema-constrained LLM output. Plans are validated against a Go type system; malformed plans are rejected before execution.
- Zero hardcoded model dependencies. Any OpenAI-compatible endpoint works (including local models via Ollama/vLLM).
- Library, not platform. No cloud, no daemon, no config files required.

## 4. Architecture

```
┌────────────────────────────────────────────────────┐
│                   User Task (string)               │
└──────────────────────┬─────────────────────────────┘
                        ▼
┌────────────────────────────────────────────────────┐
│ Planner: page snapshot + task → Plan (JSON)         │
│          one LLM call, structured output            │
└──────────────────────┬─────────────────────────────┘
                        ▼
┌────────────────────────────────────────────────────┐
│ Executor: mechanical, typed action loop             │
│  - waits, retries, timeouts handled in Go           │
│  - on action failure → Repairer                     │
└──────────────────────┬─────────────────────────────┘
                        ▼
┌────────────────────────────────────────────────────┐
│ Repairer (only on failure):                         │
│   fresh snapshot + error → minimal patch Plan       │
└────────────────────────────────────────────────────┘
```

### 4.1 Page Compiler (DOM → Snapshot)

Produces a compact text representation of the current page:

```
[1] <button> "Add to cart"                visible
[2] <input type=email> "Email"            visible, editable
[3] <a href="/checkout"> "Checkout"       visible
```

Rules:
- Only interactive elements (buttons, links, inputs, selects, textareas) plus landmark text (headings, labels).
- Filtered by visibility and viewport intersection.
- ref IDs are assigned per-snapshot and mapped internally to CDP node handles.
- Target size: < 4k tokens for typical pages, hard cap configurable (`MaxSnapshotElements`).

### 4.2 Plan Type

The LLM emits a plan — a JSON array of typed actions (see `plan.go` for the final Go types).

The planner prompt includes the schema, the task, and the current snapshot. The response is parsed and validated before execution begins.

### 4.3 Executor

A pure-Go loop with no LLM involvement:
- Resolves Ref → node handle, fails fast if the node is gone.
- Applies per-action defaults: auto-wait for element visibility, navigation settle, network quiescence (configurable).
- Supports conditional execution to reduce re-planning: Extract results can be referenced in later steps via a lightweight template (`{{extract.price}}`) for simple branching done in Go.

### 4.4 Repairer

On action failure:
- Take a fresh snapshot.
- Send only: original failed step + error + fresh snapshot (not full history).
- LLM returns either a patched step or `Abort{Reason}`.
- Repair budget is capped (`MaxRepairs`, default 2) — after that, the task fails with a structured error.

This bounds worst-case cost: 1 + MaxRepairs LLM calls per task, versus N for an agent loop.

### 4.5 Caching

- Resolution cache: (domain, normalized intent, element signature) → selector persisted locally. On cache hit, zero LLM calls.
- Plans may be declared replayable by the caller, enabling the same plan to run across sessions with re-validation.

## 5. Proposed Public API (v0 sketch)

```go
package ferro

client := ferro.NewLLM(ferro.OpenAICompatible{
    BaseURL: "http://localhost:11434/v1", // or any compatible endpoint
    Model:   "qwen2.5:14b",
})

br, err := ferro.NewBrowser(ferro.BrowserConfig{
    Headless: true,
    Pool:     ferro.PoolConfig{Size: 8}, // warm contexts
})

task := ferro.Task{
    Goal:   "Buy the cheapest 1kg bag of coffee under $20",
    Schema: OrderResultSchema, // optional structured output
}

result, err := ferro.Run(ctx, br, client, task)
```

Concurrency is achieved by running many `Run` calls against a pooled browser — each task gets its own context and plan.

## 6. Non-Goals (v1)

- General-purpose agent loops (users who want that can use browser-use).
- Computer-vision/visual grounding (a possible later milestone behind a `VisionSnapshot` option).
- Anti-bot evasion tooling beyond standard stealth flags.
- A DSL or declarative YAML format — Go code is the interface.

## 7. Alternatives Considered

| Alternative | Why rejected |
|---|---|
| Wrap Stagehand / Playwright via sidecar | Violates "single Go binary, no Node" goal; adds operational burden |
| Build on LangChainGo agents | Agent-loop model is exactly what we're avoiding |
| Emit JavaScript snippets instead of plans | Harder to validate, sandbox, and cache than typed actions |
| Screenshot/vision-first (like Skyvern) | 10–50× token cost; text snapshots suffice for most tasks |

## 8. Risks and Open Questions

- Plan drift on dynamic pages. Mitigation: re-validation of each ref before execution + repairer. Open question: should multi-page tasks trigger an implicit replan on navigation? (Proposed: yes, plans are scoped to a "page phase.")
- Complex multi-page workflows (search → filter → paginate) may genuinely need interleaved planning. Possible middle ground: Plan supports a `PlanAgain{}` action, making replanning an explicit, addressable step rather than an implicit loop.
- Local/small models may struggle with strict JSON output. Mitigation: use structured-output/constrained-decoding where the endpoint supports it; fall back to JSON repair + one retry.
- Ref stability across snapshots is a footgun for cache correctness; cache entries must key on element signatures (tag + text + attributes), never refs.
- Naming. ferro is a placeholder; conflicts should be checked before first release.

## 9. Milestones

- **M1:** chromedp page compiler + snapshot format + golden tests against saved DOM fixtures
- **M2:** Planner + typed plan validation + deterministic executor for Goto/Click/Fill/Wait/Done
- **M3:** Extract with JSON Schema, repairer, error taxonomy
- **M4:** Browser pool, resolution cache, concurrency benchmarks (target: median task ≤ 3 LLM calls; ≥ 90% token reduction vs. agent-loop baseline)
- **M5:** Optional vision fallback, PlanAgain semantics, 1.0 API freeze

## 10. Feedback Requested

- Opinionated defaults for executor waits (network idle vs. DOM settle vs. fixed budget)?
- Should Extract support streaming results for long paginated scrapes?
- Is conditional execution via templates sufficient, or do we need a first-class Branch action in v0.1?

---

**User:**
> Opinionated defaults for executor waits (network idle vs. DOM settle vs. fixed budget)? - you decide.
> Should Extract support streaming results for long paginated scrapes? - you decide
> Is conditional execution via templates sufficient, or do we need a first-class Branch action in v0.1? - you decide
>
> Now write the Go code one file at a time.

**Ox Alpha:** (Design decisions closing out the RFC's questions, then File 1 of 6: `plan.go` — see `plan.go` in this repo. Design notes: flat tagged-union JSON rather than nested per-type objects — small models emit this far more reliably; `Validate()` catches everything checkable before touching a browser; ref resolution is deliberately deferred to execution so plans remain replayable; `Secret` on `Fill` keeps credentials out of logs and LLM repair context.)

**User:** Next.

**Ox Alpha:** File 2 of 6: `snapshot.go` — the page compiler (DOM → compact snapshot with refs). See `snapshot.go`. Design notes: one `Evaluate` round trip, not N CDP calls — the naive approach (query every element via chromedp nodes) is 100+ round trips; this does the filtering in-page and ships back a few KB of JSON — the single biggest latency win in the snapshot path. Viewport filter with margin — the plan usually acts on what's on screen; off-screen stuff is noise tokens. `Truncated` flag is surfaced to the planner so the model knows refs are partial and can scroll instead of hallucinating refs it never saw. Name resolution order (aria-label → placeholder → `<label for>` → text) mirrors how accessibility trees work.

**User:** Next.

**Ox Alpha:** File 3 of 6: `executor.go` — the mechanical action loop, ref→node resolution (`executor_refs.go`), and the layered wait strategy (`settle.go`, DOM settle with budget cap). See those files. Design notes: Executor is LLM-free and context-carried — the snapshot rides in `context.Context` rather than a struct field, so `Execute` is stateless and safe for a pooled browser. Every state-changing action settles afterward — clicks and scrolls trigger rerenders, skipping settle is the #1 cause of "clicked but nothing happened" flakiness. `Select` dispatches a real `change` event — naive `el.value = x` breaks React/Vue apps that listen for events. Budget cap everywhere — worst case per action is bounded: settle ≤ 5s, waits ≤ 5s, so a 30-step plan can't hang forever.

Known limitation: signature-based selectors (`button[aria-label="..."]`) can match multiple nodes or drift on dynamic text. Mitigations, in order: (1) chromedp clicks the first match — acceptable for v0.1; (2) failures route to the repairer with a fresh snapshot; (3) M4's resolution cache stores what actually worked, keyed on signature, so repeat runs skip this fragility entirely.

**User:** Next.

**Ox Alpha:** File 4 of 6: `runner.go` — the orchestrator (planner call, run loop, PlanAgain, extract structuring). See `runner.go`. Design notes: the planner prompt is ~40 lines, fixed — never includes history, screenshots, or prior plans; a task costs plan prompt + snapshot ≈ 2–5k tokens total, once. Repairs are surgical: one failed step + fresh snapshot in, one patched step out. `PlanAgain` is the pressure valve for the "page state differs from expectation" case. `parsePlan` is deliberately lenient but single-pass (fence-stripping, brace-trimming).

**User:** Next.

**Ox Alpha:** File 5 of 6: `repair.go` — the repairer (patch-a-step, error taxonomy for classifying failures as repairable vs. fatal). See `repair.go`.

**User:** Next.

**Ox Alpha:** File 7: `llm_adapter.go` — the OpenAI-compatible client (~60 lines), satisfying the `LLMClient` interface with any OpenAI-chat-compatible HTTP endpoint (OpenAI, Ollama, vLLM, etc). See `llm_adapter.go`.

**User:** M4 resolution cache

**Ox Alpha:** File 9: `cache.go` — the M4 resolution cache. The core idea: what actually worked (the concrete selector/locator used for a given element signature on a given domain) gets persisted, so repeat runs on the same site skip planning and resolution entirely — zero LLM calls on a warm cache hit. See `cache.go`.

**User:** What next?

**Ox Alpha:** Here's my honest priority order — driven by risk, not by what's fun to build.

1. **Make the smoke test actually pass** (highest value, lowest cost). Everything so far is design that has never touched a real Chrome. The three predicted failures from the test (settle-vs-navigation, ref numbering, template misuse) need fixing, and settle.go's "execution context destroyed = navigated" bug is a guaranteed real-world flake. Until one end-to-end run works, everything else is speculative. Do this first — it's a day, not a week.
2. **The ReplayKey plan-replay path.** It's the feature that delivers the headline claim ("zero LLM calls on repeat runs") — without it, the cache only saves repair cost, which is small. It's simple: persist validated plans keyed by ReplayKey, load before planning, let recordOutcome + repairer absorb drift. Maybe 150 lines. And critically: it's also the benchmark — running a task 10× via replay IS the measurement harness. Instrument Runner with a call counter and token estimate, and the M4 assertion (warm-run LLM calls = 0) falls out for free.
3. **Fix the selector fragility for real** (the known weakest joint). The signature→CSS-selector guessing in executor_refs.go is the thing most likely to make the planner's correct plans fail at execution. Two concrete fixes, in order: click by snapshot context, not guessed CSS (store the element's index among matching siblings at snapshot time and resolve via a JS locator function); add `Element.Context` (nearest heading) — fixes both selector disambiguation and cache-signature collisions.
4. **Only then: real-model validation.** Run the planner prompt against Ollama with a 7–14B model (Qwen2.5-14B is the right first pick) over 10–20 fixture tasks. Does one call at temperature 0 produce valid plans? If small models fail, the fix is prompt work or a "medium model for planning" default.
5. **Deliberately not yet:** vision fallback, Branch, streaming Cursor (deferred features); concurrency benchmarks / pool hard-capping (premature until single-task reliability is boring); publishing/naming (build the demo first).

The one-sentence version: **one green end-to-end test, add ReplayKey + run metrics, fix ref resolution properly, then measure the one-call bet against a real small model.**

---

**User:** Produce a detailed engineering work plan for the next steps, in the format used by "kazi", a reconciliation controller for software goals... (full kazi work-plan prompt)

**Ox Alpha:** See `WORKPLAN.md` in this repo for the full "Kazi Work Plan: ferro v0.1" (plan header, epics E1–E6, tasks per epic with Done-when criteria, execution waves, risks/open questions, and definition of done).

---

**Ox Alpha:** File 9: `cache.go` — the M4 resolution cache. The core idea: when the executor resolves a ref to a selector that works, remember it. Next time the same (site, element signature) appears, skip any LLM involvement entirely and go straight to the cached selector — with cheap re-validation, and invalidation driven by the taxonomy from repair.go. This is what turns ferro from "one LLM call per run" into "one LLM call ever, per site pattern." Key: (host, kind, signature); value: the selector that worked last time plus trust metadata. Hit path: cache lookup → selector still matches exactly one visible element → execute, zero LLM. Miss/invalid path: fall through to snapshot resolution; on success, store; on `ErrStaleRef`, delete the entry.

Resolution caching alone doesn't get repeat runs to zero LLM calls — the runner still calls the planner every `Run`. That's what `Task.ReplayKey` is for: a stable key lets the runner persist the validated plan itself and skip the planner entirely on replay.

Open issues flagged rather than silently resolved: signature collisions on generic elements (mitigation: add `Element.Context`, nearest heading text); cache poisoning on false-positive matches (mitigation: the `Successes` trust counter, plus debug logging of every cache-hit action); multi-user/multi-profile cache namespacing (deferred — not a v0.1 problem, but the JSON layout is a map so it isn't a migration later).

See `cache.go`, and the updated `executor_refs.go` / `executor.go` (cache-first `resolveRef`, `selectorMatches`, `recordOutcome`) in this repo for the integration.

---

*Note: this transcript was reconstructed from the live chat page. Prose sections are transcribed faithfully; a handful of characters may have been lost in DOM extraction inside the work-plan section — treat WORKPLAN.md's raw extraction (also included) as the more literal backup for that section. All Go source in this repo was extracted directly from the chat's code blocks (not the lossy prose extractor) and lightly cleaned up only where the page's copy mechanism visibly mangled a token (e.g. a missing `&`/`=`) — logical bugs and unresolved TODOs that Ox Alpha itself flagged in the conversation (e.g. the settle-vs-navigation race noted in settle.go) are preserved as noted, per the kazi work plan.*
