package store

import "testing"

func TestMeasuredUsageRequiresCompleteConsistentCounts(t *testing.T) {
	good := `{"tokens":{"input_tokens":10,"cached_input_tokens":4,"output_tokens":3,"reasoning_output_tokens":2,"total_tokens":13}}`
	for _, kind := range []string{"usage", "done", "execution_failed", "execution_cancelled"} {
		raw := good
		if kind != "usage" {
			raw = `{"usage":` + raw + `}`
		}
		if kind != "usage" && kind != "done" {
			raw = `{"done":` + raw + `}`
		}
		got := measuredUsage(kind, []byte(raw))
		if got == nil || got.InputTokens != 10 || got.InputTokensDetails.CachedTokens != 4 || got.OutputTokensDetails.ReasoningTokens != 2 {
			t.Fatalf("%s: %+v", kind, got)
		}
	}
	for _, raw := range []string{
		`{}`, `{"input_tokens":1}`, `{"tokens":{}}`,
		`{"tokens":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0,"total_tokens":0}}`,
		`{"tokens":{"input_tokens":1,"cached_input_tokens":2,"output_tokens":0,"reasoning_output_tokens":0,"total_tokens":1}}`,
		`{"tokens":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1,"reasoning_output_tokens":2,"total_tokens":2}}`,
		`{"tokens":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1,"reasoning_output_tokens":0,"total_tokens":3}}`,
		`{"tokens":{"input_tokens":9223372036854775807,"cached_input_tokens":0,"output_tokens":1,"reasoning_output_tokens":0,"total_tokens":0}}`,
	} {
		if got := measuredUsage("usage", []byte(raw)); got != nil {
			t.Fatalf("invalid measurement accepted: %s", raw)
		}
	}
	zero := measuredUsage("usage", []byte(`{"tokens":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0,"reasoning_output_tokens":0,"total_tokens":0}}`))
	if zero == nil {
		t.Fatal("explicit zero is measured")
	}
}
