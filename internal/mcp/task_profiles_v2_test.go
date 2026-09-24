package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/dndungu/ferro/internal/core"
)

type profileSourceFuncV2 func(context.Context, string) (ProfileV2, error)

func (f profileSourceFuncV2) LoadProfile(ctx context.Context, name string) (ProfileV2, error) {
	return f(ctx, name)
}

type credentialResolverFuncV2 func(context.Context, string) (string, error)

func (f credentialResolverFuncV2) ResolveCredential(ctx context.Context, ref string) (string, error) {
	return f(ctx, ref)
}

func TestProfilesV2_UnknownName(t *testing.T) {
	credentialCalls := 0
	resolver, err := NewProfileResolverV2(profileSourceFuncV2(func(context.Context, string) (ProfileV2, error) {
		return ProfileV2{}, errors.New("private store diagnostic")
	}), credentialResolverFuncV2(func(context.Context, string) (string, error) {
		credentialCalls++
		return "must-not-fetch", nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(), "missing"); err == nil {
		t.Fatal("unknown profile resolved")
	}
	if credentialCalls != 0 {
		t.Fatalf("credential lookup ran %d times for an unknown profile", credentialCalls)
	}
}

func TestProfilesV2_ImmutableRevision(t *testing.T) {
	original, err := NewLegacyProfileV2("legacy-mcp", "HTTPS://EXAMPLE.COM:443/v1/", "exact-model", "credential-ref", core.DefaultLimitsV2())
	if err != nil {
		t.Fatal(err)
	}
	if original.Endpoint != "https://example.com/v1" {
		t.Fatalf("endpoint not normalized: %q", original.Endpoint)
	}
	profiles := original
	resolver, err := NewProfileResolverV2(profileSourceFuncV2(func(context.Context, string) (ProfileV2, error) {
		return profiles, nil
	}), credentialResolverFuncV2(func(context.Context, string) (string, error) { return "secret", nil }))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(context.Background(), "legacy-mcp")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Profile.Revision != original.Revision || resolved.Endpoint != original.Endpoint {
		t.Fatalf("resolved profile differs from immutable revision: %+v", resolved.Profile)
	}
	// The same asserted revision cannot be reused for changed profile content.
	profiles.Model = "changed-model"
	if _, err := resolver.Resolve(context.Background(), "legacy-mcp"); err == nil {
		t.Fatal("changed profile body accepted under an existing revision")
	}
	// Revision mismatch is rejected before retrieving credentials.
	credentialCalls := 0
	resolver, err = NewProfileResolverV2(profileSourceFuncV2(func(context.Context, string) (ProfileV2, error) { return profiles, nil }), credentialResolverFuncV2(func(context.Context, string) (string, error) {
		credentialCalls++
		return "secret", nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(), "legacy-mcp"); err == nil || credentialCalls != 0 {
		t.Fatalf("invalid revision error=%v credential calls=%d", err, credentialCalls)
	}

	// Pointer-valued profile fields are copied before being returned.
	reserve := int64(7)
	profile, err := NewLegacyProfileV2("legacy-chat", "https://example.com", "model", "ref", core.LimitsV2{
		RuntimeMS: 90000, Actions: 20, ModelRequests: 3, Repairs: 1, PlanningPasses: 2,
		MaxOutputTokens: 2048, MaxInputTokens: 12000, TotalReservedTokens: 24000, ReserveMicroUSD: &reserve,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err = NewProfileResolverV2(profileSourceFuncV2(func(context.Context, string) (ProfileV2, error) { return profile, nil }), credentialResolverFuncV2(func(context.Context, string) (string, error) { return "secret", nil }))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err = resolver.Resolve(context.Background(), "legacy-chat")
	if err != nil {
		t.Fatal(err)
	}
	*resolved.Profile.Limits.ReserveMicroUSD = 99
	if *profile.Limits.ReserveMicroUSD != 7 {
		t.Fatal("resolved profile aliases source monetary limit")
	}
}

func TestProfilesV2_LegacyMapping(t *testing.T) {
	for _, name := range []string{"legacy-mcp", "legacy-chat"} {
		profile, err := NewLegacyProfileV2(name, "https://EXAMPLE.com:443/v1/", "model-id", "private-ref", core.DefaultLimitsV2())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if profile.Name != name || profile.Endpoint != "https://example.com/v1" || profile.Model != "model-id" || profile.CredentialRef != "private-ref" {
			t.Fatalf("legacy settings were not mapped exactly: %+v", profile)
		}
		if !isLowerSHA256V2(profile.Revision) {
			t.Fatalf("invalid content revision: %q", profile.Revision)
		}
	}
	if _, err := NewLegacyProfileV2("other", "https://example.com", "model", "ref", core.DefaultLimitsV2()); err == nil {
		t.Fatal("unsupported legacy name accepted")
	}
}

func TestProfilesV2_SecretRedaction(t *testing.T) {
	secret := "private-token-value"
	profile, err := NewLegacyProfileV2("legacy-mcp", "https://example.com", "model", "private-reference", core.DefaultLimitsV2())
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewProfileResolverV2(profileSourceFuncV2(func(context.Context, string) (ProfileV2, error) {
		return profile, errors.New("source diagnostic contains " + secret)
	}), credentialResolverFuncV2(func(context.Context, string) (string, error) {
		return "", errors.New("credential diagnostic contains " + secret)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(), "legacy-mcp"); err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "source diagnostic") {
		t.Fatalf("source error was not safely redacted: %v", err)
	}

	resolver, err = NewProfileResolverV2(profileSourceFuncV2(func(context.Context, string) (ProfileV2, error) { return profile, nil }), credentialResolverFuncV2(func(context.Context, string) (string, error) {
		return "", errors.New("credential diagnostic contains " + secret)
	}))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(context.Background(), "legacy-mcp")
	if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "credential diagnostic") {
		t.Fatalf("credential error was not safely redacted: %v", err)
	}
	resolved = ResolvedProfileV2{Profile: profile, Endpoint: profile.Endpoint, Model: profile.Model, Credential: secret}
	encoded, err := json.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), profile.CredentialRef) {
		t.Fatalf("private profile fields leaked: %s", encoded)
	}
	description, err := json.Marshal(DescribeProfileV2(profile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(description), profile.Endpoint) || strings.Contains(string(description), profile.CredentialRef) || strings.Contains(string(description), secret) {
		t.Fatalf("public description leaked private profile fields: %s", description)
	}
}

func TestProfilesV2_RejectsMaliciousEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"https://user:password@example.com/v1",
		"https://example.com/v1?token=secret",
		"https://example.com/v1#fragment",
		"http://example.com/v1",
		"http://127.0.0.2.example/v1",
	} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := NewLegacyProfileV2("legacy-mcp", endpoint, "model", "ref", core.DefaultLimitsV2()); err == nil {
				t.Fatalf("unsafe endpoint accepted: %q", endpoint)
			}
		})
	}
	for _, endpoint := range []string{"http://localhost:8080/v1", "http://[::1]:8080/v1"} {
		if _, err := NewLegacyProfileV2("legacy-mcp", endpoint, "model", "ref", core.DefaultLimitsV2()); err != nil {
			t.Fatalf("loopback endpoint rejected (%s): %v", endpoint, err)
		}
	}
}
