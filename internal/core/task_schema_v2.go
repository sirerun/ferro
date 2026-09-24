package core

import "encoding/json"

// PreflightSchemaV2 validates the bounded schema syntax and supported keyword
// subset before the schema is used to guide paid work.
func PreflightSchemaV2(raw json.RawMessage) error {
	return ValidateSchemaShapeV2(raw)
}

// ValidateResultV2 applies the existing schema subset to a completed result.
func ValidateResultV2(raw json.RawMessage, value any) error {
	if err := PreflightSchemaV2(raw); err != nil {
		return err
	}
	return validateSchema(raw, value)
}
