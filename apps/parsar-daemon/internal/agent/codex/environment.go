package codex

import (
	"context"
	"encoding/json"
	"fmt"
)

// Check the native provider instead of assuming an older binary honors the flag.
func verifyNoExecutionEnvironment(ctx context.Context, rpc *JSONRPCClient) error {
	for _, id := range []string{"local", "remote"} {
		raw, err := rpc.Request(ctx, "environment/status", map[string]string{"environmentId": id})
		if err != nil {
			return fmt.Errorf("codex: cannot confirm disabled execution environment: %w", err)
		}
		var result struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(raw, &result) != nil || result.Status != "unknown" {
			return fmt.Errorf("codex: execution environment %s was not disabled", id)
		}
	}
	return nil
}
