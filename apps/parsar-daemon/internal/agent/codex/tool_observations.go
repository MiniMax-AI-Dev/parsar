package codex

import (
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Raw fields bypass ThreadItem's legacy any fields to preserve structured values.
type toolObservationSource struct {
	ThreadItem
	Cwd          *string                    `json:"cwd"`
	Arguments    json.RawMessage            `json:"arguments"`
	Changes      json.RawMessage            `json:"changes"`
	Output       *string                    `json:"aggregatedOutput"`
	DurationMS   *int64                     `json:"durationMs"`
	Result       json.RawMessage            `json:"result"`
	Error        json.RawMessage            `json:"error"`
	ContentItems *[]functionContent         `json:"contentItems"`
	Success      *bool                      `json:"success"`
	Action       *proto.ToolWebSearchAction `json:"action"`
}

func normalizeToolObservation(id, stage string, raw json.RawMessage) (*proto.ToolObservation, error) {
	var n toolObservationSource
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil, err
	}
	if n.ID != id || id == "" || (stage != "before" && stage != "after") {
		return nil, errors.New("invalid native tool identity or stage")
	}
	out := &proto.ToolObservation{Status: observationStatus(n.Status, stage)}
	switch n.Type {
	case "commandExecution":
		if n.Command == "" {
			return nil, errors.New("missing command")
		}
		out.Kind, out.Command, out.Cwd, out.DurationMS = "command", n.Command, n.Cwd, n.DurationMS
		if n.ExitCode != nil {
			value := int64(*n.ExitCode)
			out.ExitCode = &value
		}
		if n.Output != nil {
			out.Output, _ = json.Marshal(*n.Output)
		}
	case "mcpToolCall":
		if n.Server == "" || n.Tool == "" {
			return nil, errors.New("missing MCP identity")
		}
		out.Kind, out.Server, out.Name = "mcp", n.Server, n.Tool
		out.Arguments, out.Output, out.Error = n.Arguments, n.Result, n.Error
	case "dynamicToolCall":
		if n.Tool == "" {
			return nil, errors.New("missing function identity")
		}
		out.Kind, out.Name, out.Arguments = "function", n.Tool, n.Arguments
		if n.Namespace != "" {
			out.Name = n.Namespace + "::" + n.Tool
		}
		if stage == "after" {
			if n.Success != nil && !*n.Success {
				out.Status = "failed"
			}
			if n.ContentItems != nil {
				content := make([]proto.FunctionResultContent, 0, len(*n.ContentItems))
				for _, part := range *n.ContentItems {
					var value proto.FunctionResultContent
					switch part.Type {
					case "inputText":
						value = proto.FunctionResultContent{Type: "input_text", Text: part.Text}
					case "inputImage":
						value = proto.FunctionResultContent{Type: "input_image", ImageURL: part.ImageURL}
					default:
						return nil, errors.New("unsupported function result content")
					}
					content = append(content, value)
				}
				if err := (proto.FunctionResultPayload{Content: content}).ValidateContent(); err != nil {
					return nil, err
				}
				out.Content = &content
			}
		}
	case "fileChange":
		out.Kind, out.Name = "function", "apply_patch"
		changes := n.Changes
		if len(changes) == 0 {
			changes = json.RawMessage("null")
		}
		out.Arguments, _ = json.Marshal(struct {
			Changes json.RawMessage `json:"changes"`
		}{changes})
	case "webSearch":
		out.Kind, out.Action = "web_search", n.Action
		if out.Action != nil {
			switch out.Action.Type {
			case "openPage":
				out.Action.Type = "open_page"
			case "findInPage":
				out.Action.Type = "find_in_page"
			case "search", "open_page", "find_in_page", "other":
			default:
				return nil, errors.New("unsupported web action")
			}
		}
	default:
		return nil, errors.New("unsupported native tool observation")
	}
	return out, nil
}

func observationStatus(native, stage string) string {
	if stage == "before" {
		return "in_progress"
	}
	switch native {
	case "", "completed":
		return "completed"
	case "failed", "declined":
		return "failed"
	default:
		return "incomplete"
	}
}
