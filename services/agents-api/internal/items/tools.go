package items

import (
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

type toolObservation struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Stage  string          `json:"stage"`
	Args   json.RawMessage `json:"args"`
	Result json.RawMessage `json:"result"`
	Native json.RawMessage `json:"native_item"`
}

type nativeTool struct {
	ID           string              `json:"id"`
	Type         string              `json:"type"`
	Status       string              `json:"status"`
	Command      string              `json:"command"`
	Cwd          *string             `json:"cwd"`
	Output       *string             `json:"aggregatedOutput"`
	ExitCode     *int64              `json:"exitCode"`
	DurationMS   *int64              `json:"durationMs"`
	Server       string              `json:"server"`
	Tool         string              `json:"tool"`
	Arguments    json.RawMessage     `json:"arguments"`
	Result       json.RawMessage     `json:"result"`
	Error        json.RawMessage     `json:"error"`
	Changes      json.RawMessage     `json:"changes"`
	ContentItems json.RawMessage     `json:"contentItems"`
	Success      *bool               `json:"success"`
	Action       *v1.WebSearchAction `json:"action"`
}

func projectTool(turn string, raw json.RawMessage) ([]Update, error) {
	var p toolObservation
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if p.ID == "" || (p.Stage != "before" && p.Stage != "after") {
		return nil, errors.New("invalid tool observation")
	}
	item := v1.Item{ID: Identity(turn, "tool:"+p.ID), TurnID: turn, Status: "in_progress"}
	if len(p.Native) == 0 || string(p.Native) == "null" {
		return legacyTool(turn, p, item)
	}
	var n nativeTool
	if err := json.Unmarshal(p.Native, &n); err != nil {
		return nil, err
	}
	if n.ID != p.ID {
		return nil, errors.New("native tool identity mismatch")
	}
	item.Status = toolStatus(n.Status, p.Stage)
	switch n.Type {
	case "commandExecution":
		if n.Command == "" {
			return nil, errors.New("missing command")
		}
		item.Type = "command_execution"
		item.Command = n.Command
		item.Cwd = n.Cwd
		item.ExitCode = n.ExitCode
		item.DurationMS = n.DurationMS
		if n.Output != nil {
			item.Output = encoded(*n.Output)
		}
	case "mcpToolCall":
		if n.Server == "" || n.Tool == "" {
			return nil, errors.New("missing MCP identity")
		}
		item.Type = "mcp_call"
		item.ServerLabel = n.Server
		item.Name = n.Tool
		item.Arguments = nullable(n.Arguments)
		item.Output = nullable(n.Result)
		item.Error = nullable(n.Error)
	case "dynamicToolCall":
		if n.Tool == "" {
			return nil, errors.New("missing function identity")
		}
		item.Type = "function_call"
		item.Name = n.Tool
		item.CallID = item.ID
		item.Arguments = nullable(n.Arguments)
		if n.Success != nil && !*n.Success && p.Stage == "after" {
			item.Status = "failed"
		}
		updates := []Update{{Item: item}}
		if p.Stage == "after" && len(n.ContentItems) > 0 && string(n.ContentItems) != "null" {
			output, err := dynamicOutput(n.ContentItems)
			if err != nil {
				return nil, err
			}
			updates = append(updates, Update{Item: v1.Item{ID: Identity(turn, "result:"+p.ID), TurnID: turn, Type: "function_call_output", CallID: item.ID, Status: item.Status, Output: output}})
		}
		return updates, nil
	case "fileChange":
		item.Type = "function_call"
		item.Name = "apply_patch"
		item.CallID = item.ID
		item.Arguments = encoded(struct {
			Changes json.RawMessage `json:"changes"`
		}{nullable(n.Changes)})
	case "webSearch":
		item.Type = "web_search_call"
		item.Action = n.Action
		if item.Status == "failed" {
			item.Status = "incomplete"
		}
		if item.Action != nil {
			switch item.Action.Type {
			case "search", "open_page", "find_in_page", "other":
			default:
				return nil, errors.New("unsupported web action")
			}
		}
	default:
		return nil, errors.New("unsupported native tool observation")
	}
	return []Update{{Item: item}}, nil
}

func toolStatus(native, stage string) string {
	if stage == "before" {
		return "in_progress"
	}
	switch native {
	case "completed":
		return "completed"
	case "failed", "declined":
		return "failed"
	case "inProgress", "in_progress", "interrupted":
		return "incomplete"
	case "":
		return "completed"
	default:
		return "incomplete"
	}
}

func legacyTool(turn string, p toolObservation, item v1.Item) ([]Update, error) {
	if p.Name == "" {
		return nil, errors.New("missing tool name")
	}
	item.Type = "function_call"
	item.Name = p.Name
	item.CallID = item.ID
	item.Arguments = nullable(p.Args)
	if p.Stage == "before" {
		return []Update{{Item: item}}, nil
	}
	// Legacy frames do not retain a native completion status.
	item.Status = "incomplete"
	updates := []Update{{Item: item}}
	if len(p.Result) > 0 && string(p.Result) != "null" {
		updates = append(updates, Update{Item: v1.Item{ID: Identity(turn, "result:"+p.ID), TurnID: turn, Type: "function_call_output", CallID: item.ID, Status: "incomplete", Output: encoded(string(p.Result))}})
	}
	return updates, nil
}

func dynamicOutput(raw json.RawMessage) (json.RawMessage, error) {
	var native []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL string `json:"imageUrl"`
	}
	if err := json.Unmarshal(raw, &native); err != nil {
		return nil, err
	}
	content := make([]v1.ItemContent, 0, len(native))
	for _, c := range native {
		switch c.Type {
		case "inputText":
			content = append(content, v1.ItemContent{Type: "input_text", Text: &c.Text})
		case "inputImage":
			content = append(content, v1.ItemContent{Type: "input_image", ImageURL: c.ImageURL})
		default:
			return nil, errors.New("unsupported function result content")
		}
	}
	return json.Marshal(content)
}

func nullable(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`null`)
	}
	return raw
}
func encoded(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
