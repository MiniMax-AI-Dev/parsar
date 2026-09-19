package mcode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestWorkspaceCommandObservationsWaitForArgumentsAndRetainOutcome(t *testing.T) {
	for _, status := range []string{"completed", "failed"} {
		t.Run(status, func(t *testing.T) {
			out := make(chan proto.Envelope, 8)
			s := &Session{ctx: context.Background(), req: proto.PromptRequestPayload{RunID: "run", ObserveToolObservations: true}, out: out, tools: map[string]toolUpdate{}, completedTools: map[string]bool{}}
			s.emitTool(toolUpdate{ID: "call", Name: "mcp__parsar_workspace__workspace_bash"})
			if len(out) != 0 {
				t.Fatal("command item emitted before native arguments")
			}
			s.emitTool(toolUpdate{ID: "call", RawInput: map[string]any{"command": "pwd"}})
			s.emitTool(toolUpdate{ID: "call", Status: status, RawOutput: map[string]any{"content": []any{map[string]string{"type": "text", "text": "/workspace"}}}})
			s.emitTool(toolUpdate{ID: "call", Status: status})
			if len(out) != 2 {
				t.Fatalf("events=%d", len(out))
			}
			var before, after proto.ToolCallPayload
			_ = json.Unmarshal((<-out).Payload, &before)
			_ = json.Unmarshal((<-out).Payload, &after)
			if before.Observation == nil || before.Observation.Kind != "command" || before.Observation.Status != "in_progress" {
				t.Fatal(before)
			}
			n := after.Observation
			if n == nil || n.Command != "pwd" || n.Cwd == nil || *n.Cwd != "/workspace" || n.Status != status {
				t.Fatal(after)
			}
			if string(n.Output) != `"/workspace"` || n.ExitCode != nil || n.DurationMS != nil {
				t.Fatal(n)
			}
		})
	}
}

func TestPrivateUtilitiesAreNotInventedPublicFunctionCalls(t *testing.T) {
	for _, name := range []string{"mcp__parsar_workspace__workspace_read", "skill", "task_query", "task_output", "task_stop", "mcp__unregistered__workspace_bash"} {
		if workspaceToolObservation(toolUpdate{Name: name}, "before") != nil {
			t.Fatal(name)
		}
	}
}
