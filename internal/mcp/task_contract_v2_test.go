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

func TestRequestV2RejectsInvalidUnknownTrailingAndUnsupportedSchema(t *testing.T) {
	base := `{"schema":"ferro.task/v2","task_id":"t-1","goal":"read","model_profile":"p","policy":{"mode":"read_only","origins":["https://EXAMPLE.com:443/"]},"output_schema":{"type":"object"}}`
	for _, tc := range []struct{ name, raw string }{{"valid", base}, {"unknown", base[:len(base)-1] + `,"x":1}`}, {"trailing", base + ` {}`}, {"unsupported schema keyword", strings.Replace(base, `"type":"object"`, `"$ref":"https://x"`, 1)}} {
		t.Run(tc.name, func(t *testing.T) {
			_, e := ValidateTaskRequestV2([]byte(tc.raw))
			if tc.name == "valid" && e != nil {
				t.Fatal(e)
			}
			if tc.name != "valid" && e == nil {
				t.Fatal("invalid accepted")
			}
		})
	}
}
func TestRequestV2LimitZeroAndEnum(t *testing.T) {
	base := `{"schema":"ferro.task/v2","task_id":"t","goal":"g","model_profile":"p","policy":{"mode":"read_only","origins":["https://example.com"]},"output_schema":{"type":"object"},"limits":{"actions":0}}`
	if _, e := ValidateTaskRequestV2([]byte(base)); e == nil {
		t.Fatal("explicit zero limit accepted")
	}
	raw := []byte(strings.Replace(base, `"limits":{"actions":0}`, `"evidence":"huge"`, 1))
	if _, e := ValidateTaskRequestV2(raw); e == nil {
		t.Fatal("invalid evidence accepted")
	}
}

func TestRequestV2RejectsOverflowAndMalformedTrailing(t *testing.T) {
	for _, raw := range []string{
		`{"schema":"ferro.task/v2","task_id":"t","goal":"g","model_profile":"p","policy":{"mode":"read_only","origins":["https://example.com"]},"output_schema":{"type":"object"},"limits":{"actions":9223372036854775808}}`,
		`{"schema":"ferro.task/v2","task_id":"t","goal":"g","model_profile":"p","policy":{"mode":"read_only","origins":["https://example.com"]},"output_schema":{"type":"object"}} {`,
	} {
		if _, err := ValidateTaskRequestV2([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid input: %s", raw)
		}
	}
}
func TestReceiptFixtureJSONRoundTrip(t *testing.T) {
	r := ReceiptV2{TaskID: "t", State: ReceiptUncertainV2}
	b, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	var got ReceiptV2
	if e = json.Unmarshal(b, &got); e != nil || got.State != ReceiptUncertainV2 {
		t.Fatalf("%s %v", b, e)
	}
}

func TestRequestV2WireRegressions(t *testing.T) {
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
			_, err := ValidateTaskRequestV2([]byte(tc.raw))
			if (tc.name == "valid new profile field") == (err != nil) {
				t.Fatalf("unexpected validation result: %v", err)
			}
		})
	}
}

func TestRequestV2CanonicalOriginsAndStartURL(t *testing.T) {
	raw := `{"schema":"ferro.task/v2","task_id":"task_1","goal":"read","start_url":"https://[2001:DB8::1]:443/path?q=1","model_profile":"profile_1","policy":{"mode":"read_only","origins":["https://[2001:db8::1]/"]},"output_schema":{"type":"object"}}`
	r, err := ValidateTaskRequestV2([]byte(raw))
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
	if _, err = ValidateTaskRequestV2([]byte(raw)); err == nil {
		t.Fatal("origin mismatch accepted")
	}
}

func TestResolvedProfileCredentialIsNeverSerialized(t *testing.T) {
	b, err := json.Marshal(ResolvedProfileV2{Credential: "secret-token"})
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

func TestContractFixturesV2(t *testing.T) {
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
			_, err = ValidateTaskRequestV2(raw)
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
			var result TaskResultV2
			if err = json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			err = ValidateTaskResultV2(result)
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
		var p ProfileV2
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
		var receipt ReceiptV2
		if err = json.Unmarshal(raw, &receipt); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		var roundTrip ReceiptV2
		if err = json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatal(err)
		}
		if roundTrip.Owner != receipt.Owner || roundTrip.State != ReceiptUncertainV2 || roundTrip.TaskID != receipt.TaskID {
			t.Fatalf("receipt fixture changed: %s", encoded)
		}
	})
	t.Run("usage unknown differs from zero", func(t *testing.T) {
		raw, err := os.ReadFile(filepath.Join(root, "usage.json"))
		if err != nil {
			t.Fatal(err)
		}
		var usage core.RequestUsageV2
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
		var roundTrip core.RequestUsageV2
		if err = json.Unmarshal(encoded, &roundTrip); err != nil {
			t.Fatal(err)
		}
		if roundTrip.InputTokens == nil || *roundTrip.InputTokens != 0 || roundTrip.OutputTokens != nil {
			t.Fatalf("usage roundtrip lost unknown-vs-zero distinction: %s", encoded)
		}
	})
}

