package claudesdk

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func nativeUsage(raw json.RawMessage) (proto.Usage, error) {
	var snapshot map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&snapshot); err != nil || snapshot == nil {
		return proto.Usage{}, fmt.Errorf("claudesdk: invalid native usage snapshot")
	}
	// The SDK owns native counter scopes and cost estimates. Do not expose an
	// incomplete public token breakdown or a model selected from an unordered map.
	return proto.Usage{Provider: "claude_code", Raw: map[string]any{"claude_sdk_result": snapshot}}, nil
}
