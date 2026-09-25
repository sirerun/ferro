package core

import "encoding/json"

// PreflightSchema validates the bounded schema syntax and supported keyword
// subset before the schema is used to guide paid work.
func PreflightSchema(raw json.RawMessage) error {
	return ValidateSchemaShape(raw)
}

// ValidateResult applies the existing schema subset to a completed result.
func ValidateResult(raw json.RawMessage, value any) error {
	if err := PreflightSchema(raw); err != nil {
		return err
	}
	return validateSchema(raw, value)
}
