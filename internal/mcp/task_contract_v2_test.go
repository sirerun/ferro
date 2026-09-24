package mcp

import (
	"encoding/json"
	"testing"
)

func TestRequestV2RejectsInvalidUnknownTrailingAndUnsupportedSchema(t *testing.T) {
	base := `{"schema":"ferro.task/v2","task_id":"t-1","goal":"read","profile":"p","policy":{"mode":"read_only","origins":["https://EXAMPLE.com:443/"]}}`
	for _, tc := range []struct{ name, raw string }{{"valid", base}, {"unknown", base[:len(base)-1] + `,"x":1}`}, {"trailing", base + ` {}`}, {"zero schema", `{"schema":"ferro.task/v2","task_id":"t","goal":"g","profile":"p","policy":{"mode":"read_only","origins":[]},"output_schema":{"$ref":"https://x"}}`}} {
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
	raw := []byte(`{"schema":"ferro.task/v2","task_id":"t","goal":"g","profile":"p","policy":{"mode":"read_only","origins":[]},"limits":{"actions":0},"evidence":"compact"}`)
	r, e := ValidateTaskRequestV2(raw)
	if e != nil {
		t.Fatal(e)
	}
	if r.Limits.Actions == nil || *r.Limits.Actions != 0 {
		t.Fatal("explicit zero lost")
	}
	raw = []byte(`{"schema":"ferro.task/v2","task_id":"t","goal":"g","profile":"p","policy":{"mode":"read_only","origins":[]},"evidence":"huge"}`)
	if _, e = ValidateTaskRequestV2(raw); e == nil {
		t.Fatal("invalid evidence accepted")
	}
}

func TestRequestV2RejectsOverflowAndMalformedTrailing(t *testing.T) {
	for _, raw := range []string{
		`{"schema":"ferro.task/v2","task_id":"t","goal":"g","profile":"p","policy":{"mode":"read_only","origins":[]},"limits":{"actions":9223372036854775808}}`,
		`{"schema":"ferro.task/v2","task_id":"t","goal":"g","profile":"p","policy":{"mode":"read_only","origins":[]}} {`,
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
