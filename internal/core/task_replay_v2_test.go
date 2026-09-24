package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func replayContextFixtureV2() ReplayContextV2 {
	return ReplayContextV2{
		Principal: "account-1", ProfileRevision: "profile-revision-1", Model: "model-1",
		PolicyDigest: "policy-digest-1", SchemaDigest: "schema-digest-1",
		Compatibility: "compatibility-1", Layout: "layout-1", CallerLabel: "caller-label-1",
	}
}

func TestReplayV2_Deterministic(t *testing.T) {
	ctx := replayContextFixtureV2()
	first, err := ReplayIdentityV2(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReplayIdentityV2(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != sha256.Size*2 || first != strings.ToLower(first) {
		t.Fatalf("identity is not deterministic lowercase SHA-256: first=%q second=%q", first, second)
	}
	encoded, err := json.Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(encoded)
	if first != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("identity=%q, want SHA-256 of frozen JSON encoding %q", first, hex.EncodeToString(wantHash[:]))
	}
}

func TestReplayV2_AccountPartition(t *testing.T) {
	base := replayContextFixtureV2()
	want, err := ReplayIdentityV2(base)
	if err != nil {
		t.Fatal(err)
	}
	base.Principal = "account-2"
	got, err := ReplayIdentityV2(base)
	if err != nil {
		t.Fatal(err)
	}
	if got == want {
		t.Fatal("different authenticated principals shared a replay identity")
	}
}

func TestReplayV2_PolicyPartition(t *testing.T) {
	base := replayContextFixtureV2()
	base.PolicyDigest = "sha256:sorted-mode-and-canonical-origins"
	first, err := ReplayIdentityV2(base)
	if err != nil {
		t.Fatal(err)
	}
	// The caller supplies the digest of normalized policy data. Equivalent
	// policies therefore have identical identity inputs after normalization.
	second, err := ReplayIdentityV2(base)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("same normalized policy digest produced different identity: %q != %q", first, second)
	}
	base.PolicyDigest = "sha256:different-origins-or-mode"
	third, err := ReplayIdentityV2(base)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatal("changed policy partition reused replay identity")
	}
}

func TestReplayV2_SchemaPartition(t *testing.T) {
	base := replayContextFixtureV2()
	want, err := ReplayIdentityV2(base)
	if err != nil {
		t.Fatal(err)
	}
	base.SchemaDigest = "schema-digest-2"
	got, err := ReplayIdentityV2(base)
	if err != nil {
		t.Fatal(err)
	}
	if got == want {
		t.Fatal("changed output schema reused replay identity")
	}
}

func TestReplayV2_MissingPartition(t *testing.T) {
	fields := []struct {
		name string
		set  func(*ReplayContextV2)
	}{
		{"principal", func(c *ReplayContextV2) { c.Principal = "" }},
		{"profile revision", func(c *ReplayContextV2) { c.ProfileRevision = "" }},
		{"model", func(c *ReplayContextV2) { c.Model = "" }},
		{"policy digest", func(c *ReplayContextV2) { c.PolicyDigest = "" }},
		{"schema digest", func(c *ReplayContextV2) { c.SchemaDigest = "" }},
		{"compatibility", func(c *ReplayContextV2) { c.Compatibility = "" }},
		{"layout", func(c *ReplayContextV2) { c.Layout = "" }},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			ctx := replayContextFixtureV2()
			field.set(&ctx)
			key, err := ReplayIdentityV2(ctx)
			if err == nil || key != "" {
				t.Fatalf("missing %s accepted: key=%q err=%v", field.name, key, err)
			}
		})
	}
	ctx := ReplayContextV2{}
	if key, err := ReplayIdentityV2(ctx); err != nil || key != "" {
		t.Fatalf("empty caller label must disable replay without error: key=%q err=%v", key, err)
	}
}

func TestReplayV2_AllPartitionFieldsChangeIdentity(t *testing.T) {
	base := replayContextFixtureV2()
	want, err := ReplayIdentityV2(base)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name string
		set  func(*ReplayContextV2)
	}{
		{"principal", func(c *ReplayContextV2) { c.Principal += "-changed" }},
		{"profile revision", func(c *ReplayContextV2) { c.ProfileRevision += "-changed" }},
		{"model", func(c *ReplayContextV2) { c.Model += "-changed" }},
		{"policy digest", func(c *ReplayContextV2) { c.PolicyDigest += "-changed" }},
		{"schema digest", func(c *ReplayContextV2) { c.SchemaDigest += "-changed" }},
		{"compatibility", func(c *ReplayContextV2) { c.Compatibility += "-changed" }},
		{"layout", func(c *ReplayContextV2) { c.Layout += "-changed" }},
		{"caller label", func(c *ReplayContextV2) { c.CallerLabel += "-changed" }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			ctx := base
			mutation.set(&ctx)
			got, err := ReplayIdentityV2(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if got == want {
				t.Fatalf("changed %s did not change replay identity", mutation.name)
			}
		})
	}
}
