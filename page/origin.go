package page

import (
	"fmt"
	"net/url"
	"strings"
)

// OriginOf returns the canonical HTTP(S) origin without path or credentials.
// Opaque, missing-host and credential-bearing URLs are refused.
func OriginOf(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("page: invalid URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return "", fmt.Errorf("page: URL has no supported origin")
	}
	host := strings.ToLower(u.Host)
	if (scheme == "http" && u.Port() == "80") || (scheme == "https" && u.Port() == "443") {
		host = strings.ToLower(u.Hostname())
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	return scheme + "://" + host, nil
}
