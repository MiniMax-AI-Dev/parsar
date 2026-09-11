package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestNoEnvironmentRequiresNativeConfirmation(t *testing.T) {
	for _, status := range []string{"unknown", "ready", "pending", "disconnected", ""} {
		t.Run(status, func(t *testing.T) {
			client, server, cleanup := NewTestClient()
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- verifyNoExecutionEnvironment(ctx, client.JSONRPCClient) }()
			for _, id := range []string{"local", "remote"} {
				var req struct {
					ID     string            `json:"id"`
					Method string            `json:"method"`
					Params map[string]string `json:"params"`
				}
				if err := json.NewDecoder(server.FromClient).Decode(&req); err != nil {
					t.Fatal(err)
				}
				if req.Method != "environment/status" || req.Params["environmentId"] != id {
					t.Fatal(req)
				}
				if err := json.NewEncoder(server.ToClient).Encode(map[string]any{"id": req.ID, "result": map[string]string{"status": status}}); err != nil {
					t.Fatal(err)
				}
				if status != "unknown" {
					break
				}
			}
			if err := <-done; (err == nil) != (status == "unknown") {
				t.Fatalf("status %q: %v", status, err)
			}
		})
	}
}
