package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const TaskSchemaV2 = "ferro.task/v2"
const ResultSchemaV2 = "ferro.result/v2"

var ErrUnsupportedHardDollarV2 = errors.New("hard-dollar mode is unsupported")

type LimitsV2 struct {
	RuntimeMS           int64  `json:"runtime_ms"`
	Actions             int64  `json:"actions"`
	ModelRequests       int64  `json:"model_requests"`
	Repairs             int64  `json:"repairs"`
	PlanningPasses      int64  `json:"planning_passes"`
	MaxOutputTokens     int64  `json:"max_output_tokens"`
	MaxInputTokens      int64  `json:"max_input_tokens"`
	TotalReservedTokens int64  `json:"total_reserved_tokens"`
	ReserveMicroUSD     *int64 `json:"reserve_micro_usd,omitempty"`
	HardDollar          bool   `json:"hard_dollar,omitempty"`
}
type LimitOverridesV2 struct {
	RuntimeMS           *int64 `json:"runtime_ms,omitempty"`
	Actions             *int64 `json:"actions,omitempty"`
	ModelRequests       *int64 `json:"model_requests,omitempty"`
	Repairs             *int64 `json:"repairs,omitempty"`
	PlanningPasses      *int64 `json:"planning_passes,omitempty"`
	MaxOutputTokens     *int64 `json:"max_output_tokens,omitempty"`
	MaxInputTokens      *int64 `json:"max_input_tokens,omitempty"`
	TotalReservedTokens *int64 `json:"total_reserved_tokens,omitempty"`
	ReserveMicroUSD     *int64 `json:"reserve_micro_usd,omitempty"`
	HardDollar          *bool  `json:"hard_dollar,omitempty"`
}

func DefaultLimitsV2() LimitsV2 {
	return LimitsV2{RuntimeMS: 90000, Actions: 20, ModelRequests: 3, Repairs: 1, PlanningPasses: 2, MaxOutputTokens: 2048, MaxInputTokens: 12000, TotalReservedTokens: 24000}
}
func (l LimitsV2) ValidateV2() error {
	d := DefaultLimitsV2()
	vals := []struct {
		name string
		v, m int64
	}{{"runtime_ms", l.RuntimeMS, d.RuntimeMS}, {"actions", l.Actions, d.Actions}, {"model_requests", l.ModelRequests, d.ModelRequests}, {"repairs", l.Repairs, d.Repairs}, {"planning_passes", l.PlanningPasses, d.PlanningPasses}, {"max_output_tokens", l.MaxOutputTokens, d.MaxOutputTokens}, {"max_input_tokens", l.MaxInputTokens, d.MaxInputTokens}, {"total_reserved_tokens", l.TotalReservedTokens, d.TotalReservedTokens}}
	for _, x := range vals {
		if x.v < 0 || x.v > x.m {
			return fmt.Errorf("%s outside [0,%d]", x.name, x.m)
		}
	}
	if l.ReserveMicroUSD != nil && *l.ReserveMicroUSD <= 0 {
		return fmt.Errorf("reserve_micro_usd must be positive")
	}
	if l.HardDollar {
		return ErrUnsupportedHardDollarV2
	}
	return nil
}
func (o LimitOverridesV2) ApplyV2(service LimitsV2) (LimitsV2, error) {
	out := service
	pairs := []struct {
		p *int64
		v *int64
	}{{o.RuntimeMS, &out.RuntimeMS}, {o.Actions, &out.Actions}, {o.ModelRequests, &out.ModelRequests}, {o.Repairs, &out.Repairs}, {o.PlanningPasses, &out.PlanningPasses}, {o.MaxOutputTokens, &out.MaxOutputTokens}, {o.MaxInputTokens, &out.MaxInputTokens}, {o.TotalReservedTokens, &out.TotalReservedTokens}}
	for _, x := range pairs {
		if x.p != nil {
			if *x.p < 0 {
				return LimitsV2{}, fmt.Errorf("limit override must be nonnegative")
			}
			if *x.p > *x.v {
				return LimitsV2{}, fmt.Errorf("limit override cannot exceed service maximum")
			}
			if *x.p < *x.v {
				*x.v = *x.p
			}
		}
	}
	if o.ReserveMicroUSD != nil {
		if *o.ReserveMicroUSD <= 0 {
			return LimitsV2{}, fmt.Errorf("reserve_micro_usd must be positive")
		}
		out.ReserveMicroUSD = o.ReserveMicroUSD
	}
	if o.HardDollar != nil {
		out.HardDollar = *o.HardDollar
	}
	return out, out.ValidateV2()
}

type RequestUsageV2 struct {
	InputTokens      *int64 `json:"input_tokens,omitempty"`
	OutputTokens     *int64 `json:"output_tokens,omitempty"`
	TotalTokens      *int64 `json:"total_tokens,omitempty"`
	ReasoningTokens  *int64 `json:"reasoning_tokens,omitempty"`
	CacheReadTokens  *int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int64 `json:"cache_write_tokens,omitempty"`
	BilledMicroUSD   *int64 `json:"billed_micro_usd,omitempty"`
}
type TransmissionV2 string

const (
	TransmissionNotSentV2          TransmissionV2 = "not_sent"
	TransmissionSentUnknownV2      TransmissionV2 = "sent_unknown"
	TransmissionResponseReceivedV2 TransmissionV2 = "response_received"
)

type CompletionV2 struct {
	Text                                   string
	Usage                                  RequestUsageV2
	ProviderRequestID, Model, FinishReason string
	Transmission                           TransmissionV2
}
type MetadataCompleterV2 interface {
	CompleteWithUsage(context.Context, string, string) (CompletionV2, error)
}
type ReservationV2 struct {
	ID                        string
	Kind                      string
	EstimatedInput, MaxOutput int64
}
type BudgetSnapshotV2 struct {
	Requests, Actions, PlanningPasses, Repairs int64
	ReservedTokens, UncertainRequests          int64
}
type BudgetControllerV2 interface {
	Admit(context.Context, string, int64, int64) (ReservationV2, error)
	Reconcile(string, CompletionV2) error
	AdmitAction(context.Context) error
	Snapshot() BudgetSnapshotV2
}
type ReplayContextV2 struct{ Principal, ProfileRevision, Model, PolicyDigest, SchemaDigest, Compatibility, Layout, CallerLabel string }
type ReplayIdentityV2 func(ReplayContextV2) (string, error)

// NewTaskBudgetV2 is the L03 constructor contract; its implementation belongs to L03.
// PreflightSchemaV2 and ValidateResultV2 belong to L05 and are intentionally not declared here.
// ReplayIdentityV2 is the L06 contract; the exported symbol documents its signature.
type ReplayIdentityFuncV2 func(ctx ReplayContextV2) (string, error)

var _ = json.Valid
var _ = time.Time{}
