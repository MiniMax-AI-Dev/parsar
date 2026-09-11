package proto

import (
	"encoding/json"
	"errors"
)

func (u *TokenUsage) UnmarshalJSON(raw []byte) error {
	type plain TokenUsage
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for _, key := range []string{"input_tokens", "cached_input_tokens", "output_tokens", "reasoning_output_tokens", "total_tokens"} {
		var count *int64
		if json.Unmarshal(fields[key], &count) != nil || count == nil || *count < 0 {
			return errors.New("token usage requires all non-negative counters")
		}
	}
	return json.Unmarshal(raw, (*plain)(u))
}
