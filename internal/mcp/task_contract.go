package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sirerun/ferro/internal/core"
)

type EvidenceMode string

const (
	EvidenceCompact   EvidenceMode = "compact"
	EvidenceArtifacts EvidenceMode = "artifacts"
)

type TaskPolicy struct {
	Mode    string   `json:"mode"`
	Origins []string `json:"origins"`
}

type RunTaskRequest struct {
	Schema       string              `json:"schema"`
	TaskID       string              `json:"task_id"`
	Goal         string              `json:"goal"`
	StartURL     string              `json:"start_url,omitempty"`
	MaxPlannings int                 `json:"max_plannings,omitempty"`
	ModelProfile string              `json:"model_profile"`
	Policy       *TaskPolicy         `json:"policy"`
	OutputSchema json.RawMessage     `json:"output_schema"`
	Limits       core.LimitOverrides `json:"limits,omitempty"`
	ReplayLabel  string              `json:"replay_key,omitempty"`
	Evidence     EvidenceMode        `json:"evidence,omitempty"`
}

type TaskStatus string

const (
	TaskSucceeded        TaskStatus = "succeeded"
	TaskFailed           TaskStatus = "failed"
	TaskBlocked          TaskStatus = "blocked"
	TaskCancelled        TaskStatus = "cancelled"
	TaskBudgetExhausted  TaskStatus = "budget_exhausted"
	TaskOutcomeUncertain TaskStatus = "outcome_uncertain"
)

type TaskError struct {
	Category string `json:"category"`
	Stage    string `json:"stage"`
	Retry    string `json:"retry"`
	Detail   string `json:"detail,omitempty"`
}

type SideEffectState string

const (
	SideEffectNone      SideEffectState = "none_observed"
	SideEffectConfirmed SideEffectState = "confirmed"
	SideEffectUnknown   SideEffectState = "unknown"
)

type Artifact struct {
	ID        string `json:"id"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
}

type TaskResult struct {
	Schema           string              `json:"schema"`
	TaskID           string              `json:"task_id"`
	ExecutionID      string              `json:"execution_id"`
	Status           TaskStatus          `json:"status"`
	StartedAt        time.Time           `json:"started_at"`
	EndedAt          time.Time           `json:"ended_at"`
	ModelProfile     string              `json:"model_profile"`
	ProfileRevision  string              `json:"profile_revision"`
	Model            string              `json:"model,omitempty"`
	EffectiveLimits  core.Limits         `json:"effective_limits"`
	Result           json.RawMessage     `json:"result,omitempty"`
	ResultArtifactID string              `json:"result_artifact_id,omitempty"`
	PartialResult    json.RawMessage     `json:"partial_result,omitempty"`
	Validation       string              `json:"validation"`
	Summary          string              `json:"summary,omitempty"`
	Usage            core.RequestUsage   `json:"usage"`
	Budget           core.BudgetSnapshot `json:"budget"`
	Error            *TaskError          `json:"error,omitempty"`
	SideEffectState  SideEffectState     `json:"side_effect_state"`
	Artifacts        []Artifact          `json:"artifacts,omitempty"`
}

type Profile struct {
	Name          string      `json:"name"`
	Revision      string      `json:"revision"`
	Endpoint      string      `json:"endpoint"`
	Model         string      `json:"model"`
	CredentialRef string      `json:"credential_ref,omitempty"`
	Limits        core.Limits `json:"limits"`
}

type ProfileDescription struct {
	Name         string   `json:"name"`
	Revision     string   `json:"revision"`
	Capabilities []string `json:"capabilities"`
}

type ResolvedProfile struct {
	Profile    Profile `json:"profile"`
	Endpoint   string  `json:"endpoint"`
	Model      string  `json:"model"`
	Credential string  `json:"-"`
}

func (p ResolvedProfile) String() string {
	return fmt.Sprintf("ResolvedProfile{%s@%s}", p.Profile.Name, p.Profile.Revision)
}
func (p ResolvedProfile) GoString() string { return p.String() }

type CredentialResolver interface {
	ResolveCredential(context.Context, string) (string, error)
}
type ProfileSource interface {
	LoadProfile(context.Context, string) (Profile, error)
}
type ProfileResolver interface {
	Resolve(context.Context, string) (ResolvedProfile, error)
}

type ReceiptState string

const (
	ReceiptAdmitted        ReceiptState = "admitted"
	ReceiptRunning         ReceiptState = "running"
	ReceiptSucceeded       ReceiptState = "succeeded"
	ReceiptFailed          ReceiptState = "failed"
	ReceiptBlocked         ReceiptState = "blocked"
	ReceiptCancelled       ReceiptState = "cancelled"
	ReceiptBudgetExhausted ReceiptState = "budget_exhausted"
	ReceiptUncertain       ReceiptState = "outcome_uncertain"
	ReceiptExpired         ReceiptState = "expired"
)

type Receipt struct {
	Owner         string       `json:"owner"`
	ExecutionID   string       `json:"execution_id"`
	TaskID        string       `json:"task_id"`
	RequestDigest string       `json:"request_digest"`
	State         ReceiptState `json:"state"`
	Result        *TaskResult  `json:"result,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	Artifacts     []Artifact   `json:"artifacts,omitempty"`
}

