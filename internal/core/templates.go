package core

import (
	"fmt"
	"strings"
)

// --- template expansion (runner support, RFC §4.3) ---

// expandTemplates rewrites {{extract.field}} and {{extract.last.field}} in
// Text/Value/URL fields from prior Extract results. Unknown templates are
// left as-is so failures surface at execution, not silently as "".
func expandTemplates(a *Action, store extractStore) error {
	apply := func(s *string) error {
		if !strings.Contains(*s, "{{") {
			return nil
		}
		out := *s
		for {
			start := strings.Index(out, "{{")
			if start < 0 {
				break
			}
			end := strings.Index(out[start:], "}}")
			if end < 0 {
				break
			}
			key := strings.TrimSpace(out[start+2 : start+end])
			if !strings.HasPrefix(key, "extract.") {
				return fmt.Errorf("unsupported template %q (only extract.* allowed)", key)
			}
			val, err := lookupExtract(store, strings.TrimPrefix(key, "extract."))
			if err != nil {
				return err
			}
			out = out[:start] + val + out[start+end+2:]
		}
		*s = out
		return nil
	}
	for _, target := range []*string{&a.Text, &a.Value, &a.URL} {
		if err := apply(target); err != nil {
			return err
		}
	}
	return nil
}

func lookupExtract(store extractStore, path string) (string, error) {
	parts := strings.SplitN(path, ".", 2)
	if parts[0] == "last" {
		last, ok := store["last"]
		if !ok {
			return "", fmt.Errorf("template {{extract.last}}: no prior extract")
		}
		if len(parts) == 1 {
			return fmt.Sprint(last), nil
		}
		return dig(last, parts[1])
	}
	v, ok := store[parts[0]]
	if !ok {
		return "", fmt.Errorf("template {{extract.%s}}: no such extract", parts[0])
	}
	if len(parts) == 1 {
		return fmt.Sprint(v), nil
	}
	return dig(v, parts[1])
}

// dig walks map[string]any values produced by Extract.
func dig(v any, path string) (string, error) {
	cur := v
	for _, key := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", fmt.Errorf("template path %q: not an object at %q", path, key)
		}
		cur, ok = m[key]
		if !ok {
			return "", fmt.Errorf("template path %q: missing key %q", path, key)
		}
	}
	return fmt.Sprint(cur), nil
}
