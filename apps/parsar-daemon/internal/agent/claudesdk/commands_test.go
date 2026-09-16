package claudesdk

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func commandEvent(id, stage, status, command string) bridgeEvent {
	return bridgeEvent{Type: "command_observation", SessionID: "native-session", ID: id, Stage: stage,
		Observation: &proto.ToolObservation{Kind: "command", Status: status, Command: command}}
}

func TestCommandObservationLifecycleAndOptIn(t *testing.T) {
	for _, observed := range []bool{false, true} {
		state := commandState{calls: map[string]proto.ToolObservation{}}
		start := startRequest{Workspace: &workspaceProfile{}, observeFunctions: observed}
		var events []proto.ToolCallPayload
		emit := func(kind string, payload any) {
			if kind != proto.TypeToolCall {
				t.Fatal(kind)
			}
			events = append(events, payload.(proto.ToolCallPayload))
		}
		const command = "  printf 'failure\\n'; exit 7  "
		before := commandEvent("native-call", "before", "in_progress", command)
		if err := state.receive(before, start, "native-session", emit); err != nil || state.complete() {
			t.Fatal("call was not pending", err)
		}
		if err := state.receive(before, start, "native-session", emit); err == nil {
			t.Fatal("duplicate bridge call accepted")
		}
		after := commandEvent("native-call", "after", "failed", command)
		after.Observation.Output = json.RawMessage(`"Exit code 7\nfailure"`)
		if err := state.receive(after, start, "native-session", emit); err != nil || !state.complete() {
			t.Fatal("native result did not complete the call", err)
		}
		if err := state.receive(after, start, "native-session", emit); err == nil {
			t.Fatal("duplicate bridge result accepted")
		}
		if err := state.receive(commandEvent("pending", "before", "in_progress", "sleep 30"), start, "native-session", emit); err != nil {
			t.Fatal(err)
		}
		state.close(start, emit)
		state.close(start, emit)
		if !state.complete() || state.calls["pending"].Status != "incomplete" || state.calls["native-call"].Status != "failed" {
			t.Fatal("closure changed an observed result or lost an unfinished call")
		}
		if !observed {
			if len(events) != 0 {
				t.Fatal("opt-out emitted observations")
			}
			continue
		}
		if len(events) != 4 || events[0].ID != "native-call" || events[0].Name != "Bash" || events[0].Observation.Command != command ||
			string(events[1].Observation.Output) != `"Exit code 7\nfailure"` || events[1].Observation.ExitCode != nil ||
			events[1].Observation.Cwd != nil || events[1].Observation.DurationMS != nil || events[3].ID != "pending" ||
			events[3].Observation.Status != "incomplete" || len(events[3].Observation.Output) != 0 {
			t.Fatal("observation identity, output or unknown metadata changed", events)
		}
	}
}

func TestCommandObservationsRejectUnqualifiedOrInconsistentEvents(t *testing.T) {
	for _, mode := range []string{"profile", "uninitialized", "session", "id", "nil", "kind", "empty-command", "name", "cwd", "exit", "duration", "arguments", "error", "output-object", "before-output", "before-status", "after-before", "changed-command", "after-status", "stage"} {
		t.Run(mode, func(t *testing.T) {
			state := commandState{calls: map[string]proto.ToolObservation{}}
			start := startRequest{Workspace: &workspaceProfile{}, observeFunctions: true}
			sessionID := "native-session"
			event := commandEvent("call", "before", "in_progress", "pwd")
			if strings.HasPrefix(mode, "after-") || mode == "changed-command" {
				if mode != "after-before" {
					state.calls[event.ID] = *event.Observation
				}
				event.Stage, event.Observation.Status = "after", "completed"
			}
			switch mode {
			case "profile":
				start.Workspace = nil
			case "uninitialized":
				sessionID = ""
			case "session":
				event.SessionID = "other"
			case "id":
				event.ID = ""
			case "nil":
				event.Observation = nil
			case "kind":
				event.Observation.Kind = "mcp"
			case "empty-command":
				event.Observation.Command = " "
			case "name":
				event.Observation.Name = "unexpected"
			case "cwd":
				value := "/inferred"
				event.Observation.Cwd = &value
			case "exit":
				value := int64(0)
				event.Observation.ExitCode = &value
			case "duration":
				value := int64(10)
				event.Observation.DurationMS = &value
			case "arguments":
				event.Observation.Arguments = json.RawMessage(`{}`)
			case "error":
				event.Observation.Error = json.RawMessage(`"failure"`)
			case "output-object":
				event.Observation.Output = json.RawMessage(`{"stdout":"output"}`)
			case "before-output":
				event.Observation.Output = json.RawMessage(`"early"`)
			case "before-status":
				event.Observation.Status = "completed"
			case "changed-command":
				event.Observation.Command = "different"
			case "after-status":
				event.Observation.Status = "in_progress"
			case "stage":
				event.Stage = "unknown"
			}
			if err := state.receive(event, start, sessionID, func(string, any) { t.Fatal("invalid event was emitted") }); err == nil {
				t.Fatal("invalid command observation was accepted")
			}
		})
	}
}

func TestCommandObservationUnknownAndEmptyOutputRemainDistinct(t *testing.T) {
	for _, output := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`""`)} {
		state := commandState{calls: map[string]proto.ToolObservation{}}
		start := startRequest{Workspace: &workspaceProfile{}}
		emit := func(string, any) { t.Fatal("opt-out emitted an observation") }
		if err := state.receive(commandEvent("call", "before", "in_progress", "pwd"), start, "native-session", emit); err != nil {
			t.Fatal(err)
		}
		event := commandEvent("call", "after", "completed", "pwd")
		event.Observation.Output = output
		if err := state.receive(event, start, "native-session", emit); err != nil || string(state.calls["call"].Output) != string(output) {
			t.Fatal("output availability changed", err)
		}
	}
}
