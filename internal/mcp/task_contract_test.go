package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dndungu/ferro/internal/core"
)

func TestRequestRejectsInvalidUnknownTrailingAndUnsupportedSchema(t *testing.T) {
	base := `{"schema":"ferro.task/v2","task_id":"t-1","goal":"read","model_profile":"p","policy":{"mode":"read_only","origins":["https://EXAMPLE.com:443/"]},"output_schema":{"type":"object"}}`
	for _, tc := range []struct{ name, raw string }{{"valid", base}, {"unknown", base[:len(base)-1] + `,"x":1}`}, {"trailing", base + ` {}`}, {"unsupported schema keyword", strings.Replace(base, `"type":"object"`, `"$ref":"https://x"`, 1)}} {
		t.Run(tc.name, func(t *testing.T) {
			_, e := ValidateTaskRequest([]byte(tc.raw))
			if tc.name == "valid" && e != nil {
				t.Fatal(e)
			}
			if tc.name != "valid" && e == nil {
				t.Fatal("invalid accepted")
			}
		})
	}
}

func TestTaskPolicyDefaultsToReadWrite(t *testing.T) {
	raw := `{"task_id":"task_default_mode","goal":"search and read results","policy":{"origins":["https://example.com"]},"output_schema":{"type":"object"}}`
	request, err := ValidateTaskRequest([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if request.Policy.Mode != "read_write" {
		t.Fatalf("default mode=%q, want read_write", request.Policy.Mode)
	}
	if request.ModelProfile != "legacy-mcp" {
		t.Fatalf("default model profile=%q, want legacy-mcp", request.ModelProfile)
	}
}
func TestRequestLimitZeroAndEnum(t *testing.T) {
	base := `{"schema":"ferro.task/v2","task_id":"t","goal":"g","model_profile":"p","policy":{"mode":"read_only","origins":["https://example.com"]},"output_schema":{"type":"object"},"limits":{"actions":0}}`
	if _, e := ValidateTaskRequest([]byte(base)); e == nil {
		t.Fatal("explicit zero limit accepted")
	}
	raw := []byte(strings.Replace(base, `"limits":{"actions":0}`, `"evidence":"huge"`, 1))
	if _, e := ValidateTaskRequest(raw); e == nil {
		t.Fatal("invalid evidence accepted")
	}
}

func TestRequestRejectsOverflowAndMalformedTrailing(t *testing.T) {
	for _, raw := range []string{
		`{"schema":"ferro.task/v2","task_id":"t","goal":"g","model_profile":"p","policy":{"mode":"read_only","origins":["https://example.com"]},"output_schema":{"type":"object"},"limits":{"actions":9223372036854775808}}`,
		`{"schema":"ferro.task/v2","task_id":"t","goal":"g","model_profile":"p","policy":{"mode":"read_only","origins":["https://example.com"]},"output_schema":{"type":"object"}} {`,
	} {
		if _, err := ValidateTaskRequest([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid input: %s", raw)
		}
	}
}
func TestReceiptFixtureJSONRoundTrip(t *testing.T) {
	r := Receipt{TaskID: "t", State: ReceiptUncertain}
	b, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	var got Receipt
	if e = json.Unmarshal(b, &got); e != nil || got.State != ReceiptUncertain {
		t.Fatalf("%s %v", b, e)
	}
}

func TestRequestWireRegressions(t *testing.T) {
	base := `{"schema":"ferro.task/v2","task_id":"t-1","goal":"read","model_profile":"p","policy":{"mode":"read_only","origins":["https://example.com"]},"output_schema":{"type":"object"}}`
	tests := []struct {
		name string
		raw  string
	}{
		{"valid new profile field", base},
		{"missing output schema", `{"schema":"ferro.task/v2","task_id":"t-1","goal":"read","model_profile":"p","policy":{"mode":"read_only","origins":["https://example.com"]}}`},
		{"wrong schema keyword type", strings.Replace(base, `"type":"object"`, `"type":7`, 1)},
		{"invalid UTF8", string(append([]byte(base[:len(base)-1]), 0xff, '}'))},
		{"origin mismatch start url", strings.Replace(base, `"output_schema"`, `"start_url":"https://elsewhere.example","output_schema"`, 1)},
		{"trailing malformed bytes", base + ` {`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateTaskRequest([]byte(tc.raw))
			if (tc.name == "valid new profile field") == (err != nil) {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}

func TestRequestCanonicalOriginsAndStartURL(t *testing.T) {
	raw := `{"schema":"ferro.task/v2","task_id":"task_1","goal":"read","start_url":"https://[2001:DB8::1]:443/path?q=1","model_profile":"profile_1","policy":{"mode":"read_only","origins":["https://[2001:db8::1]/"]},"output_schema":{"type":"object"}}`
	r, err := ValidateTaskRequest([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Policy.Origins) != 1 || r.Policy.Origins[0] != "https://[2001:db8::1]" {
		t.Fatalf("unexpected canonical origins: %#v", r.Policy.Origins)
	}
	mutated := strings.Replace(raw, `"start_url":"https://[2001:DB8::1]:443/path?q=1"`, `"start_url":"https://[::1]:443/path?q=1"`, 1)
	if mutated == raw {
		t.Fatal("failed to mutate start_url")
	}
	raw = mutated
	if _, err = ValidateTaskRequest([]byte(raw)); err == nil {
		t.Fatal("origin mismatch accepted")
	}
}

func TestResolvedProfileCredentialIsNeverSerialized(t *testing.T) {
	b, err := json.Marshal(ResolvedProfile{Credential: "secret-token"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret-token") || strings.Contains(string(b), "credential") {
		t.Fatalf("credential leaked: %s", b)
	}
}

func TestLegacyRunTaskArgsSerializationRemainsStable(t *testing.T) {
	got, err := json.Marshal(RunTaskArgs{Goal: "read"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), `{"goal":"read"`) {
		t.Fatalf("legacy wire changed: %s", got)
	}
}

func TestContractFixtures(t *testing.T) {
	root := filepath.Join("..", "..", "docs", "contracts", "bulk-v2")
	requestFiles, err := filepath.Glob(filepath.Join(root, "request-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range requestFiles {
		name := filepath.Base(path)
		wantValid := name == "request-valid.json"
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ValidateTaskRequest(raw)
			if (err == nil) != wantValid {
				t.Fatalf("valid=%v, err=%v", wantValid, err)
			}
		})
	}
	for _, item := range []struct {
		file  string
		valid bool
	}{{"result-success.json", true}, {"result-failure.json", true}} {
		t.Run(item.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, item.file))
			if err != nil {
				t.Fatal(err)
			}
			var result TaskResult
			if err = json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			err = ValidateTaskResult(result)
			if (err == nil) != item.valid {
				t.Fatalf("valid=%v, err=%v", item.valid, err)
			}
		})
	}
	t.Run("profile credential reference roundtrip", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join(root, "profile.json"))
		if err != nil {
			t.Fatal(err)
		}
		var p Profile
		if err = json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if p.CredentialRef == "" || !json.Valid(encoded) {
			t.Fatalf("profile fixture lost private credential reference: %s", encoded)
		}
	})
	t.Run("receipt fixture roundtrip", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join(root, "receipt.json"))
		if err != nil {
			t.Fatal(err)
		}
		var receipt Receipt
		if err = json.Unmarshal(raw, &receipt); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		var roundTrip Receipt
		if err = json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatal(err)
		}
		if roundTrip.Owner != receipt.Owner || roundTrip.State != ReceiptUncertain || roundTrip.TaskID != receipt.TaskID {
			t.Fatalf("receipt fixture changed: %s", encoded)
		}
	})
	t.Run("usage unknown differs from zero", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join(root, "usage.json"))
		if err != nil {
			t.Fatal(err)
		}
		var usage core.RequestUsage
		if err = json.Unmarshal(raw, &usage); err != nil {
			t.Fatal(err)
		}
		if usage.InputTokens == nil || *usage.InputTokens != 0 || usage.OutputTokens != nil || usage.TotalTokens == nil || *usage.TotalTokens != 0 {
			t.Fatalf("usage presence lost: %+v", usage)
		}
		encoded, err := json.Marshal(usage)
		if err != nil {
			t.Fatal(err)
		}
		var roundTrip core.RequestUsage
		if err = json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatal(err)
		}
		if roundTrip.InputTokens == nil || *roundTrip.InputTokens != 0 || roundTrip.OutputTokens != nil {
			t.Fatalf("usage roundtrip lost unknown-vs-zero distinction: %s", encoded)
		}
	})
}

