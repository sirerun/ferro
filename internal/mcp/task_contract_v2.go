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

	"github.com/dndungu/ferro/internal/core"
)

type EvidenceModeV2 string

const (
	EvidenceCompactV2   EvidenceModeV2 = "compact"
	EvidenceArtifactsV2 EvidenceModeV2 = "artifacts"
)

type TaskPolicyV2 struct {
	Mode    string   `json:"mode"`
	Origins []string `json:"origins"`
}

type RunTaskV2Request struct {
	Schema       string                `json:"schema"`
	TaskID       string                `json:"task_id"`
	Goal         string                `json:"goal"`
	StartURL     string                `json:"start_url,omitempty"`
	ModelProfile string                `json:"model_profile"`
	Policy       *TaskPolicyV2         `json:"policy"`
	OutputSchema json.RawMessage       `json:"output_schema"`
	Limits       core.LimitOverridesV2 `json:"limits,omitempty"`
	ReplayLabel  string                `json:"replay_key,omitempty"`
	Evidence     EvidenceModeV2        `json:"evidence,omitempty"`
}

type TaskStatusV2 string

const (
	TaskSucceededV2        TaskStatusV2 = "succeeded"
	TaskFailedV2           TaskStatusV2 = "failed"
	TaskBlockedV2          TaskStatusV2 = "blocked"
	TaskCancelledV2        TaskStatusV2 = "cancelled"
	TaskBudgetExhaustedV2  TaskStatusV2 = "budget_exhausted"
	TaskOutcomeUncertainV2 TaskStatusV2 = "outcome_uncertain"
)

type TaskErrorV2 struct {
	Category string `json:"category"`
	Stage    string `json:"stage"`
	Retry    string `json:"retry"`
	Detail   string `json:"detail,omitempty"`
}

type SideEffectStateV2 string

const (
	SideEffectNoneV2      SideEffectStateV2 = "none_observed"
	SideEffectConfirmedV2 SideEffectStateV2 = "confirmed"
	SideEffectUnknownV2   SideEffectStateV2 = "unknown"
)

