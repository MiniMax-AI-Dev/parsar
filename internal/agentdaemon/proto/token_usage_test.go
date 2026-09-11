package proto

import (
	"encoding/json"
	"testing"
)

func TestTokenUsageKeepsMissingDistinctFromMeasuredZero(t *testing.T) {
	for _, raw := range []string{
		`{"tokens":{}}`,
		`{"tokens":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0,"reasoning_output_tokens":null,"total_tokens":0}}`,
		`{"tokens":{"input_tokens":-1,"cached_input_tokens":0,"output_tokens":0,"reasoning_output_tokens":0,"total_tokens":0}}`,
	} {
		var usage Usage
		if json.Unmarshal([]byte(raw), &usage) == nil {
			t.Fatalf("incomplete counts accepted: %s", raw)
		}
	}
	var legacy, measured Usage
	if err := json.Unmarshal([]byte(`{"input_tokens":10}`), &legacy); err != nil || legacy.Tokens != nil {
		t.Fatalf("legacy usage changed: %+v %v", legacy, err)
	}
	if err := json.Unmarshal([]byte(`{"tokens":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0,"reasoning_output_tokens":0,"total_tokens":0,"future":"ignored"}}`), &measured); err != nil || measured.Tokens == nil {
		t.Fatalf("measured zero lost: %+v %v", measured, err)
	}
}