func TestTaskResultArtifactBackedSuccess(t *testing.T) {
	result := validTaskResult()
	result.Result = nil
	result.ResultArtifactID = "result-large"
	result.Artifacts = []Artifact{{ID: "result-large", SHA256: strings.Repeat("a", 64), MediaType: "application/json", Size: 16385}}
	if err := ValidateTaskResult(result); err != nil {
		t.Fatalf("valid artifact-backed success rejected: %v", err)
	}
}

func TestTaskResultRejectsInvalidArtifactAndResultCombinations(t *testing.T) {
	base := validTaskResult()
	base.Result = nil
	base.ResultArtifactID = "result-large"
	base.Artifacts = []Artifact{{ID: "result-large", SHA256: strings.Repeat("a", 64), MediaType: "application/json", Size: 16385}}
	for _, tc := range []struct {
		name string
		edit func(*TaskResult)
	}{
		{"missing reference", func(r *TaskResult) { r.Artifacts = nil }},
		{"duplicate reference", func(r *TaskResult) { r.Artifacts = append(r.Artifacts, r.Artifacts[0]) }},
		{"inline and artifact", func(r *TaskResult) { r.Result = json.RawMessage(`{}`) }},
		{"artifact too small", func(r *TaskResult) { r.Artifacts[0].Size = 16384 }},
		{"artifact wrong media type", func(r *TaskResult) { r.Artifacts[0].MediaType = "text/plain" }},
		{"failure with artifact reference", func(r *TaskResult) { r.Status = TaskFailed; r.ResultArtifactID = "result-large" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			r.Artifacts = append([]Artifact(nil), base.Artifacts...)
			tc.edit(&r)
			if err := ValidateTaskResult(r); err == nil {
				t.Fatal("invalid artifact/result combination accepted")
			}
		})
	}
}

