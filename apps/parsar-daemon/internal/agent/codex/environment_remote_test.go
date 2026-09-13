package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestRemoteEnvironmentRequiresNativeReadiness(t *testing.T) {
	for _, mode := range []string{"ready", "local fallback", "connection rejected", "invalid info", "pending", "disconnected"} {
		t.Run(mode, func(t *testing.T) {
			client, server, cleanup := NewTestClient()
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- verifyRemoteEnvironment(ctx, client.JSONRPCClient) }()
			for index, method := range []string{"environment/status", "environment/info", "environment/status"} {
				var request struct {
					ID, Method string
					Params     map[string]string
				}
				if err := json.NewDecoder(server.FromClient).Decode(&request); err != nil {
					t.Fatal(err)
				}
				id := "remote"
				if index == 0 {
					id = "local"
				}
				if request.Method != method || request.Params["environmentId"] != id {
					t.Fatal(request)
				}
				var result any = map[string]string{"status": "unknown"}
				stop := false
				if index == 0 && mode == "local fallback" {
					result = map[string]string{"status": "ready"}
					stop = true
				}
				if index == 1 {
					result = map[string]any{"shell": map[string]string{"path": "/bin/bash"}}
				}
				if index == 1 && mode == "invalid info" {
					result = map[string]any{}
					stop = true
				}
				if index == 2 {
					status := "ready"
					if mode == "pending" || mode == "disconnected" {
						status = mode
					}
					result = map[string]string{"status": status}
				}
				reply := map[string]any{"id": request.ID, "result": result}
				if index == 1 && mode == "connection rejected" {
					delete(reply, "result")
					reply["error"] = map[string]any{"code": -32603, "message": "connection denied"}
					stop = true
				}
				if err := json.NewEncoder(server.ToClient).Encode(reply); err != nil {
					t.Fatal(err)
				}
				if stop {
					break
				}
			}
			if err := <-done; (err == nil) != (mode == "ready") {
				t.Fatalf("%s: %v", mode, err)
			}
		})
	}
}

func TestRemoteEnvironmentSelectedForFirstAndResumedTurns(t *testing.T) {
	for _, resume := range []bool{false, true} {
		client, server, cleanup := NewTestClient()
		defer cleanup()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		s := &Session{rpc: client.JSONRPCClient, cancelCtx: ctx, cancelFn: cancel, cfg: defaultSessionConfig(), out: make(chan proto.Envelope, 8), bufs: NewItemBuffers(), waitDone: make(chan struct{}), cleanup: func() {}, interactions: newPendingCodexInteractions()}
		plan := SessionPlan{Cwd: "/local-harness", Environments: []EnvironmentSelection{{EnvironmentID: "remote", Cwd: "/executor-only", RuntimeWorkspaceRoots: []string{"/executor-only"}}}}
		req := proto.PromptRequestPayload{Prompt: "remote work", StrictResume: true}
		method := "thread/start"
		if resume {
			req.AgentSessionID = "native-thread"
			method = "thread/resume"
		}
		go s.run(plan, req)
		for index, expected := range []string{method, "turn/start"} {
			var request struct {
				ID, Method string
				Params     struct {
					Environments []EnvironmentSelection `json:"environments"`
					Cwd          string                 `json:"cwd"`
					ThreadID     string                 `json:"threadId"`
				}
			}
			if err := json.NewDecoder(server.FromClient).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Method != expected {
				t.Fatal(request.Method)
			}
			if index == 1 || !resume {
				if len(request.Params.Environments) != 1 || request.Params.Environments[0].EnvironmentID != "remote" || request.Params.Environments[0].Cwd != "/executor-only" {
					t.Fatal("native request omitted remote selection")
				}
			} else if len(request.Params.Environments) != 0 {
				t.Fatal("resume invented unsupported environments field")
			}
			if index == 0 && !resume && request.Params.Cwd != "/local-harness" {
				t.Fatal("thread/start changed local cwd")
			}
			if index == 1 && request.Params.ThreadID != "native-thread" {
				t.Fatal("turn lost native identity")
			}
			if err := json.NewEncoder(server.ToClient).Encode(map[string]any{"id": request.ID, "result": map[string]any{"thread": map[string]string{"id": "native-thread"}}}); err != nil {
				t.Fatal(err)
			}
		}
		cancel()
		select {
		case <-s.waitDone:
		case <-time.After(time.Second):
			t.Fatal("native run did not stop")
		}
	}
}