var (
	ErrReceiptNotFound    = errors.New("receipt not found")
	ErrReceiptConflict    = errors.New("receipt conflict")
	ErrReceiptCapacity    = errors.New("receipt capacity exceeded")
	ErrReceiptExpired     = errors.New("receipt expired")
	ErrReceiptOwnerDenied = errors.New("receipt owner denied")
)

type ArtifactSink interface {
	PutArtifact(context.Context, string, string, []byte, string) (Artifact, error)
}

// ReceiptStore owns durable receipt and artifact lifecycle. Close releases store resources and locks.
type ReceiptStore interface {
	ArtifactSink
	Admit(context.Context, string, RunTaskRequest, string) (Receipt, bool, error)
	Get(context.Context, string, string) (Receipt, error)
	Lookup(context.Context, string, string) (Receipt, error)
	Finalize(context.Context, string, string, TaskResult) error
	ReadArtifact(context.Context, string, string, string, int64, int64) ([]byte, error)
	Cleanup(context.Context, time.Time) error
	Close() error
}

type TaskPolicyGuard interface{ Check(context.Context) error }

type ExecutionRecord struct {
	Owner            string
	ExecutionID      string
	TaskID           string
	Profile          string
	ProfileRevision  string
	Model            string
	Limits           core.Limits
	Usage            core.RequestUsage
	Budget           core.BudgetSnapshot
	StartedAt        time.Time
	EndedAt          time.Time
	Status           TaskStatus
	Error            *TaskError
	SideEffectState  SideEffectState
	Summary          string
	Result           json.RawMessage
	ResultArtifactID string
	PartialResult    json.RawMessage
	Validation       string
	Artifacts        []Artifact
}

var taskIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var stableErrorCategories = map[string]bool{
	"invalid_input": true, "profile_missing": true, "disconnected": true, "pairing_changed": true, "tab_busy": true, "origin_denied": true, "login_required": true, "output_invalid": true, "provider_error": true, "deadline_exceeded": true, "capability_unsupported": true, "storage_error": true, "request_conflict": true,
}
var retryPolicies = map[string]bool{"never": true, "reconcile_only": true, "known_not_executed": true}

func ValidateTaskRequest(raw []byte) (RunTaskRequest, error) {
	var r RunTaskRequest
	if len(raw) > 65536 {
		return r, errInvalid("request exceeds 65536 bytes")
	}
	if !utf8Valid(raw) {
		return r, errInvalid("request is not valid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return r, errInvalid(err.Error())
	}
	if v, ok := fields["limits"]; ok && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		return r, errInvalid("limits must be an object")
	}
	if err := d.Decode(&r); err != nil {
		return r, errInvalid(err.Error())
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return r, errInvalid("trailing JSON")
	}
	return validateTaskRequestSemantics(r)
}

