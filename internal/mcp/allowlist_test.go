package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAllowlist_MissingFileDeniesEverything(t *testing.T) {
	a, err := NewAllowlist(filepath.Join(shortTempDir(t), "allowlist.json"))
	if err != nil {
		t.Fatalf("NewAllowlist on a missing file returned an error, want deny-by-default: %v", err)
	}
	if err := a.Check("https://example.com"); err == nil {
		t.Error("Check succeeded against an empty allowlist")
	}
}

func TestAllowlist_AllowAndDeny(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "allowlist.json")
	if err := os.WriteFile(path, []byte(`["https://mail.google.com", "https://example.com:8080"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := NewAllowlist(path)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		origin string
		allow  bool
	}{
		{"https://mail.google.com", true},
		{"https://example.com:8080", true},
		{"https://example.com", false},    // no port -- distinct origin
		{"http://mail.google.com", false}, // wrong scheme -- distinct origin
		{"https://evil.example", false},
	}
	for _, tc := range cases {
		err := a.Check(tc.origin)
		if tc.allow && err != nil {
			t.Errorf("Check(%q) = %v, want allowed", tc.origin, err)
		}
		if !tc.allow && err == nil {
			t.Errorf("Check(%q) succeeded, want denied", tc.origin)
		}
		if !tc.allow && err != nil {
			// ADR 005: the denial must name the origin and the file to edit.
			if got := err.Error(); !strings.Contains(got, tc.origin) || !strings.Contains(got, path) {
				t.Errorf("Check(%q) error %q does not name the origin and the allowlist path", tc.origin, got)
			}
		}
	}
}

func TestAllowlist_ReloadsOnMtimeChange(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "allowlist.json")
	if err := os.WriteFile(path, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := NewAllowlist(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Check("https://example.com"); err == nil {
		t.Fatal("Check succeeded before the origin was added")
	}

	// Ensure the mtime actually advances on filesystems with coarse
	// (1s-granularity) mtimes.
	time.Sleep(1100 * time.Millisecond)
	if err := os.WriteFile(path, []byte(`["https://example.com"]`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := a.Check("https://example.com"); err != nil {
		t.Errorf("Check did not pick up the extended allowlist without a restart: %v", err)
	}
}

func TestAllowlistRevocationFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allowlist.json")
	if err := os.WriteFile(path, []byte(`["https://example.com"]`), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := NewAllowlist(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Check("https://example.com"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	if a.Check("https://example.com") == nil {
		t.Fatal("malformed policy kept old access")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if a.Check("https://example.com") == nil {
		t.Fatal("deleted policy kept old access")
	}
}
