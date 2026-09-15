package api

import (
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

func resolveSessionTools(input []json.RawMessage) ([]json.RawMessage, error) {
	tools := make([]json.RawMessage, len(input))
	functions := make([]v1.FunctionToolInput, 0, len(input))
	positions := make([]int, 0, len(input))
	servers := map[string]bool{}
	for i, raw := range input {
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &kind) != nil {
			return nil, errors.New("Invalid execution tool configuration.")
		}
		switch kind.Type {
		case "mcp":
			resolved, err := resolveMCPTool(raw, false)
			if err != nil {
				return nil, err
			}
			var tool v1.MCPTool
			if json.Unmarshal(resolved, &tool) != nil || servers[tool.ServerLabel] {
				return nil, errors.New("Execution requires distinct MCP server labels.")
			}
			servers[tool.ServerLabel] = true
			tools[i] = resolved
		case "function":
			var function v1.FunctionToolInput
			if decodeInputObject(raw, &function, "type", "name", "description", "parameters", "defer_loading") != nil {
				return nil, errors.New("Invalid execution function fields.")
			}
			functions = append(functions, function)
			positions = append(positions, i)
		default:
			return nil, errors.New("Execution currently supports non-deferred functions and the service-origin HTTP MCP profile only.")
		}
	}
	resolved, err := resolveFunctions(functions)
	if err != nil {
		return nil, err
	}
	for i, value := range resolved {
		tools[positions[i]] = value
	}
	return tools, nil
}
