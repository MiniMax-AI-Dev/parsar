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
		value, deferred, err := resolveFunction(tool)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(*tool.Name) == "" || len(*tool.Name) > 512 || names[*tool.Name] {
			return nil, errors.New("Function names must be nonempty, unique and at most 512 bytes.")
		}
		if deferred {
			return nil, errors.New("Deferred function discovery is not supported by this service yet.")
		}

		names[*tool.Name] = true
		tools = append(tools, value)
	}
	return tools, nil
}

// resolveFunction validates the persisted wire shape. Execution admission may
// impose additional restrictions, without narrowing the reusable resource.
func resolveFunction(tool v1.FunctionToolInput) (json.RawMessage, bool, error) {
	if tool.Type != "function" || tool.Name == nil || tool.Description == nil {
		return nil, false, errors.New("Function tools require type=function, name and description.")
	}
	var schema map[string]json.RawMessage
	if json.Unmarshal(tool.Parameters, &schema) != nil || schema == nil {
		return nil, false, errors.New("Function parameters must be a JSON Schema object.")
	}
	deferred, err := optionalBoolean(tool.DeferLoading, false)
	if err != nil {
		return nil, false, errors.New("defer_loading must be a boolean when supplied.")
	}
	value, err := json.Marshal(struct {
		Type         string          `json:"type"`
		Name         string          `json:"name"`
		Description  string          `json:"description"`
		Parameters   json.RawMessage `json:"parameters"`
		DeferLoading bool            `json:"defer_loading"`
	}{"function", *tool.Name, *tool.Description, tool.Parameters, deferred})
	return value, deferred, err
}

func optionalBoolean(raw json.RawMessage, fallback bool) (bool, error) {
	if len(raw) == 0 {
		return fallback, nil
	}
	var value bool
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
		return false, errors.New("Expected a boolean.")
	}
	return value, nil
}
