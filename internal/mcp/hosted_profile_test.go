package mcp

import (
	"context"
	"testing"
	"time"
)

func TestDeploymentDeadlineChangesEffectiveProfileAndRevision(t *testing.T) {
	cfg := Config{LLMBaseURL: "https://api.example.com/v1", LLMModel: "configured-model"}
	normal, err := (&Owner{cfg: cfg}).resolveTaskProfileV2(context.Background(), "legacy-mcp")
	if err != nil {
		t.Fatal(err)
	}
	cfg.BlockTimeout = 45 * time.Second
	bounded, err := (&Owner{cfg: cfg}).resolveTaskProfileV2(context.Background(), "legacy-mcp")
	if err != nil {
		t.Fatal(err)
	}
	if normal.Profile.Limits.RuntimeMS != 90000 || bounded.Profile.Limits.RuntimeMS != 45000 || normal.Profile.Revision == bounded.Profile.Revision {
		t.Fatalf("deadline not reflected in profile: normal=%+v bounded=%+v", normal.Profile, bounded.Profile)
	}
}
