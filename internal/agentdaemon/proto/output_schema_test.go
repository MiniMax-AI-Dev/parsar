package proto

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestOutputSchemaTransportPreservesObjectAndNumbers(t *testing.T) {
	for _, schema := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`{"const":9007199254740993,"multipleOf":0.00000000000000000001}`)} {
		request := PromptRequestPayload{OutputSchema: schema}
		env, err := NewEnvelope(TypePromptRequest, "run", request)
		if err != nil {
			t.Fatal(err)
		}
		var decoded PromptRequestPayload
		if err := env.DecodePayload(&decoded); err != nil || !bytes.Equal(decoded.OutputSchema, schema) {
			t.Fatal("output schema changed in transport", err)
		}
		if err := ValidateOutputSchema(decoded.OutputSchema); err != nil {
			t.Fatal(err)
		}
		if schema == nil && bytes.Contains(env.Payload, []byte("output_schema")) {
			t.Fatal("ordinary request acquired an output schema")
		}
	}
	for _, raw := range []string{"", "null", "true", "[]", `"object"`, "3", "{"} {
		if ValidateOutputSchema(json.RawMessage(raw)) == nil {
			t.Fatal("non-object schema accepted", raw)
		}
	}
}
