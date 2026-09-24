package core

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildSelectorUsesBackendSnapshotSelector(t *testing.T) {
	e := Element{Tag: "button", Name: "Save", Selector: "body > main > button:nth-of-type(2)"}
	got := buildSelector(e)
	if !strings.HasPrefix(got, "ferro-target:") {
		t.Fatalf("buildSelector() = %q, want a verified target wrapper", got)
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(got, "ferro-target:"))
	if err != nil {
		t.Fatal(err)
	}
	var target struct {
		Selector string `json:"selector"`
		Tag      string `json:"tag"`
		Name     string `json:"name"`
	}
	if err := json.Unmarshal(payload, &target); err != nil {
		t.Fatal(err)
	}
	if target.Selector != e.Selector || target.Tag != e.Tag || target.Name != e.Name {
		t.Fatalf("target = %+v, want selector/tag/name from snapshot %+v", target, e)
	}
}

func TestSnapshotSelectorIsInternalOnly(t *testing.T) {
	var e Element
	if err := json.Unmarshal([]byte(`{"ref":1,"tag":"button","selector":"body > button:nth-of-type(2)"}`), &e); err != nil {
		t.Fatal(err)
	}
	if e.Selector == "" {
		t.Fatal("extension selector was not decoded")
	}
	encoded, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "selector") {
		t.Fatalf("internal selector leaked from snapshot: %s", encoded)
	}
}