type ArtifactV2 struct {
	ID        string `json:"id"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
}

type TaskResultV2 struct {
	Schema          string                `json:"schema"`
	TaskID          string                `json:"task_id"`
	ExecutionID     string                `json:"execution_id"`
	Status          TaskStatusV2          `json:"status"`
	StartedAt       time.Time             `json:"started_at"`
	EndedAt         time.Time             `json:"ended_at"`
	ModelProfile    string                `json:"model_profile"`
	ProfileRevision string                `json:"profile_revision"`
	Model           string                `json:"model,omitempty"`
	EffectiveLimits core.LimitsV2         `json:"effective_limits"`
	Result          json.RawMessage       `json:"result,omitempty"`
	PartialResult   json.RawMessage       `json:"partial_result,omitempty"`
	Validation      string                `json:"validation"`
	Summary         string                `json:"summary,omitempty"`
	Usage           core.RequestUsageV2   `json:"usage"`
	Budget          core.BudgetSnapshotV2 `json:"budget"`
	Error           *TaskErrorV2          `json:"error,omitempty"`
	SideEffectState SideEffectStateV2     `json:"side_effect_state"`
	Artifacts       []ArtifactV2          `json:"artifacts,omitempty"`
}

type ProfileV2 struct {
	Name          string        `json:"name"`
	Revision      string        `json:"revision"`
	Endpoint      string        `json:"endpoint"`
	Model         string        `json:"model"`
	CredentialRef string        `json:"credential_ref,omitempty"`
	Limits        core.LimitsV2 `json:"limits"`
}

type ProfileDescriptionV2 struct {
	Name         string   `json:"name"`
	Revision     string   `json:"revision"`
	Capabilities []string `json:"capabilities"`
}

type ResolvedProfileV2 struct {
	Profile    ProfileV2 `json:"profile"`
	Endpoint   string    `json:"endpoint"`
	Model      string    `json:"model"`
	Credential string    `json:"-"`
}

func (p ResolvedProfileV2) String() string {
	return fmt.Sprintf("ResolvedProfileV2{%s@%s}", p.Profile.Name, p.Profile.Revision)
}
func (p ResolvedProfileV2) GoString() string { return p.String() }

type CredentialResolverV2 interface {
	ResolveCredential(context.Context, string) (string, error)
}
type ProfileSourceV2 interface {
	LoadProfile(context.Context, string) (ProfileV2, error)
}
type ProfileResolverV2 interface {
	Resolve(context.Context, string) (ResolvedProfileV2, error)
}

type ReceiptStateV2 string

const (
	ReceiptAdmittedV2        ReceiptStateV2 = "admitted"
	ReceiptRunningV2         ReceiptStateV2 = "running"
	ReceiptSucceededV2       ReceiptStateV2 = "succeeded"
	ReceiptFailedV2          ReceiptStateV2 = "failed"
	ReceiptBlockedV2         ReceiptStateV2 = "blocked"
	ReceiptCancelledV2       ReceiptStateV2 = "cancelled"
	ReceiptBudgetExhaustedV2 ReceiptStateV2 = "budget_exhausted"
	ReceiptUncertainV2       ReceiptStateV2 = "outcome_uncertain"
	ReceiptExpiredV2         ReceiptStateV2 = "expired"
)

type ReceiptV2 struct {
	Owner         string         `json:"owner"`
	ExecutionID   string         `json:"execution_id"`
	TaskID        string         `json:"task_id"`
	RequestDigest string         `json:"request_digest"`
	State         ReceiptStateV2 `json:"state"`
	Result        *TaskResultV2  `json:"result,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	Artifacts     []ArtifactV2   `json:"artifacts,omitempty"`
}

var (
	ErrReceiptNotFoundV2    = errors.New("receipt not found")
	ErrReceiptConflictV2    = errors.New("receipt conflict")
	ErrReceiptCapacityV2    = errors.New("receipt capacity exceeded")
	ErrReceiptExpiredV2     = errors.New("receipt expired")
	ErrReceiptOwnerDeniedV2 = errors.New("receipt owner denied")
)

type ArtifactSinkV2 interface {
	PutArtifact(context.Context, string, string, []byte, string) (ArtifactV2, error)
}

// ReceiptStoreV2 owns durable receipt and artifact lifecycle. Close releases store resources and locks.
type ReceiptStoreV2 interface {
	ArtifactSinkV2
	Admit(context.Context, string, RunTaskV2Request, string) (ReceiptV2, bool, error)
	Get(context.Context, string, string) (ReceiptV2, error)
	Lookup(context.Context, string, string) (ReceiptV2, error)
	Finalize(context.Context, string, string, TaskResultV2) error
	ReadArtifact(context.Context, string, string, string, int64, int64) ([]byte, error)
	Cleanup(context.Context, time.Time) error
	Close() error
}

type TaskPolicyGuardV2 interface{ Check(context.Context) error }

type ExecutionRecordV2 struct {
	Owner           string
	ExecutionID     string
	TaskID          string
	Profile         string
	ProfileRevision string
	Model           string
	Limits          core.LimitsV2
	Usage           core.RequestUsageV2
	Budget          core.BudgetSnapshotV2
	StartedAt       time.Time
	EndedAt         time.Time
	Status          TaskStatusV2
	Error           *TaskErrorV2
	SideEffectState SideEffectStateV2
	Summary         string
	Result          json.RawMessage
	PartialResult   json.RawMessage
	Validation      string
	Artifacts       []ArtifactV2
}

var taskIDPatternV2 = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var stableErrorCategoriesV2 = map[string]bool{
	"invalid_input": true, "profile_missing": true, "disconnected": true, "pairing_changed": true, "tab_busy": true, "origin_denied": true, "login_required": true, "output_invalid": true, "provider_error": true, "deadline_exceeded": true, "capability_unsupported": true, "storage_error": true, "request_conflict": true,
}
var retryPoliciesV2 = map[string]bool{"never": true, "reconcile_only": true, "known_not_executed": true}

func ValidateTaskRequestV2(raw []byte) (RunTaskV2Request, error) {
	var r RunTaskV2Request
	if len(raw) > 65536 {
		return r, errInvalidV2("request exceeds 65536 bytes")
	}
	if !utf8Valid(raw) {
		return r, errInvalidV2("request is not valid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return r, errInvalidV2(err.Error())
	}
	if v, ok := fields["limits"]; ok && bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		return r, errInvalidV2("limits must be an object")
	}
	if err := d.Decode(&r); err != nil {
		return r, errInvalidV2(err.Error())
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return r, errInvalidV2("trailing JSON")
	}
	if r.Schema != "ferro.task/v2" || !validTaskIDV2(r.TaskID) || r.Goal == "" || r.ModelProfile == "" || r.Policy == nil || len(r.OutputSchema) == 0 || bytes.Equal(bytes.TrimSpace(r.OutputSchema), []byte("null")) {
		return r, errInvalidV2("missing or invalid required field")
	}
	if len(r.Goal) > 16384 || !utf8Valid([]byte(r.Goal)) {
		return r, errInvalidV2("goal exceeds 16384 bytes or is invalid UTF-8")
	}
	if len(r.TaskID) > 128 || !validTaskIDV2(r.ModelProfile) {
		return r, errInvalidV2("invalid task_id or model_profile")
	}
	if len(r.ReplayLabel) > 256 || !utf8Valid([]byte(r.ReplayLabel)) {
		return r, errInvalidV2("invalid replay_key")
	}
	if r.Policy.Mode != "read_only" || len(r.Policy.Origins) == 0 {
		return r, errInvalidV2("unsupported policy or empty origins")
	}
	origins, err := canonicalOriginsV2(r.Policy.Origins)
	if err != nil {
		return r, err
	}
	r.Policy.Origins = origins
	if r.StartURL != "" {
		u, err := url.Parse(r.StartURL)
		if err != nil || u.Opaque != "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return r, errInvalidV2("start_url must be a valid http(s) URL without userinfo")
		}
		origin, err := canonicalOriginV2(u.Scheme + "://" + u.Host)
		if err != nil {
			return r, err
		}
		i := sort.SearchStrings(origins, origin)
		if i == len(origins) || origins[i] != origin {
			return r, errInvalidV2("start_url origin is not permitted")
		}
	}
	if _, err := r.Limits.ApplyV2(core.DefaultLimitsV2()); err != nil {
		return r, errInvalidV2(err.Error())
	}
	if len(r.OutputSchema) > 32768 {
		return r, errInvalidV2("schema exceeds 32768 bytes")
	}
	if err := core.ValidateSchemaShapeV2(r.OutputSchema); err != nil {
		return r, errInvalidV2("invalid output schema: " + err.Error())
	}
	if r.Evidence == "" {
		r.Evidence = EvidenceCompactV2
	}
	if r.Evidence != EvidenceCompactV2 && r.Evidence != EvidenceArtifactsV2 {
		return r, errInvalidV2("invalid evidence mode")
	}
	return r, nil
}

func errInvalidV2(s string) error { return fmt.Errorf("invalid task v2: %s", s) }
func validTaskIDV2(s string) bool { return taskIDPatternV2.MatchString(s) }
func utf8Valid(b []byte) bool     { return strings.ToValidUTF8(string(b), "") == string(b) }

