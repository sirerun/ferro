package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/dndungu/ferro/internal/core"
)

type profileResolverV2 struct {
	source      ProfileSourceV2
	credentials CredentialResolverV2
}

func (r ResolvedProfileV2) MarshalJSON() ([]byte, error) {
	profile := cloneProfileV2(r.Profile)
	profile.CredentialRef = ""
	return json.Marshal(struct {
		Profile  ProfileV2 `json:"profile"`
		Endpoint string    `json:"endpoint"`
		Model    string    `json:"model"`
	}{Profile: profile, Endpoint: r.Endpoint, Model: r.Model})
}

// NewProfileResolverV2 constructs a resolver backed by configured profiles and
// a private credential store.
func NewProfileResolverV2(source ProfileSourceV2, credentials CredentialResolverV2) (ProfileResolverV2, error) {
	if isNilProfileDependencyV2(source) || isNilProfileDependencyV2(credentials) {
		return nil, fmt.Errorf("profile source and credential resolver are required")
	}
	return &profileResolverV2{source: source, credentials: credentials}, nil
}

func (r *profileResolverV2) Resolve(ctx context.Context, name string) (ResolvedProfileV2, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedProfileV2{}, err
	}
	if !validTaskIDV2(name) {
		return ResolvedProfileV2{}, fmt.Errorf("invalid profile name")
	}
	profile, err := r.source.LoadProfile(ctx, name)
	if err != nil {
		// The source may contain arbitrary private diagnostics. Never expose them.
		return ResolvedProfileV2{}, fmt.Errorf("profile %q is unavailable", name)
	}
	if err := ctx.Err(); err != nil {
		return ResolvedProfileV2{}, err
	}
	profile = cloneProfileV2(profile)
	if profile.Name != name {
		return ResolvedProfileV2{}, fmt.Errorf("profile %q is unavailable", name)
	}
	normalizedEndpoint, err := normalizeProfileEndpointV2(profile.Endpoint)
	if err != nil {
		return ResolvedProfileV2{}, fmt.Errorf("invalid profile configuration")
	}
	profile.Endpoint = normalizedEndpoint
	if strings.TrimSpace(profile.Model) == "" || profile.Model != strings.TrimSpace(profile.Model) {
		return ResolvedProfileV2{}, fmt.Errorf("invalid profile configuration")
	}
	if !utf8Valid([]byte(profile.Model)) || !utf8Valid([]byte(profile.CredentialRef)) {
		return ResolvedProfileV2{}, fmt.Errorf("invalid profile configuration")
	}
	if err := profile.Limits.ValidateV2(); err != nil {
		return ResolvedProfileV2{}, fmt.Errorf("invalid profile configuration")
	}
	wantRevision, err := profileRevisionV2(profile)
	if err != nil || !isLowerSHA256V2(profile.Revision) || profile.Revision != wantRevision {
		return ResolvedProfileV2{}, fmt.Errorf("invalid profile revision")
	}
	credential := ""
	if profile.CredentialRef != "" {
		if err := ctx.Err(); err != nil {
			return ResolvedProfileV2{}, err
		}
		credential, err = r.credentials.ResolveCredential(ctx, profile.CredentialRef)
		if err != nil {
			// Credential store errors may contain the reference or secret.
			return ResolvedProfileV2{}, fmt.Errorf("profile credential is unavailable")
		}
		if credential == "" {
			return ResolvedProfileV2{}, fmt.Errorf("profile credential is unavailable")
		}
	}
	return ResolvedProfileV2{
		Profile: cloneProfileV2(profile), Endpoint: profile.Endpoint,
		Model: profile.Model, Credential: credential,
	}, nil
}

// NewLegacyProfileV2 maps the two explicitly supported legacy settings into
// an immutable named profile. Callers supply already configured values.
func NewLegacyProfileV2(name, endpoint, model, credentialRef string, limits core.LimitsV2) (ProfileV2, error) {
	if name != "legacy-mcp" && name != "legacy-chat" {
		return ProfileV2{}, fmt.Errorf("unsupported legacy profile name")
	}
	normalizedEndpoint, err := normalizeProfileEndpointV2(endpoint)
	if err != nil || strings.TrimSpace(model) == "" || model != strings.TrimSpace(model) || !utf8Valid([]byte(model)) || !utf8Valid([]byte(credentialRef)) {
		return ProfileV2{}, fmt.Errorf("invalid legacy profile configuration")
	}
	if err := limits.ValidateV2(); err != nil {
		return ProfileV2{}, fmt.Errorf("invalid legacy profile limits: %w", err)
	}
	profile := ProfileV2{
		Name: name, Endpoint: normalizedEndpoint, Model: model,
		CredentialRef: credentialRef, Limits: cloneLimitsProfileV2(limits),
	}
	revision, err := profileRevisionV2(profile)
	if err != nil {
		return ProfileV2{}, fmt.Errorf("compute legacy profile revision: %w", err)
	}
	profile.Revision = revision
	return cloneProfileV2(profile), nil
}

// DescribeProfileV2 returns only fields intended for public profile listings.
func DescribeProfileV2(profile ProfileV2) ProfileDescriptionV2 {
	return ProfileDescriptionV2{Name: profile.Name, Revision: profile.Revision, Capabilities: []string{"read_only"}}
}

func profileRevisionV2(profile ProfileV2) (string, error) {
	profile = cloneProfileV2(profile)
	profile.Revision = ""
	data, err := json.Marshal(profile)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func normalizeProfileEndpointV2(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("invalid profile endpoint")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("invalid profile endpoint scheme")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", fmt.Errorf("invalid profile endpoint host")
	}
	if u.Scheme == "http" {
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return "", fmt.Errorf("HTTP profile endpoints must be loopback")
		}
	}
	port := ""
	portSpecified := false
	if strings.HasPrefix(u.Host, "[") {
		end := strings.IndexByte(u.Host, ']')
		if end < 0 {
			return "", fmt.Errorf("invalid profile endpoint host")
		}
		remainder := u.Host[end+1:]
		if remainder != "" {
			if !strings.HasPrefix(remainder, ":") {
				return "", fmt.Errorf("invalid profile endpoint port")
			}
			port, portSpecified = remainder[1:], true
		}
	} else if colon := strings.LastIndexByte(u.Host, ':'); colon >= 0 {
		if strings.Contains(u.Host[:colon], ":") {
			return "", fmt.Errorf("IPv6 profile endpoint hosts must be bracketed")
		}
		port, portSpecified = u.Host[colon+1:], true
	}
	if portSpecified && port == "" {
		return "", fmt.Errorf("invalid profile endpoint port")
	}
	if port != "" {
		for _, digit := range port {
			if digit < '0' || digit > '9' {
				return "", fmt.Errorf("invalid profile endpoint port")
			}
		}
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", fmt.Errorf("invalid profile endpoint port")
		}
	}
	if port == "80" && u.Scheme == "http" || port == "443" && u.Scheme == "https" {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	u.Host = host
	if u.Path == "/" {
		u.Path = ""
		u.RawPath = ""
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func cloneProfileV2(profile ProfileV2) ProfileV2 {
	profile.Limits = cloneLimitsProfileV2(profile.Limits)
	return profile
}

func cloneLimitsProfileV2(limits core.LimitsV2) core.LimitsV2 {
	if limits.ReserveMicroUSD != nil {
		value := *limits.ReserveMicroUSD
		limits.ReserveMicroUSD = &value
	}
	return limits
}

func isLowerSHA256V2(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func isNilProfileDependencyV2(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
