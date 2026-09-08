package core

import "time"

// RunMetrics reports what a Runner.Run call actually cost: LLM calls,
// replans, repairs, and a rough token estimate. It is the machine-checkable
// evidence for ferro's core claim — that the LLM is asked once (or rarely),
// not once per action (RFC M4 / kazi work plan E3-T1).
//
// EstimatedTokens is a coarse approximation (len(prompt+response)/4) — good
// enough to compare "few calls" vs "an agent loop", not a billing figure.
type RunMetrics struct {
	LLMCalls        int               // total model calls: planning + repairs + structuring
	Plannings       int               // replans after the initial plan
	Repairs         int               // number of single-step repair attempts
	ReplayHits      int               // validated plan-cache hits
	PlannerRetries  int               // malformed planner-output retries
	CacheErrors     []string          // load/flush failures; task result remains valid
	ExtractErrors   map[string]string // per-field extraction failures
	CacheHits       int               // resolution-cache hits (0 unless a cache is attached)
	EstimatedTokens int               // len(prompt+response)/4, summed across LLM calls
	Duration        time.Duration     // wall-clock time for the whole Run call
	ErrorClass      string            // set only when Run returns an error
}
