package v1

import (
	"encoding/json"
	"testing"
)

func TestRequiredActionFunctionArgumentsRoundTrip(t *testing.T) {
	for _, arguments := range []string{`null`, `{"n":9007199254740993}`, `[1,"value"]`, `false`} {
		original := `{"arguments":` + arguments + `,"call_id":"call","name":"lookup","turn_id":"turn","type":"function_call"}`
		var action RequiredAction
		if err := json.Unmarshal([]byte(original), &action); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(action)
		if err != nil || string(raw) != original {
			t.Fatal("function argument presence or precision changed", string(raw), err)
		}
	}
}