// validateTaskRequestSemantics validates a decoded request independently of
// its original wire encoding. ValidateTaskRequest enforces the raw byte
// limit; typed callers have no original encoding and receive semantic checks.
func validateTaskRequestSemantics(r RunTaskRequest) (RunTaskRequest, error) {
	if r.ModelProfile == "" {
		r.ModelProfile = "legacy-mcp"
	}
	if r.Policy != nil {
		policy := *r.Policy
		policy.Origins = append([]string(nil), r.Policy.Origins...)
		r.Policy = &policy
	}
	if !utf8Valid([]byte(r.Goal)) || !utf8Valid([]byte(r.StartURL)) || !utf8Valid([]byte(r.ReplayLabel)) || !utf8Valid(r.OutputSchema) {
		return r, errInvalid("request contains invalid UTF-8")
	}
	if r.Policy != nil {
		for _, origin := range r.Policy.Origins {
			if !utf8Valid([]byte(origin)) {
				return r, errInvalid("origin is not valid UTF-8")
			}
		}
	}
	if (r.Schema != "" && r.Schema != "ferro.task/v2") || !validTaskID(r.TaskID) || r.Goal == "" || r.ModelProfile == "" || r.Policy == nil || len(r.OutputSchema) == 0 || bytes.Equal(bytes.TrimSpace(r.OutputSchema), []byte("null")) {
		return r, errInvalid("missing or invalid required field")
	}
	if len(r.Goal) > 16384 || !utf8Valid([]byte(r.Goal)) {
		return r, errInvalid("goal exceeds 16384 bytes or is invalid UTF-8")
	}
	if len(r.TaskID) > 128 || !validTaskID(r.ModelProfile) {
		return r, errInvalid("invalid task_id or model_profile")
	}
	if len(r.ReplayLabel) > 256 || !utf8Valid([]byte(r.ReplayLabel)) {
		return r, errInvalid("invalid replay_key")
	}
	if r.Policy.Mode == "" {
		r.Policy.Mode = "read_write"
	}
	if (r.Policy.Mode != "read_write" && r.Policy.Mode != "read_only") || len(r.Policy.Origins) == 0 {
		return r, errInvalid("unsupported policy or empty origins")
	}
	origins, err := canonicalOrigins(r.Policy.Origins)
	if err != nil {
		return r, err
	}
	r.Policy.Origins = origins
	if r.MaxPlannings < 0 {
		return r, errInvalid("max_plannings must be nonnegative")
	}
	if r.MaxPlannings > 0 {
		if r.Limits.PlanningPasses != nil && *r.Limits.PlanningPasses != int64(r.MaxPlannings) {
			return r, errInvalid("max_plannings conflicts with limits.planning_passes")
		}
		planningPasses := int64(r.MaxPlannings)
		r.Limits.PlanningPasses = &planningPasses
	}
	if r.StartURL != "" {
		u, err := url.Parse(r.StartURL)
		if err != nil || u.Opaque != "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return r, errInvalid("start_url must be a valid http(s) URL without userinfo")
		}
		origin, err := canonicalOrigin(u.Scheme + "://" + u.Host)
		if err != nil {
			return r, err
		}
		i := sort.SearchStrings(origins, origin)
		if i == len(origins) || origins[i] != origin {
			return r, errInvalid("start_url origin is not permitted")
		}
	}
	if _, err := r.Limits.Apply(core.DefaultLimits()); err != nil {
		return r, errInvalid(err.Error())
	}
	if len(r.OutputSchema) > 32768 {
		return r, errInvalid("schema exceeds 32768 bytes")
	}
	if err := core.ValidateSchemaShape(r.OutputSchema); err != nil {
		return r, errInvalid("invalid output schema: " + err.Error())
	}
	if r.Evidence == "" {
		r.Evidence = EvidenceCompact
	}
	if r.Evidence != EvidenceCompact && r.Evidence != EvidenceArtifacts {
		return r, errInvalid("invalid evidence mode")
	}
	return r, nil
}

func errInvalid(s string) error { return fmt.Errorf("invalid task v2: %s", s) }
func validTaskID(s string) bool { return taskIDPattern.MatchString(s) }
func utf8Valid(b []byte) bool   { return strings.ToValidUTF8(string(b), "") == string(b) }

func canonicalOrigins(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, errInvalid("at least one exact origin is required")
	}
	set := map[string]bool{}
	for _, raw := range in {
		c, err := canonicalOrigin(raw)
		if err != nil {
			return nil, err
		}
		set[c] = true
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out, nil
}

func canonicalOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.Contains(u.Host, "*") {
		return "", errInvalid("origin must be exact http(s) origin")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errInvalid("invalid origin host")
	}
	if ip := net.ParseIP(host); ip == nil {
		host = strings.TrimSuffix(host, ".")
	}
	port := u.Port()
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", errInvalid("invalid origin port")
		}
	}
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	authority := host
	if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	if port != "" {
		authority += ":" + port
	}
	return u.Scheme + "://" + authority, nil
}