func TestTaskResultRejectsNegativeBudgetAndMonetaryAmounts(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*TaskResult)
	}{
		{"negative counter", func(r *TaskResult) { r.Budget.Actions = -1 }},
		{"negative reported usage", func(r *TaskResult) { v := int64(-1); r.Budget.ReportedUsage.InputTokens = &v }},
		{"negative reserved money", func(r *TaskResult) { v := int64(-1); r.Budget.ReservedMicroUSD = &v }},
		{"negative billed money", func(r *TaskResult) { v := int64(-1); r.Budget.ReportedUsage.BilledMicroUSD = &v }},
		{"unsupported currency", func(r *TaskResult) { r.Budget.Currency = "EUR" }},
		{"empty currency with monetary value", func(r *TaskResult) { v := int64(0); r.Budget.ReservedMicroUSD = &v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := validTaskResult()
			tc.edit(&r)
			if err := ValidateTaskResult(r); err == nil {
				t.Fatal("invalid budget accepted")
			}
		})
	}
}

func TestTaskResultPreservesUnknownAndZeroBudgetMoney(t *testing.T) {
	r := validTaskResult()
	r.Budget.Currency = "USD"
	zero := int64(0)
	r.Budget.ReservedMicroUSD = &zero
	if err := ValidateTaskResult(r); err != nil {
		t.Fatalf("zero monetary amount with USD rejected: %v", err)
	}
	if r.Budget.UnresolvedMicroUSD != nil || r.Budget.ReportedUsage.BilledMicroUSD != nil {
		t.Fatal("validation changed unknown monetary amounts")
	}
}

func TestTaskResultAllowsTruthfulUsageOverrun(t *testing.T) {
	r := validTaskResult()
	r.Budget.Actions = r.EffectiveLimits.Actions + 1
	used := r.EffectiveLimits.MaxInputTokens + 1
	r.Budget.ReportedUsage.InputTokens = &used
	if err := ValidateTaskResult(r); err != nil {
		t.Fatalf("truthful usage overrun rejected: %v", err)
	}
}

func TestTaskResultRejectsInvalidUTF8InResultAndArtifactMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*TaskResult)
	}{
		{"result", func(r *TaskResult) { r.Result = json.RawMessage{'{', '"', 0xff, '"', ':', '1', '}'} }},
		{"partial result", func(r *TaskResult) {
			r.Status = TaskFailed
			r.Result = nil
			r.PartialResult = json.RawMessage{'{', '"', 0xff, '"', ':', '1', '}'}
		}},
		{"artifact media type", func(r *TaskResult) {
			r.Artifacts = []Artifact{{ID: "a", SHA256: strings.Repeat("a", 64), MediaType: string([]byte{0xff}), Size: 1}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := validTaskResult()
			tc.edit(&r)
			if err := ValidateTaskResult(r); err == nil {
				t.Fatal("invalid UTF-8 accepted")
			}
		})
	}
}

func validTaskResult() TaskResult {
	return TaskResult{
		Schema: "ferro.result/v2", TaskID: "task", ExecutionID: "exec", Status: TaskSucceeded,
		StartedAt: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC), EndedAt: time.Date(2026, 9, 24, 12, 0, 1, 0, time.UTC),
		ModelProfile: "profile", ProfileRevision: "revision", EffectiveLimits: core.DefaultLimits(),
		Result: json.RawMessage(`{}`), Validation: "valid", SideEffectState: SideEffectNone,
	}
}