func TestTaskResultV2ArtifactBackedSuccess(t *testing.T) {
	result := validTaskResultV2()
	result.Result = nil
	result.ResultArtifactID = "result-large"
	result.Artifacts = []ArtifactV2{{ID: "result-large", SHA256: strings.Repeat("a", 64), MediaType: "application/json", Size: 16385}}
	if err := ValidateTaskResultV2(result); err != nil {
		t.Fatalf("valid artifact-backed success rejected: %v", err)
	}
}

func TestTaskResultV2RejectsInvalidArtifactAndResultCombinations(t *testing.T) {
	base := validTaskResultV2()
	base.Result = nil
	base.ResultArtifactID = "result-large"
	base.Artifacts = []ArtifactV2{{ID: "result-large", SHA256: strings.Repeat("a", 64), MediaType: "application/json", Size: 16385}}
	for _, tc := range []struct {
		name string
		edit func(*TaskResultV2)
	}{
		{"missing reference", func(r *TaskResultV2) { r.Artifacts = nil }},
		{"duplicate reference", func(r *TaskResultV2) { r.Artifacts = append(r.Artifacts, r.Artifacts[0]) }},
		{"inline and artifact", func(r *TaskResultV2) { r.Result = json.RawMessage(`{}`) }},
		{"artifact too small", func(r *TaskResultV2) { r.Artifacts[0].Size = 16384 }},
		{"artifact wrong media type", func(r *TaskResultV2) { r.Artifacts[0].MediaType = "text/plain" }},
		{"failure with artifact reference", func(r *TaskResultV2) { r.Status = TaskFailedV2; r.ResultArtifactID = "result-large" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			r.Artifacts = append([]ArtifactV2(nil), base.Artifacts...)
			tc.edit(&r)
			if err := ValidateTaskResultV2(r); err == nil {
				t.Fatal("invalid artifact/result combination accepted")
			}
		})
	}
}

func TestTaskResultV2RejectsNegativeBudgetAndMonetaryAmounts(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*TaskResultV2)
	}{
		{"negative counter", func(r *TaskResultV2) { r.Budget.Actions = -1 }},
		{"negative reported usage", func(r *TaskResultV2) { v := int64(-1); r.Budget.ReportedUsage.InputTokens = &v }},
		{"negative reserved money", func(r *TaskResultV2) { v := int64(-1); r.Budget.ReservedMicroUSD = &v }},
		{"negative billed money", func(r *TaskResultV2) { v := int64(-1); r.Budget.ReportedUsage.BilledMicroUSD = &v }},
		{"unsupported currency", func(r *TaskResultV2) { r.Budget.Currency = "EUR" }},
		{"empty currency with monetary value", func(r *TaskResultV2) { v := int64(0); r.Budget.ReservedMicroUSD = &v }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := validTaskResultV2()
			tc.edit(&r)
			if err := ValidateTaskResultV2(r); err == nil {
				t.Fatal("invalid budget accepted")
			}
		})
	}
}

func TestTaskResultV2PreservesUnknownAndZeroBudgetMoney(t *testing.T) {
	r := validTaskResultV2()
	r.Budget.Currency = "USD"
	zero := int64(0)
	r.Budget.ReservedMicroUSD = &zero
	if err := ValidateTaskResultV2(r); err != nil {
		t.Fatalf("zero monetary amount with USD rejected: %v", err)
	}
	if r.Budget.UnresolvedMicroUSD != nil || r.Budget.ReportedUsage.BilledMicroUSD != nil {
		t.Fatal("validation changed unknown monetary amounts")
	}
}

func TestTaskResultV2AllowsTruthfulUsageOverrun(t *testing.T) {
	r := validTaskResultV2()
	r.Budget.Actions = r.EffectiveLimits.Actions + 1
	used := r.EffectiveLimits.MaxInputTokens + 1
	r.Budget.ReportedUsage.InputTokens = &used
	if err := ValidateTaskResultV2(r); err != nil {
		t.Fatalf("truthful usage overrun rejected: %v", err)
	}
}

func TestTaskResultV2RejectsInvalidUTF8InResultAndArtifactMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*TaskResultV2)
	}{
		{"result", func(r *TaskResultV2) { r.Result = json.RawMessage{'{', '"', 0xff, '"', ':', '1', '}'} }},
		{"partial result", func(r *TaskResultV2) {
			r.Status = TaskFailedV2
			r.Result = nil
			r.PartialResult = json.RawMessage{'{', '"', 0xff, '"', ':', '1', '}'}
		}},
		{"artifact media type", func(r *TaskResultV2) {
			r.Artifacts = []ArtifactV2{{ID: "a", SHA256: strings.Repeat("a", 64), MediaType: string([]byte{0xff}), Size: 1}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := validTaskResultV2()
			tc.edit(&r)
			if err := ValidateTaskResultV2(r); err == nil {
				t.Fatal("invalid UTF-8 accepted")
			}
		})
	}
}

func validTaskResultV2() TaskResultV2 {
	return TaskResultV2{
		Schema: "ferro.result/v2", TaskID: "task", ExecutionID: "exec", Status: TaskSucceededV2,
		StartedAt: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC), EndedAt: time.Date(2026, 9, 24, 12, 0, 1, 0, time.UTC),
		ModelProfile: "profile", ProfileRevision: "revision", EffectiveLimits: core.DefaultLimitsV2(),
		Result: json.RawMessage(`{}`), Validation: "valid", SideEffectState: SideEffectNoneV2,
	}
}