// ValidateTaskResult validates result metadata and inline JSON. For an artifact-backed
// result it validates the manifest only; the builder must validate the original
// result against its output schema before storing the artifact bytes.
func ValidateTaskResult(r TaskResult) error {
	if r.Schema != "ferro.result/v2" || !validTaskID(r.TaskID) || !validTaskID(r.ExecutionID) || !validTaskID(r.ModelProfile) || r.ProfileRevision == "" {
		return fmt.Errorf("invalid result identity")
	}
	if r.StartedAt.IsZero() || r.EndedAt.IsZero() || r.EndedAt.Before(r.StartedAt) {
		return fmt.Errorf("invalid result timestamps")
	}
	if r.Summary != "" && (!utf8Valid([]byte(r.Summary)) || len(r.Summary) > 2048) {
		return fmt.Errorf("invalid summary")
	}
	if err := r.EffectiveLimits.Validate(); err != nil {
		return fmt.Errorf("invalid effective limits: %w", err)
	}
	for _, v := range []*int64{r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.TotalTokens, r.Usage.ReasoningTokens, r.Usage.CacheReadTokens, r.Usage.CacheWriteTokens, r.Usage.BilledMicroUSD} {
		if v != nil && *v < 0 {
			return fmt.Errorf("negative usage")
		}
	}
	if err := validateResultBudget(r.Budget); err != nil {
		return err
	}
	if r.ResultArtifactID != "" && !validTaskID(r.ResultArtifactID) {
		return fmt.Errorf("invalid result artifact ID")
	}
	if len(r.Result) > 16384 || len(r.PartialResult) > 16384 {
		return fmt.Errorf("inline result exceeds 16384 bytes")
	}
	if len(r.Result) > 0 && (!utf8Valid(r.Result) || !json.Valid(r.Result)) {
		return fmt.Errorf("invalid result JSON")
	}
	if len(r.PartialResult) > 0 && (!utf8Valid(r.PartialResult) || !json.Valid(r.PartialResult)) {
		return fmt.Errorf("invalid partial result JSON")
	}
	if r.Validation != "valid" && r.Validation != "invalid" && r.Validation != "not_run" {
		return fmt.Errorf("invalid validation state")
	}
	switch r.Status {
	case TaskSucceeded:
		if r.Validation != "valid" || (len(r.Result) == 0) == (r.ResultArtifactID == "") || len(r.PartialResult) > 0 || r.Error != nil {
			return fmt.Errorf("success requires valid result without error")
		}
	case TaskFailed, TaskBlocked, TaskCancelled, TaskBudgetExhausted, TaskOutcomeUncertain:
		if len(r.Result) > 0 || r.ResultArtifactID != "" {
			return fmt.Errorf("failure cannot contain accepted result")
		}
	default:
		return fmt.Errorf("invalid task status")
	}
	if r.Error != nil {
		if !stableErrorCategories[r.Error.Category] || r.Error.Stage == "" || !retryPolicies[r.Error.Retry] || len(r.Error.Detail) > 2048 || !utf8Valid([]byte(r.Error.Detail)) {
			return fmt.Errorf("invalid typed task error")
		}
		if r.SideEffectState == SideEffectUnknown && r.Error.Retry != "never" && r.Error.Retry != "reconcile_only" {
			return fmt.Errorf("unknown side effects prohibit retry")
		}
	}
	if r.SideEffectState != SideEffectNone && r.SideEffectState != SideEffectConfirmed && r.SideEffectState != SideEffectUnknown {
		return fmt.Errorf("invalid side effect state")
	}
	if len(r.Artifacts) > 128 {
		return fmt.Errorf("too many artifacts")
	}
	artifactIDs := make(map[string]struct{}, len(r.Artifacts))
	var referenced *Artifact
	for i := range r.Artifacts {
		a := &r.Artifacts[i]
		if !validTaskID(a.ID) || len(a.SHA256) != 64 || !isHex(a.SHA256) || a.Size < 0 || a.Size > 4<<20 || a.MediaType == "" || len(a.MediaType) > 128 || !utf8Valid([]byte(a.MediaType)) {
			return fmt.Errorf("invalid artifact metadata")
		}
		if _, exists := artifactIDs[a.ID]; exists {
			return fmt.Errorf("duplicate artifact ID")
		}
		artifactIDs[a.ID] = struct{}{}
		if a.ID == r.ResultArtifactID {
			referenced = a
		}
	}
	if r.ResultArtifactID != "" {
		if referenced == nil {
			return fmt.Errorf("result artifact not found")
		}
		if referenced.Size <= 16384 || referenced.Size > 4<<20 || referenced.MediaType != "application/json" {
			return fmt.Errorf("invalid result artifact metadata")
		}
	}
	return nil
}

func validateResultBudget(b core.BudgetSnapshot) error {
	for _, v := range []int64{b.Requests, b.Actions, b.PlanningPasses, b.Repairs, b.ReservedTokens, b.UncertainRequests, b.EstimatedInputTokens, b.ReservedOutputTokens} {
		if v < 0 {
			return fmt.Errorf("negative budget counter")
		}
	}
	for _, v := range []*int64{b.ReportedUsage.InputTokens, b.ReportedUsage.OutputTokens, b.ReportedUsage.TotalTokens, b.ReportedUsage.ReasoningTokens, b.ReportedUsage.CacheReadTokens, b.ReportedUsage.CacheWriteTokens, b.ReportedUsage.BilledMicroUSD, b.ReservedMicroUSD, b.UnresolvedMicroUSD} {
		if v != nil && *v < 0 {
			return fmt.Errorf("negative budget amount")
		}
	}
	if b.Currency != "" && b.Currency != "USD" {
		return fmt.Errorf("unsupported budget currency")
	}
	if b.Currency == "" && (b.ReportedUsage.BilledMicroUSD != nil || b.ReservedMicroUSD != nil || b.UnresolvedMicroUSD != nil) {
		return fmt.Errorf("budget currency required for monetary amounts")
	}
	return nil
}
func isHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
