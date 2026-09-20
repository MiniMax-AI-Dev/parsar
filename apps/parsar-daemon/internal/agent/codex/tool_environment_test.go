package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestInitializedToolHookReadiness(t *testing.T) {
	for _, mode := range []string{"ready", "disabled", "unmanaged", "async", "wrong command", "errors"} {
		t.Run(mode, func(t *testing.T) {
			client, server, cleanup := NewTestClient()
			defer cleanup()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- verifyToolEnvironmentHook(ctx, client.JSONRPCClient, "/workspace") }()
			var request struct {
				ID, Method string
				Params     struct {
					CWDs []string `json:"cwds"`
				}
			}
			if err := json.NewDecoder(server.FromClient).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Method != "hooks/list" || len(request.Params.CWDs) != 1 || request.Params.CWDs[0] != "/workspace" {
				t.Fatal("wrong hook lookup")
			}
			hook := map[string]any{"eventName": "preToolUse", "command": toolEnvironmentHookCommand, "matcher": "^Bash$", "enabled": true, "isManaged": true, "async": false, "sourcePath": toolEnvironmentHookSource, "trustStatus": "managed"}
			entry := map[string]any{"cwd": "/workspace", "hooks": []any{hook}, "errors": []any{}}
			switch mode {
			case "disabled":
				hook["enabled"] = false
			case "unmanaged":
				hook["isManaged"] = false
			case "async":
				hook["async"] = true
			case "wrong command":
				hook["command"] = "/workspace/untrusted"
			case "errors":
				entry["errors"] = []any{"cannot load hook"}
			}
			if err := json.NewEncoder(server.ToClient).Encode(map[string]any{"id": request.ID, "result": map[string]any{"data": []any{entry}}}); err != nil {
				t.Fatal(err)
			}
			if err := <-done; (err == nil) != (mode == "ready") {
				t.Fatal("hook admission", mode, err)
			}
		})
	}
}

func TestInitializedToolHookFailureStopsRunWithoutSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	out := make(chan proto.Envelope, 4)
	s := &Session{toolEnvironment: true, runID: "run", cancelCtx: ctx, cancelFn: cancel, out: out, cfg: defaultSessionConfig(), bufs: NewItemBuffers()}
	s.setThreadID("thread")
	s.beginRootTurn("thread", "turn")
	s.onToolEnvironmentHook(json.RawMessage(`{"threadId":"foreign","turnId":"turn","run":{"sourcePath":"/etc/codex/runtime-hooks","status":"failed"}}`))
	if s.terminal.Load() {
		t.Fatal("foreign hook ended run")
	}
	s.onToolEnvironmentHook(json.RawMessage(`{"threadId":"thread","turnId":"turn","run":{"sourcePath":"/etc/codex/runtime-hooks","status":"failed"}}`))
	if !s.terminal.Load() || ctx.Err() == nil {
		t.Fatal("failed hook did not stop execution")
	}
	if len(out) != 2 || (<-out).Type != proto.TypeError || (<-out).Type != proto.TypeDone {
		t.Fatal("failed hook reported wrong terminal events")
	}
}
