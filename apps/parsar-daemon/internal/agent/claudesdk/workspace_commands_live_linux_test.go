//go:build linux

package claudesdk

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func liveWorkspaceCommands(t *testing.T, runID string, events []proto.Envelope, commands []string) []proto.ToolCallPayload {
	t.Helper()
	started := map[string]string{}
	finished := map[string]bool{}
	var complete []proto.ToolCallPayload
	terminal := false
	for _, event := range events {
		if event.Type == proto.TypeDone {
			terminal = true
		}
		if event.Type != proto.TypeToolCall {
			continue
		}
		var call proto.ToolCallPayload
		if terminal || event.ID != runID || event.DecodePayload(&call) != nil || call.ID == "" || call.NativeItem != nil || call.Observation == nil || call.Observation.Kind != "command" {
			t.Fatal("invalid command frame or execution identity")
		}
		o := call.Observation
		if o.ExitCode != nil || o.Cwd != nil || o.DurationMS != nil {
			t.Fatal("command observation invented unavailable native metadata")
		}
		switch call.Stage {
		case "before":
			if len(started) >= len(commands) || started[call.ID] != "" || o.Command != commands[len(started)] || o.Status != "in_progress" || len(o.Output) != 0 {
				t.Fatal("unexpected, duplicate or fabricated command start")
			}
			started[call.ID] = o.Command
		case "after":
			if started[call.ID] != o.Command || finished[call.ID] || o.Status == "in_progress" {
				t.Fatal("command completion lacks a unique matching start")
			}
			finished[call.ID] = true
			complete = append(complete, call)
		default:
			t.Fatal("invalid command stage")
		}
	}
	if len(complete) != len(commands) || len(started) != len(commands) {
		t.Fatal("command observations missing or replayed from an earlier query")
	}
	return complete
}
