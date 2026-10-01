package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSchema_Preflight(t *testing.T) {
	valid := json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`)
	if err := PreflightSchema(valid); err != nil {
		t.Fatalf("valid supported schema rejected: %v", err)
	}
	unsupported := json.RawMessage(`{"type":"object","$ref":"https://example.test/schema.json"}`)
	if err := PreflightSchema(unsupported); err == nil {
		t.Fatal("schema with unsupported keyword passed preflight")
	}
	if err := ValidateResult(unsupported, map[string]any{"title": "A"}); err == nil {
		t.Fatal("a matching result bypassed schema preflight")
	}
}

func TestSchema_RejectRemoteRef(t *testing.T) {
	remote := json.RawMessage(`{"$ref":"https://example.test/schema.json"}`)
	if err := PreflightSchema(remote); err == nil {
		t.Fatal("remote reference schema was accepted")
	}
	if err := ValidateResult(remote, map[string]any{}); err == nil {
		t.Fatal("result validation resolved or accepted a remote reference")
	}
}

func TestSchema_DepthLimit(t *testing.T) {
	if err := PreflightSchema(nestedSchema(t, 7)); err != nil {
		t.Fatalf("schema at the existing depth boundary rejected: %v", err)
	}
	if err := PreflightSchema(nestedSchema(t, 8)); err == nil {
		t.Fatal("schema beyond the depth limit was accepted")
	}
}

func TestSchema_InvalidFinal(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`)
	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "wrong field type", value: map[string]any{"title": 7}},
		{name: "missing required field", value: map[string]any{}},
		{name: "extra field", value: map[string]any{"title": "ok", "secret": "unexpected"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateResult(schema, tc.value); err == nil {
				t.Fatal("invalid final result was accepted")
			}
		})
	}
	if err := ValidateResult(schema, map[string]any{"title": "valid"}); err != nil {
		t.Fatalf("valid final result rejected: %v", err)
	}
}

func TestSchema_PartialNotSuccess(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"url":{"type":"string"}},"required":["title","url"],"additionalProperties":false}`)
	partial := map[string]any{"title": "Observed title"}
	if err := ValidateResult(schema, partial); err == nil {
		t.Fatal("partial result missing a required final field was accepted")
	}
	final := map[string]any{"title": "Observed title", "url": "https://example.test/"}
	if err := ValidateResult(schema, final); err != nil {
		t.Fatalf("complete final result rejected: %v", err)
	}
}

func nestedSchema(t *testing.T, wrappers int) json.RawMessage {
	t.Helper()
	var node = map[string]any{"type": "string"}
	for range wrappers {
		node = map[string]any{"properties": map[string]any{"child": node}}
	}
	raw, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal test schema: %v", err)
	}
	return raw
}

func TestSchema_SizeLimit(t *testing.T) {
	tooLarge := json.RawMessage(`{"description":"` + strings.Repeat("x", 32*1024) + `"}`)
	if err := PreflightSchema(tooLarge); err == nil {
		t.Fatal("schema above the byte limit was accepted")
	}
}
