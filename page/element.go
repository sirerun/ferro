package page

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// Signature is the stable identity of a target element. Built from the
// snapshot Element, deliberately excluding positional data.
func (e Element) Signature() string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s|%s|%s|%s", e.Tag, e.Role, e.Name, normalizeText(e.Text))
	return fmt.Sprintf("%x", h.Sum(nil))[:16] // short: keys are logged
}

func normalizeText(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }
