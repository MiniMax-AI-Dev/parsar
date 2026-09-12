package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

func resolveFunctions(input []v1.FunctionToolInput) ([]json.RawMessage, error) {
	tools := make([]json.RawMessage, 0, len(input))
	if len(input) > 64 {
		return nil, errors.New("This service supports at most 64 function tools.")
	}
	names := make(map[string]bool, len(input))
	for _, tool := range input {
		if tool.Type != "function" || tool.Name == nil || tool.Description == nil {
			return nil, errors.New("Function tools require type=function, name and description.")
		}
		if strings.TrimSpace(*tool.Name) == "" || len(*tool.Name) > 512 || names[*tool.Name] {
			return nil, errors.New("Function names must be nonempty, unique and at most 512 bytes.")
		}
		var schema map[string]json.RawMessage
		if json.Unmarshal(tool.Parameters, &schema) != nil || schema == nil {
			return nil, errors.New("Function parameters must be a JSON Schema object.")
		}
		deferred := false
		if len(tool.DeferLoading) > 0 && (bytes.Equal(bytes.TrimSpace(tool.DeferLoading), []byte("null")) || json.Unmarshal(tool.DeferLoading, &deferred) != nil) {
			return nil, errors.New("defer_loading must be a boolean when supplied.")
		}
		if deferred {
			return nil, errors.New("Deferred function discovery is not supported by this service yet.")
		}
		value, err := json.Marshal(struct {
			Type         string          `json:"type"`
			Name         string          `json:"name"`
			Description  string          `json:"description"`
			Parameters   json.RawMessage `json:"parameters"`
			DeferLoading bool            `json:"defer_loading"`
		}{"function", *tool.Name, *tool.Description, tool.Parameters, false})
		if err != nil {
			return nil, err
		}
		names[*tool.Name] = true
		tools = append(tools, value)
	}
	return tools, nil
}
