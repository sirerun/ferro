package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildSelectorUsesBackendSnapshotSelector(t *testing.T) {
	e := Element{Tag: "button", Name: "Save", Selector: "body > main > button:nth-of-type(2)"}
	if got := buildSelector(e); got != e.Selector {
		t.Fatalf("buildSelector() = %q, want snapshot selector %q", got, e.Selector)
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
