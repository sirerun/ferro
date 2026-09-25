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

type profileResolver struct {
	source      ProfileSource
	credentials CredentialResolver
}

func (r ResolvedProfile) MarshalJSON() ([]byte, error) {
	profile := cloneProfile(r.Profile)
	profile.CredentialRef = ""
	return json.Marshal(struct {
		Profile  Profile `json:"profile"`
		Endpoint string  `json:"endpoint"`
		Model    string  `json:"model"`
	}{Profile: profile, Endpoint: r.Endpoint, Model: r.Model})
}

// NewProfileResolver constructs a resolver backed by configured profiles and
// a private credential store.
func NewProfileResolver(source ProfileSource, credentials CredentialResolver) (ProfileResolver, error) {
	if isNilProfileDependency(source) || isNilProfileDependency(credentials) {
		return nil, fmt.Errorf("profile source and credential resolver are required")
	}
	return &profileResolver{source: source, credentials: credentials}, nil
}

func (r *profileResolver) Resolve(ctx context.Context, name string) (ResolvedProfile, error) {
	if err := ctx.Err(); err != nil {
		return ResolvedProfile{}, err
	}
	if !validTaskID(name) {
		return ResolvedProfile{}, fmt.Errorf("invalid profile name")
	}
	profile, err := r.source.LoadProfile(ctx, name)
	if err != nil {
		// The source may contain arbitrary private diagnostics. Never expose them.
		return ResolvedProfile{}, fmt.Errorf("profile %q is unavailable", name)
	}
	if err := ctx.Err(); err != nil {
		return ResolvedProfile{}, err
	}
	profile = cloneProfile(profile)
	if profile.Name != name {
		return ResolvedProfile{}, fmt.Errorf("profile %q is unavailable", name)
	}
	normalizedEndpoint, err := normalizeProfileEndpoint(profile.Endpoint)
	if err != nil {
		return ResolvedProfile{}, fmt.Errorf("invalid profile configuration")
	}
	profile.Endpoint = normalizedEndpoint
	if strings.TrimSpace(profile.Model) == "" || profile.Model != strings.TrimSpace(profile.Model) {
		return ResolvedProfile{}, fmt.Errorf("invalid profile configuration")
	}
	if !utf8Valid([]byte(profile.Model)) || !utf8Valid([]byte(profile.CredentialRef)) {
		return ResolvedProfile{}, fmt.Errorf("invalid profile configuration")
	}
	if err := profile.Limits.Validate(); err != nil {
		return ResolvedProfile{}, fmt.Errorf("invalid profile configuration")
	}
	wantRevision, err := profileRevision(profile)
	if err != nil || !isLowerSHA256(profile.Revision) || profile.Revision != wantRevision {
		return ResolvedProfile{}, fmt.Errorf("invalid profile revision")
	}
	credential := ""
	if profile.CredentialRef != "" {
		if err := ctx.Err(); err != nil {
			return ResolvedProfile{}, err
		}
		credential, err = r.credentials.ResolveCredential(ctx, profile.CredentialRef)
		if err != nil {
			// Credential store errors may contain the reference or secret.
			return ResolvedProfile{}, fmt.Errorf("profile credential is unavailable")
		}
		if credential == "" {
			return ResolvedProfile{}, fmt.Errorf("profile credential is unavailable")
		}
	}
	return ResolvedProfile{
		Profile: cloneProfile(profile), Endpoint: profile.Endpoint,
		Model: profile.Model, Credential: credential,
	}, nil
}

// NewLegacyProfile maps the two explicitly supported legacy settings into
// an immutable named profile. Callers supply already configured values.
func NewLegacyProfile(name, endpoint, model, credentialRef string, limits core.Limits) (Profile, error) {
	if name != "legacy-mcp" && name != "legacy-chat" {
		return Profile{}, fmt.Errorf("unsupported legacy profile name")
	}
	normalizedEndpoint, err := normalizeProfileEndpoint(endpoint)
	if err != nil || strings.TrimSpace(model) == "" || model != strings.TrimSpace(model) || !utf8Valid([]byte(model)) || !utf8Valid([]byte(credentialRef)) {
		return Profile{}, fmt.Errorf("invalid legacy profile configuration")
	}
	if err := limits.Validate(); err != nil {
		return Profile{}, fmt.Errorf("invalid legacy profile limits: %w", err)
	}
	profile := Profile{
		Name: name, Endpoint: normalizedEndpoint, Model: model,
		CredentialRef: credentialRef, Limits: cloneLimitsProfile(limits),
	}
	revision, err := profileRevision(profile)
	if err != nil {
		return Profile{}, fmt.Errorf("compute legacy profile revision: %w", err)
	}
	profile.Revision = revision
	return cloneProfile(profile), nil
}

// DescribeProfile returns only fields intended for public profile listings.
func DescribeProfile(profile Profile) ProfileDescription {
	return ProfileDescription{Name: profile.Name, Revision: profile.Revision, Capabilities: []string{"read_only"}}
}

func profileRevision(profile Profile) (string, error) {
	profile = cloneProfile(profile)
	profile.Revision = ""
	data, err := json.Marshal(profile)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func normalizeProfileEndpoint(raw string) (string, error) {
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

func cloneProfile(profile Profile) Profile {
	profile.Limits = cloneLimitsProfile(profile.Limits)
	return profile
}

func cloneLimitsProfile(limits core.Limits) core.Limits {
	if limits.ReserveMicroUSD != nil {
		value := *limits.ReserveMicroUSD
		limits.ReserveMicroUSD = &value
	}
	return limits
}

func isLowerSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func isNilProfileDependency(value any) bool {
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
