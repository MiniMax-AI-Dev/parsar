package codex

import (
	"context"
	"encoding/json"
	"fmt"
)

// Check the native provider instead of assuming an older binary honors the flag.
func verifyNoExecutionEnvironment(ctx context.Context, rpc *JSONRPCClient) error {
	for _, id := range []string{"local", "remote"} {
		status, err := nativeEnvironmentStatus(ctx, rpc, id)
		if err != nil {
			return fmt.Errorf("codex: cannot confirm disabled execution environment: %w", err)
		}
		if status != "unknown" {
			return fmt.Errorf("codex: execution environment %s was not disabled", id)
		}
	}
	return nil
}

func nativeEnvironmentStatus(ctx context.Context, rpc *JSONRPCClient, id string) (string, error) {
	raw, err := rpc.Request(ctx, "environment/status", map[string]string{"environmentId": id})
	if err != nil {
		return "", err
	}
	var result struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Status == "" {
		return "", fmt.Errorf("codex: invalid native environment status")
	}
	return result.Status, nil
}
