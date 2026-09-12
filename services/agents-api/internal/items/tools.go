package items

import (
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func projectTool(turn string, raw json.RawMessage) ([]Update, error) {
	var p struct {
		ID          string                 `json:"id"`
		Stage       string                 `json:"stage"`
		Observation *proto.ToolObservation `json:"observation"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	n := p.Observation
	if p.ID == "" || n == nil || (p.Stage != "before" && p.Stage != "after") {
		return nil, errors.New("invalid tool observation")
	}
	switch n.Status {
	case "in_progress", "completed", "failed", "incomplete":
	default:
		return nil, errors.New("invalid tool status")
	}
	if (p.Stage == "before") != (n.Status == "in_progress") {
		return nil, errors.New("inconsistent tool stage and status")
	}
	item := v1.Item{ID: Identity(turn, "tool:"+p.ID), TurnID: turn, Status: n.Status}
	switch n.Kind {
	case "command":
		if n.Command == "" {
			return nil, errors.New("missing command")
		}
		if len(n.Output) > 0 && string(n.Output) != "null" {
			var output string
			if err := json.Unmarshal(n.Output, &output); err != nil {
				return nil, err
			}
			item.Output = n.Output
		}
		item.Type, item.Command, item.Cwd = "command_execution", n.Command, n.Cwd
		item.ExitCode, item.DurationMS = n.ExitCode, n.DurationMS
	case "mcp":
		if n.Server == "" || n.Name == "" {
			return nil, errors.New("missing MCP identity")
		}
		item.Type, item.ServerLabel, item.Name = "mcp_call", n.Server, n.Name
		item.Arguments, item.Output, item.Error = nullable(n.Arguments), nullable(n.Output), nullable(n.Error)
	case "function":
		if n.Name == "" {
			return nil, errors.New("missing function identity")
		}
		item.Type, item.Name, item.CallID = "function_call", n.Name, item.ID
		item.Arguments = nullable(n.Arguments)
		updates := []Update{{Item: item}}
		if p.Stage == "after" && n.Content != nil {
			if err := (proto.FunctionResultPayload{Content: *n.Content}).ValidateContent(); err != nil {
				return nil, err
			}
			content := make([]v1.ItemContent, 0, len(*n.Content))
			for _, part := range *n.Content {
				value := v1.ItemContent{Type: part.Type, Text: part.Text}
				if part.ImageURL != nil {
					value.ImageURL = *part.ImageURL
				}
				content = append(content, value)
			}
			updates = append(updates, Update{Item: v1.Item{ID: Identity(turn, "result:"+p.ID), TurnID: turn, Type: "function_call_output", CallID: item.ID, Status: item.Status, Output: encoded(content)}})
		}
		return updates, nil
	case "web_search":
		item.Type = "web_search_call"
		if item.Status == "failed" {
			item.Status = "incomplete"
		}
		if n.Action != nil {
			switch n.Action.Type {
			case "search", "open_page", "find_in_page", "other":
			default:
				return nil, errors.New("unsupported web action")
			}
			item.Action = &v1.WebSearchAction{Type: n.Action.Type, Query: n.Action.Query, Queries: n.Action.Queries, URL: n.Action.URL, Pattern: n.Action.Pattern}
		}
	default:
		return nil, errors.New("unsupported tool observation")
	}
	return []Update{{Item: item}}, nil
}

func nullable(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`null`)
	}
	return raw
}
func encoded(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
