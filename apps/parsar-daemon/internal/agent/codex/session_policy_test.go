package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

func TestThreadRequestsApplyBypassPolicy(t *testing.T) {
	for _, method := range []string{"thread/start", "thread/resume"} {
		t.Run(method, func(t *testing.T) {
			t.Setenv("PARSAR_HOME", t.TempDir())
			plan, err := BuildSessionPlan("run-policy", "conv/agent/codex", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer plan.Cleanup()
			client, server, cleanup := NewTestClient()
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			session := &Session{rpc: client.JSONRPCClient, cancelCtx: ctx, cfg: sessionConfig{logger: log.With("component", "policy-test")}}
			done := make(chan error, 1)
			go func() {
				if method == "thread/resume" {
					done <- session.resumeThread("old-thread", plan)
				} else {
					done <- session.startThread(plan)
				}
			}()
			var request struct {
				ID     string         `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if err := json.NewDecoder(server.FromClient).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Method != method {
				t.Fatalf("method = %q", request.Method)
			}
			if request.Params["approvalPolicy"] != "never" || request.Params["sandbox"] != "danger-full-access" {
				t.Fatalf("bypass policy missing from %s: %+v", method, request.Params)
			}
			if method == "thread/resume" && request.Params["threadId"] != "old-thread" {
				t.Fatalf("resume lost thread ID: %+v", request.Params)
			}
			if err := json.NewEncoder(server.ToClient).Encode(map[string]any{"id": request.ID, "result": map[string]any{"thread": map[string]any{"id": "resolved-thread"}}}); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if session.currentThreadID() != "resolved-thread" {
				t.Fatal("thread response was not applied")
			}
		})
	}
}
