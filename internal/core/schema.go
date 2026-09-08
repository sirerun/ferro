package core

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
)

// validateSchema implements the documented JSON Schema subset. Unsupported
// keywords fail explicitly instead of pretending to validate a result.
func validateSchema(raw json.RawMessage, value any) error {
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil || schema == nil {
		return fmt.Errorf("schema must be an object")
	}
	if err := checkSchema(schema, "$"); err != nil {
		return err
	}
	// Normalize Go maps, integers and RawMessages to JSON types.
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var normalized any
	if err = json.Unmarshal(b, &normalized); err != nil {
		return err
	}
	return matchSchema(schema, normalized, "$")
}

func checkSchema(s map[string]any, path string) error {
	for key, v := range s {
		switch key {
		case "$schema", "title", "description":
			if _, ok := v.(string); !ok {
				return fmt.Errorf("schema %s.%s must be string", path, key)
			}
		case "type":
			if v != "object" && v != "array" && v != "string" && v != "number" && v != "integer" && v != "boolean" && v != "null" {
				return fmt.Errorf("schema %s.type unsupported: %v", path, v)
			}
		case "properties":
			props, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("schema %s.properties must be object", path)
			}
			for k, p := range props {
				child, ok := p.(map[string]any)
				if !ok {
					return fmt.Errorf("schema %s.%s must be object", path, k)
				}
				if err := checkSchema(child, path+"."+k); err != nil {
					return err
				}
			}
		case "items":
			child, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("schema %s.items must be object", path)
			}
			if err := checkSchema(child, path+"[]"); err != nil {
				return err
			}
		case "required":
			keys, ok := v.([]any)
			if !ok {
				return fmt.Errorf("schema %s.required must be array", path)
			}
			for _, k := range keys {
				if _, ok := k.(string); !ok {
					return fmt.Errorf("schema %s.required entries must be strings", path)
				}
			}
		case "additionalProperties":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("schema %s.additionalProperties must be boolean", path)
			}
		case "enum":
			arr, ok := v.([]any)
			if !ok || len(arr) == 0 {
				return fmt.Errorf("schema %s.enum must be nonempty array", path)
			}
		case "minimum", "maximum", "minLength", "maxLength", "minItems", "maxItems":
			n, ok := v.(float64)
			if !ok {
				return fmt.Errorf("schema %s.%s must be number", path, key)
			}
			if key != "minimum" && key != "maximum" && (n < 0 || math.Trunc(n) != n) {
				return fmt.Errorf("schema %s.%s must be nonnegative integer", path, key)
			}
		default:
			return fmt.Errorf("schema %s: unsupported keyword %q", path, key)
		}
	}
	return nil
}
func matchSchema(s map[string]any, v any, path string) error {
	if typ, ok := s["type"].(string); ok {
		valid := false
		switch typ {
		case "object":
			_, valid = v.(map[string]any)
		case "array":
			_, valid = v.([]any)
		case "string":
			_, valid = v.(string)
		case "boolean":
			_, valid = v.(bool)
		case "null":
			valid = v == nil
		case "number":
			_, valid = v.(float64)
		case "integer":
			n, ok := v.(float64)
			valid = ok && math.Trunc(n) == n
		}
		if !valid {
			return fmt.Errorf("schema %s: expected %s", path, typ)
		}
	}
	if choices, ok := s["enum"].([]any); ok {
		found := false
		for _, x := range choices {
			if reflect.DeepEqual(x, v) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("schema %s: value not in enum", path)
		}
	}
	if obj, ok := v.(map[string]any); ok {
		required, _ := s["required"].([]any)
		for _, key := range required {
			if _, ok := obj[key.(string)]; !ok {
				return fmt.Errorf("schema %s.%s: required", path, key)
			}
		}
		props, _ := s["properties"].(map[string]any)
		for k, x := range obj {
			if p, ok := props[k].(map[string]any); ok {
				if err := matchSchema(p, x, path+"."+k); err != nil {
					return err
				}
			} else if allow, ok := s["additionalProperties"].(bool); ok && !allow {
				return fmt.Errorf("schema %s.%s: extra property", path, k)
			}
		}
	}
	if arr, ok := v.([]any); ok {
		if err := bounds(s, float64(len(arr)), "minItems", "maxItems", path); err != nil {
			return err
		}
		if item, ok := s["items"].(map[string]any); ok {
			for i, x := range arr {
				if err := matchSchema(item, x, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	if str, ok := v.(string); ok {
		if err := bounds(s, float64(len([]rune(str))), "minLength", "maxLength", path); err != nil {
			return err
		}
	}
	if num, ok := v.(float64); ok {
		return bounds(s, num, "minimum", "maximum", path)
	}
	return nil
}
func bounds(s map[string]any, n float64, min, max, path string) error {
	if b, ok := s[min].(float64); ok && n < b {
		return fmt.Errorf("schema %s: below %s", path, min)
	}
	if b, ok := s[max].(float64); ok && n > b {
		return fmt.Errorf("schema %s: above %s", path, max)
	}
	return nil
}
