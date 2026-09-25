package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// ReplayIdentity derives an opaque cache partition from the frozen replay
// context. An empty caller label explicitly disables replay.
func ReplayIdentity(ctx ReplayContext) (string, error) {
	if ctx.CallerLabel == "" {
		return "", nil
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"principal", ctx.Principal},
		{"profile revision", ctx.ProfileRevision},
		{"model", ctx.Model},
		{"policy digest", ctx.PolicyDigest},
		{"schema digest", ctx.SchemaDigest},
		{"compatibility", ctx.Compatibility},
		{"layout", ctx.Layout},
		{"caller label", ctx.CallerLabel},
	} {
		if strings.TrimSpace(field.value) == "" {
			return "", fmt.Errorf("missing replay identity %s", field.name)
		}
	}
	encoded, err := json.Marshal(ctx)
	if err != nil {
		return "", fmt.Errorf("encode replay identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