func canonicalOriginsV2(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, errInvalidV2("at least one exact origin is required")
	}
	set := map[string]bool{}
	for _, raw := range in {
		c, err := canonicalOriginV2(raw)
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

func canonicalOriginV2(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.Contains(u.Host, "*") {
		return "", errInvalidV2("origin must be exact http(s) origin")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errInvalidV2("invalid origin host")
	}
	if ip := net.ParseIP(host); ip == nil {
		host = strings.TrimSuffix(host, ".")
	}
	port := u.Port()
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", errInvalidV2("invalid origin port")
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

func ValidateTaskResultV2(r TaskResultV2) error {
	if r.Schema != "ferro.result/v2" || !validTaskIDV2(r.TaskID) || !validTaskIDV2(r.ExecutionID) || !validTaskIDV2(r.ModelProfile) || r.ProfileRevision == "" {
		return fmt.Errorf("invalid result identity")
	}
	if r.StartedAt.IsZero() || r.EndedAt.IsZero() || r.EndedAt.Before(r.StartedAt) {
		return fmt.Errorf("invalid result timestamps")
	}
	if r.Summary != "" && (!utf8Valid([]byte(r.Summary)) || len(r.Summary) > 2048) {
		return fmt.Errorf("invalid summary")
	}
	if err := r.EffectiveLimits.ValidateV2(); err != nil {
		return fmt.Errorf("invalid effective limits: %w", err)
	}
	for _, v := range []*int64{r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.TotalTokens, r.Usage.ReasoningTokens, r.Usage.CacheReadTokens, r.Usage.CacheWriteTokens, r.Usage.BilledMicroUSD} {
		if v != nil && *v < 0 {
			return fmt.Errorf("negative usage")
		}
	}
	if len(r.Result) > 16384 || len(r.PartialResult) > 16384 {
		return fmt.Errorf("inline result exceeds 16384 bytes")
	}
	if len(r.Result) > 0 && !json.Valid(r.Result) {
		return fmt.Errorf("invalid result JSON")
	}
	if len(r.PartialResult) > 0 && !json.Valid(r.PartialResult) {
		return fmt.Errorf("invalid partial result JSON")
	}
	if r.Validation != "valid" && r.Validation != "invalid" && r.Validation != "not_run" {
		return fmt.Errorf("invalid validation state")
	}
	switch r.Status {
	case TaskSucceededV2:
		if r.Validation != "valid" || len(r.Result) == 0 || len(r.PartialResult) > 0 || r.Error != nil {
			return fmt.Errorf("success requires valid result without error")
		}
	case TaskFailedV2, TaskBlockedV2, TaskCancelledV2, TaskBudgetExhaustedV2, TaskOutcomeUncertainV2:
		if len(r.Result) > 0 {
			return fmt.Errorf("failure cannot contain accepted result")
		}
	default:
		return fmt.Errorf("invalid task status")
	}
	if r.Error != nil {
		if !stableErrorCategoriesV2[r.Error.Category] || r.Error.Stage == "" || !retryPoliciesV2[r.Error.Retry] || len(r.Error.Detail) > 2048 || !utf8Valid([]byte(r.Error.Detail)) {
			return fmt.Errorf("invalid typed task error")
		}
		if r.SideEffectState == SideEffectUnknownV2 && r.Error.Retry != "never" && r.Error.Retry != "reconcile_only" {
			return fmt.Errorf("unknown side effects prohibit retry")
		}
	}
	if r.SideEffectState != SideEffectNoneV2 && r.SideEffectState != SideEffectConfirmedV2 && r.SideEffectState != SideEffectUnknownV2 {
		return fmt.Errorf("invalid side effect state")
	}
	if len(r.Artifacts) > 128 {
		return fmt.Errorf("too many artifacts")
	}
	for _, a := range r.Artifacts {
		if !validTaskIDV2(a.ID) || len(a.SHA256) != 64 || !isHexV2(a.SHA256) || a.Size < 0 || a.Size > 4<<20 || a.MediaType == "" || len(a.MediaType) > 128 {
			return fmt.Errorf("invalid artifact metadata")
		}
	}
	return nil
}
func isHexV2(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}
