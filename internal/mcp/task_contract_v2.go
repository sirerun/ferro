package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/dndungu/ferro/internal/core"
	"io"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
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
	Profile      string                `json:"profile"`
	Policy       *TaskPolicyV2         `json:"policy"`
	Limits       core.LimitOverridesV2 `json:"limits,omitempty"`
	OutputSchema json.RawMessage       `json:"output_schema,omitempty"`
	Evidence     EvidenceModeV2        `json:"evidence,omitempty"`
	ReplayLabel  string                `json:"replay_label,omitempty"`
}
type ArtifactV2 struct {
	ID        string `json:"id"`
	SHA256    string `json:"sha256"`
	MediaType string `json:"media_type"`
	Size      int64  `json:"size"`
}
type TaskResultV2 struct {
	Schema      string              `json:"schema"`
	ExecutionID string              `json:"execution_id"`
	TaskID      string              `json:"task_id"`
	Status      string              `json:"status"`
	Summary     string              `json:"summary,omitempty"`
	Output      json.RawMessage     `json:"output,omitempty"`
	Validated   bool                `json:"validated"`
	Usage       core.RequestUsageV2 `json:"usage"`
	Artifacts   []ArtifactV2        `json:"artifacts,omitempty"`
	ErrorCode   string              `json:"error_code,omitempty"`
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
	Name     string `json:"name"`
	Revision string `json:"revision"`
	Model    string `json:"model"`
}
type ResolvedProfileV2 struct {
	Profile    ProfileV2
	Endpoint   string
	Model      string
	Credential string
}
type CredentialResolverV2 interface {
	ResolveCredential(context.Context, string) (string, error)
}
type ProfileSourceV2 interface {
	LoadProfile(context.Context, string) (ProfileV2, error)
}
type ProfileResolverV2 interface {
	Resolve(context.Context, string) (ResolvedProfileV2, error)
}

// NewProfileResolverV2(source ProfileSourceV2, credentials CredentialResolverV2) (ProfileResolverV2,error) is implemented by L01.
type ReceiptStateV2 string

const (
	ReceiptAdmittedV2  ReceiptStateV2 = "admitted"
	ReceiptRunningV2   ReceiptStateV2 = "running"
	ReceiptSucceededV2 ReceiptStateV2 = "succeeded"
	ReceiptFailedV2    ReceiptStateV2 = "failed"
	ReceiptUncertainV2 ReceiptStateV2 = "outcome_uncertain"
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
type ReceiptStoreV2 interface {
	Admit(context.Context, string, RunTaskV2Request, string) (ReceiptV2, bool, error)
	Get(context.Context, string, string) (ReceiptV2, error)
	Lookup(context.Context, string, string) (ReceiptV2, error)
	Finalize(context.Context, string, string, TaskResultV2) error
	ReadArtifact(context.Context, string, string, int64, int64) ([]byte, error)
	Cleanup(context.Context, time.Time) error
}
type ArtifactSinkV2 interface {
	PutArtifact(context.Context, string, string, []byte, string) (ArtifactV2, error)
}
type TaskPolicyGuardV2 interface{ Check(context.Context) error }
type ExecutionRecordV2 struct {
	Owner, ExecutionID, TaskID, Profile string
	Limits                              core.LimitsV2
	Usage                               core.RequestUsageV2
	StartedAt, FinishedAt               time.Time
	ErrorCode, Summary                  string
	Output                              json.RawMessage
	Validated                           bool
	Partial                             json.RawMessage
}

// OpenReceiptStoreV2(directory string,maxBytes int64)(ReceiptStoreV2,error) is implemented by L07.
// NewTaskPolicyDriverV2(driver core.PageDriver,policy TaskPolicyV2,guard TaskPolicyGuardV2,budget core.BudgetControllerV2)(core.PageDriver,error) is implemented by L04.
// BuildTaskResultV2(ctx context.Context,record ExecutionRecordV2,sink ArtifactSinkV2)(TaskResultV2,error) is implemented by L08.
func ValidateTaskRequestV2(raw []byte) (RunTaskV2Request, error) {
	var r RunTaskV2Request
	if len(raw) > 65536 {
		return r, errInvalidV2("request exceeds 65536 bytes")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return r, errInvalidV2(err.Error())
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return r, errInvalidV2("trailing JSON")
	}
	if r.Schema != "ferro.task/v2" || r.TaskID == "" || r.Goal == "" || r.Profile == "" || r.Policy == nil {
		return r, errInvalidV2("missing or invalid required field")
	}
	if len(r.Goal) > 16384 {
		return r, errInvalidV2("goal exceeds 16384 bytes")
	}
	if !validTaskIDV2(r.TaskID) {
		return r, errInvalidV2("invalid task_id")
	}
	if r.Policy.Mode != "read_only" {
		return r, errInvalidV2("unsupported policy mode")
	}
	origins, err := canonicalOriginsV2(r.Policy.Origins)
	if err != nil {
		return r, err
	}
	r.Policy.Origins = origins
	if err := r.LimitsValidateV2(); err != nil {
		return r, err
	}
	if len(r.OutputSchema) > 32768 {
		return r, errInvalidV2("schema exceeds 32768 bytes")
	}
	if err := preflightSchemaV2(r.OutputSchema); err != nil {
		return r, err
	}
	if r.Evidence != "" && r.Evidence != EvidenceCompactV2 && r.Evidence != EvidenceArtifactsV2 {
		return r, errInvalidV2("invalid evidence mode")
	}
	return r, nil
}
func (r RunTaskV2Request) LimitsValidateV2() error {
	_, err := r.Limits.ApplyV2(core.DefaultLimitsV2())
	return err
}
func errInvalidV2(s string) error { return fmt.Errorf("invalid task v2: %s", s) }

var taskIDPatternV2 = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func validTaskIDV2(s string) bool { return taskIDPatternV2.MatchString(s) }
func canonicalOriginsV2(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		u, e := url.Parse(s)
		if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.Contains(u.Host, "*") {
			return nil, errInvalidV2("origin must be exact http(s) origin")
		}
		host := strings.ToLower(u.Hostname())
		if host == "" {
			return nil, errInvalidV2("invalid origin host")
		}
		if ip := net.ParseIP(host); ip == nil {
			host = strings.TrimSuffix(host, ".")
		}
		port := u.Port()
		if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
			port = ""
		}
		authority := host
		if strings.Contains(host, ":") {
			authority = "[" + host + "]"
		}
		if port != "" {
			authority += " isport " + port
			authority = strings.Replace(authority, " isport ", ":", 1)
		}
		c := u.Scheme + "://" + authority
		if !seen[c] {
			out = append(out, c)
			seen[c] = true
		}
	}
	sort.Strings(out)
	return out, nil
}
func preflightSchemaV2(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var root any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&root); err != nil {
		return errInvalidV2("invalid output schema")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errInvalidV2("trailing schema JSON")
	}
	var walk func(any, int) error
	walk = func(v any, depth int) error {
		if depth > 16 {
			return errInvalidV2("schema depth exceeds 16")
		}
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				switch k {
				case "$schema", "title", "description", "type", "properties", "items", "required", "additionalProperties", "enum", "minimum", "maximum", "minLength", "maxLength", "minItems", "maxItems":
				default:
					return errInvalidV2("unsupported schema keyword: " + k)
				}
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range x {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, 1); err != nil {
		return err
	}
	return nil
}
