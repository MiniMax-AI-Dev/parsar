package mcode

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func decodeComponent(value string) (string, error) { return url.PathUnescape(value) }

func (s *Session) handle(frame rpcFrame) error {
	if len(frame.ID) > 0 {
		if frame.Method == "session/request_permission" && s.active {
			return s.askPermission(frame)
		}
		if frame.Method == "elicitation/create" && s.active {
			return s.askQuestion(frame)
		}
		return s.write(rpcFrame{JSONRPC: "2.0", ID: frame.ID, Error: &rpcError{Code: -32601, Message: "ACP method not supported by Parsar"}})
	}
	if frame.Method != "session/update" || !s.active {
		return nil
	}
	var event sessionUpdate
	if err := json.Unmarshal(frame.Params, &event); err != nil {
		return fmt.Errorf("mcode: invalid session update")
	}
	if event.SessionID != s.sessionID {
		return nil
	}
	switch event.Update.Kind {
	case "agent_message_chunk":
		if event.Update.Content.Type != "text" {
			return fmt.Errorf("mcode: unsupported response content")
		}
		text := event.Update.Content.Text
		s.content.WriteString(text)
		s.sequence++
		s.emit(proto.TypeDelta, proto.DeltaPayload{Delta: text, Sequence: s.sequence})
	case "agent_thought_chunk":
		s.sequence++
		s.emit(proto.TypeThinking, proto.ThinkingPayload{Text: event.Update.Content.Text, Sequence: s.sequence})
	case "tool_call", "tool_call_update":
		s.emitTool(event.Update.toolUpdate)
	}
	return nil
}

func (s *Session) emitTool(update toolUpdate) {
	if update.ID == "" || s.completedTools[update.ID] {
		return
	}
	previous, started := s.tools[update.ID]
	if update.Name == "" {
		update.Name = previous.Name
	}
	if update.Name == "" {
		update.Name = update.Title
	}
	if update.RawInput == nil {
		update.RawInput = previous.RawInput
	}
	if !started {
		s.emit(proto.TypeToolCall, proto.ToolCallPayload{ID: update.ID, Name: update.Name, Stage: "before", Args: update.RawInput})
	}
	if update.Status == "completed" || update.Status == "failed" {
		s.emit(proto.TypeToolCall, proto.ToolCallPayload{ID: update.ID, Name: update.Name, Stage: "after", Result: map[string]any{"output": update.RawOutput, "status": update.Status}})
		delete(s.tools, update.ID)
		s.completedTools[update.ID] = true
	} else {
		s.tools[update.ID] = update
	}
}

func (s *Session) askPermission(frame rpcFrame) error {
	var request permissionRequest
	if err := json.Unmarshal(frame.Params, &request); err != nil {
		return fmt.Errorf("mcode: invalid permission request")
	}
	if request.SessionID != s.sessionID {
		return fmt.Errorf("mcode: permission request belongs to another session")
	}
	pending := pendingPermission{RPCID: frame.ID}
	for _, option := range request.Options {
		switch option.Kind {
		case "allow_once":
			pending.Allow = option.ID
		case "reject_once":
			pending.Deny = option.ID
		}
	}
	if pending.Allow == "" || pending.Deny == "" {
		return fmt.Errorf("mcode: permission request has no one-time allow/deny options")
	}
	id := "perm_" + uuid.NewString()
	s.mu.Lock()
	s.permissions[id] = pending
	s.mu.Unlock()
	tool := request.ToolCall.Name
	if tool == "" {
		tool = request.ToolCall.Title
	}
	s.emit(proto.TypePermissionRequest, proto.PermissionRequestPayload{RequestID: id, Tool: tool, Title: request.ToolCall.Title, Payload: request.ToolCall.RawInput})
	return nil
}
