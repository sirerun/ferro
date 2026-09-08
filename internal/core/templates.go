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
		remaining := *s
		var out strings.Builder
		for {
			start := strings.Index(remaining, "{{")
			if start < 0 {
				break
			}
			end := strings.Index(remaining[start:], "}}")
			if end < 0 {
				break
			}
			key := strings.TrimSpace(remaining[start+2 : start+end])
			if !strings.HasPrefix(key, "extract.") {
				return fmt.Errorf("unsupported template %q (only extract.* allowed)", key)
			}
			val, err := lookupExtract(store, strings.TrimPrefix(key, "extract."))
			if err != nil {
				return err
			}
			out.WriteString(remaining[:start])
			out.WriteString(val)
			remaining = remaining[start+end+2:]
		}
		out.WriteString(remaining)
		*s = out.String()
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
	v, err := extractValue(store, path)
	if err != nil {
		return "", err
	}
	return fmt.Sprint(v), nil
}
func extractValue(store extractStore, path string) (any, error) {
	var cur any = map[string]any(store)
	for _, key := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("template %q: not an object at %q", path, key)
		}
		cur, ok = obj[key]
		if !ok {
			return nil, fmt.Errorf("template %q: missing key %q", path, key)
		}
	}
	return cur, nil
}
