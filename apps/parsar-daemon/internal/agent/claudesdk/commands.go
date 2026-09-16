package claudesdk

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type commandState struct {
	calls map[string]proto.ToolObservation
}

func (c *commandState) receive(event bridgeEvent, start startRequest, sessionID string, emit func(string, any)) error {
	n := event.Observation
	if start.Workspace == nil || sessionID == "" || event.SessionID != sessionID || event.ID == "" || n == nil ||
		n.Kind != "command" || strings.TrimSpace(n.Command) == "" || n.Name != "" || n.Cwd != nil || n.ExitCode != nil || n.DurationMS != nil ||
		n.Server != "" || len(n.Arguments) != 0 || len(n.Error) != 0 || n.Content != nil || n.Action != nil {
		return fmt.Errorf("claudesdk: invalid command observation")
	}
	if len(n.Output) > 0 {
		var output *string
		if json.Unmarshal(n.Output, &output) != nil {
			return fmt.Errorf("claudesdk: invalid command output")
		}
	}
	previous, exists := c.calls[event.ID]
	if (event.Stage == "before" && (exists || n.Status != "in_progress" || len(n.Output) != 0)) ||
		(event.Stage == "after" && (!exists || previous.Status != "in_progress" || previous.Command != n.Command ||
			(n.Status != "completed" && n.Status != "failed" && n.Status != "incomplete"))) ||
		(event.Stage != "before" && event.Stage != "after") {
		return fmt.Errorf("claudesdk: inconsistent command observation")
	}
	c.calls[event.ID] = *n
	if start.observeFunctions {
		emit(proto.TypeToolCall, proto.ToolCallPayload{ID: event.ID, Name: "Bash", Stage: event.Stage, Observation: n})
	}
	return nil
}

func (c *commandState) complete() bool {
	for _, call := range c.calls {
		if call.Status == "in_progress" {
			return false
		}
	}
	return true
}

func (c *commandState) close(start startRequest, emit func(string, any)) {
	for id, call := range c.calls {
		if call.Status != "in_progress" {
			continue
		}
		call.Status = "incomplete"
		c.calls[id] = call
		if start.observeFunctions {
			emit(proto.TypeToolCall, proto.ToolCallPayload{ID: id, Name: "Bash", Stage: "after", Observation: &call})
		}
	}
}
