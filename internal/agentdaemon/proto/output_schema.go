package proto

import (
	"encoding/json"
	"errors"
)

// ValidateOutputSchema checks the transport shape without interpreting the dialect
// or converting schema numbers. The native harness/provider owns schema support.
func ValidateOutputSchema(schema json.RawMessage) error {
	if schema == nil {
		return nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(schema, &object) != nil || object == nil {
		return errors.New("output schema must be a JSON object")
	}
	return nil
}
