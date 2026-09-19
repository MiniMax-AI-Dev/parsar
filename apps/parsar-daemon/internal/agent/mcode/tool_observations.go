package mcode

import (
	"encoding/json"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (s *Session) emitToolStage(update toolUpdate, stage string) {
	payload := proto.ToolCallPayload{ID: update.ID, Name: update.Name, Stage: stage, Args: update.RawInput}
	if stage == "after" {
		payload.Result = map[string]any{"output": update.RawOutput, "status": update.Status}
	}
	if s.req.ObserveToolObservations {
		payload.Observation = workspaceToolObservation(update, stage)
		// Native task/skill bookkeeping has no qualified public item mapping.
		if payload.Observation == nil {
			return
		}
	}
	s.emit(proto.TypeToolCall, payload)
}

func workspaceToolObservation(update toolUpdate, stage string) *proto.ToolObservation {
	if update.Name != "mcp__parsar_workspace__workspace_bash" {
		return nil
	}
	command, _ := update.RawInput["command"].(string)
	if command == "" {
		return nil
	}
	cwd := "/workspace"
	result := &proto.ToolObservation{Kind: "command", Command: command, Cwd: &cwd, Status: "in_progress"}
	if stage == "after" {
		result.Status = update.Status
		// ACP reports MCP content but no structured exit code or duration.
		raw, _ := json.Marshal(update.RawOutput)
		var output struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(raw, &output) == nil && output.Content != nil {
			text := ""
			for _, part := range output.Content {
				if part.Type == "text" {
					text += part.Text
				}
			}
			result.Output, _ = json.Marshal(text)
		}
	}
	return result
}
