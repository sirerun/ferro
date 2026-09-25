package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const TaskSchema = "ferro.task/v2"
const ResultSchema = "ferro.result/v2"

var ErrUnsupportedHardDollar = errors.New("hard-dollar mode is unsupported")
var ErrBudgetExhausted = errors.New("task budget exhausted")
var ErrDuplicateReconcile = errors.New("reservation already reconciled")
var ErrInvalidReservation = errors.New("invalid reservation")
var ErrUnsupportedCostReserve = errors.New("monetary reservation cannot be enforced")

type Limits struct {
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
type LimitOverrides struct {
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

func DefaultLimits() Limits {
	return Limits{RuntimeMS: 90000, Actions: 20, ModelRequests: 3, Repairs: 1, PlanningPasses: 2, MaxOutputTokens: 2048, MaxInputTokens: 12000, TotalReservedTokens: 24000}
}
func (l Limits) Validate() error {
	d := DefaultLimits()
	vals := []struct {
		name string
		v, m int64
	}{{"runtime_ms", l.RuntimeMS, d.RuntimeMS}, {"actions", l.Actions, d.Actions}, {"model_requests", l.ModelRequests, d.ModelRequests}, {"repairs", l.Repairs, d.Repairs}, {"planning_passes", l.PlanningPasses, d.PlanningPasses}, {"max_output_tokens", l.MaxOutputTokens, d.MaxOutputTokens}, {"max_input_tokens", l.MaxInputTokens, d.MaxInputTokens}, {"total_reserved_tokens", l.TotalReservedTokens, d.TotalReservedTokens}}
	for _, x := range vals {
		if x.v <= 0 || x.v > x.m {
			return fmt.Errorf("%s outside [1,%d]", x.name, x.m)
		}
	}
	if l.ReserveMicroUSD != nil && *l.ReserveMicroUSD <= 0 {
		return fmt.Errorf("reserve_micro_usd must be positive")
	}
	if l.HardDollar {
		return ErrUnsupportedHardDollar
	}
	return nil
}
func (o LimitOverrides) Apply(service Limits) (Limits, error) {
	if err := service.Validate(); err != nil {
		return Limits{}, fmt.Errorf("invalid service limits: %w", err)
	}
	out := cloneLimits(service)
	pairs := []struct {
		p *int64
		v *int64
	}{{o.RuntimeMS, &out.RuntimeMS}, {o.Actions, &out.Actions}, {o.ModelRequests, &out.ModelRequests}, {o.Repairs, &out.Repairs}, {o.PlanningPasses, &out.PlanningPasses}, {o.MaxOutputTokens, &out.MaxOutputTokens}, {o.MaxInputTokens, &out.MaxInputTokens}, {o.TotalReservedTokens, &out.TotalReservedTokens}}
	for _, x := range pairs {
		if x.p != nil {
			if *x.p <= 0 {
				return Limits{}, fmt.Errorf("limit override must be positive")
			}
			if *x.p > *x.v {
				return Limits{}, fmt.Errorf("limit override cannot exceed service maximum")
			}
			if *x.p < *x.v {
				*x.v = *x.p
			}
		}
	}
	if o.ReserveMicroUSD != nil {
		if *o.ReserveMicroUSD <= 0 {
			return Limits{}, fmt.Errorf("reserve_micro_usd must be positive")
		}
		if service.ReserveMicroUSD != nil && *o.ReserveMicroUSD > *service.ReserveMicroUSD {
			return Limits{}, fmt.Errorf("reserve_micro_usd override cannot exceed service reserve")
		}
		out.ReserveMicroUSD = cloneInt64(o.ReserveMicroUSD)
	}
	if o.HardDollar != nil && *o.HardDollar {
		return Limits{}, ErrUnsupportedHardDollar
	}
	// Hard-dollar mode is rejected by service validation above and can never be
	// disabled by an untrusted request override.
	return out, out.Validate()
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneLimits(limits Limits) Limits {
	limits.ReserveMicroUSD = cloneInt64(limits.ReserveMicroUSD)
	return limits
}

type RequestUsage struct {
	InputTokens      *int64 `json:"input_tokens,omitempty"`
	OutputTokens     *int64 `json:"output_tokens,omitempty"`
	TotalTokens      *int64 `json:"total_tokens,omitempty"`
	ReasoningTokens  *int64 `json:"reasoning_tokens,omitempty"`
	CacheReadTokens  *int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens *int64 `json:"cache_write_tokens,omitempty"`
	BilledMicroUSD   *int64 `json:"billed_micro_usd,omitempty"`
}
type Transmission string

const (
	TransmissionNotSent          Transmission = "not_sent"
	TransmissionSentUnknown      Transmission = "sent_unknown"
	TransmissionResponseReceived Transmission = "response_received"
)

type Completion struct {
	Text                                   string
	Usage                                  RequestUsage
	ProviderRequestID, Model, FinishReason string
	Transmission                           Transmission
}
type MetadataCompleter interface {
	CompleteWithUsage(context.Context, string, string) (Completion, error)
}
type Reservation struct {
	ID                        string
	Kind                      string
	EstimatedInput, MaxOutput int64
}
type BudgetSnapshot struct {
	Requests             int64        `json:"requests"`
	Actions              int64        `json:"actions"`
	PlanningPasses       int64        `json:"planning_passes"`
	Repairs              int64        `json:"repairs"`
	ReservedTokens       int64        `json:"reserved_tokens"`
	UncertainRequests    int64        `json:"uncertain_requests"`
	EstimatedInputTokens int64        `json:"estimated_input_tokens"`
	ReservedOutputTokens int64        `json:"reserved_output_tokens"`
	ReportedUsage        RequestUsage `json:"reported_usage"`
	Currency             string       `json:"currency"`
	ReservedMicroUSD     *int64       `json:"reserved_micro_usd"`
	UnresolvedMicroUSD   *int64       `json:"unresolved_micro_usd"`
}

// L03 must reject nonnil monetary reserves unless its constructor receives a
// verified rate that lets it enforce the reserve; accepting an unenforceable
// reserve would misstate the budget guarantee.
type BudgetController interface {
	Admit(context.Context, string, int64, int64) (Reservation, error)
	Reconcile(string, Completion) error
	AdmitAction(context.Context) error
	Snapshot() BudgetSnapshot
}
type ReplayContext struct{ Principal, ProfileRevision, Model, PolicyDigest, SchemaDigest, Compatibility, Layout, CallerLabel string }

const maxSchemaBytes = 32 * 1024
const maxSchemaDepth = 16

// ValidateSchemaShape validates bounded JSON syntax and the existing core
// schema keyword subset without matching a result value.
func ValidateSchemaShape(raw json.RawMessage) error {
	if len(raw) == 0 || len(raw) > maxSchemaBytes {
		return fmt.Errorf("schema must contain 1 to %d bytes", maxSchemaBytes)
	}
	if !utf8.Valid(raw) {
		return fmt.Errorf("schema must be valid UTF-8")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	var parsed any
	if err := decoder.Decode(&parsed); err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("schema must contain one JSON value")
		}
		return fmt.Errorf("decode trailing schema data: %w", err)
	}
	schema, ok := parsed.(map[string]any)
	if !ok || len(schema) == 0 {
		return fmt.Errorf("schema must be a nonempty object")
	}
	if err := checkSchemaDepth(parsed, 1); err != nil {
		return err
	}
	return checkSchema(schema, "$")
}

func checkSchemaDepth(value any, depth int) error {
	if depth > maxSchemaDepth {
		return fmt.Errorf("schema exceeds maximum depth %d", maxSchemaDepth)
	}
	switch node := value.(type) {
	case map[string]any:
		for _, child := range node {
			if err := checkSchemaDepth(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range node {
			if err := checkSchemaDepth(child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// NewTaskBudget is the L03 constructor contract; its implementation belongs to L03.
// PreflightSchema and ValidateResult belong to L05 and are intentionally not declared here.
type ReplayIdentityFunc func(ctx ReplayContext) (string, error)
