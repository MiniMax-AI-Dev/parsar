package execution

import (
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func profileError(err error) error {
	if errors.Is(err, engine.ErrInvalidInput) {
		return store.ErrInvalidInput
	}
	return err
}

func validateProfileConfiguration(profile engine.Profile, snapshot Snapshot) error {
	if profile.ValidateConfiguration != nil {
		if err := profile.ValidateConfiguration(snapshot.Agent, snapshot.Environment, snapshot.Daemon != nil); err != nil {
			return profileError(err)
		}
	}
	functions, mcp, err := executionTools(snapshot.Agent.Tools)
	// Preserve each profile's admission error precedence when tool decoding fails.
	if profile.ValidateConfiguration != nil && err != nil {
		return err
	}
	if profile.ValidateTools != nil {
		if validationErr := profile.ValidateTools(snapshot.Environment, snapshot.Daemon != nil, functions, mcp); validationErr != nil {
			return profileError(validationErr)
		}
	}
	return err
}

func validateProfileInputs(profile engine.Profile, inputs []store.Input) error {
	if profile.ValidateFunctionResult == nil {
		return nil
	}
	for _, input := range inputs {
		if input.Kind != "tool_result" {
			continue
		}
		var value store.FunctionResultInput
		if json.Unmarshal(input.Payload, &value) != nil {
			return store.ErrInvalidInput
		}
		result, err := functionResult(store.FunctionCall{CallID: value.CallID, Result: value.Result})
		if err != nil {
			return store.ErrInvalidInput
		}
		if err := profile.ValidateFunctionResult(result.Content); err != nil {
			return profileError(err)
		}
	}
	return nil
}
