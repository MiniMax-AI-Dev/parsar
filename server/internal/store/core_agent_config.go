package store

import (
	"encoding/json"
	"fmt"
	"github.com/openai/openai-go/v3"
	"strings"
)

func foldCoreAgentConfig(dst, src map[string]any) error {
	for key, value := range src {
		switch key {
		case "model":
			model, ok := value.(string)
			if !ok || strings.TrimSpace(model) == "" {
				return fmt.Errorf("%w: model must be a nonempty Core model name", ErrInvalidInput)
			}
			dst[key] = strings.TrimSpace(model)
		case "tools", "multi_agent", "reasoning", "text", "service_tier":
			dst[key] = value
		case "credential_bindings", "model_credential_binding":
			return fmt.Errorf("%w: execution credentials are configured in Core", ErrInvalidInput)
		default:
			return fmt.Errorf("%w: unsupported Core Agent configuration field %s", ErrInvalidInput, key)
		}
	}
	raw, err := json.Marshal(dst)
	if err != nil {
		return fmt.Errorf("%w: invalid Core configuration", ErrInvalidInput)
	}
	var agent openai.BetaAgentSessionNewParamsAgent
	if err := json.Unmarshal(raw, &agent); err != nil {
		return fmt.Errorf("%w: invalid Core Agent configuration", ErrInvalidInput)
	}
	return nil
}

// ValidateCoreAgentConfig checks the Agent fields from the pinned Agents API contract.
func ValidateCoreAgentConfig(config map[string]any, requireModel bool) error {
	dst := map[string]any{}
	if err := foldCoreAgentConfig(dst, config); err != nil {
		return err
	}
	if requireModel && dst["model"] == nil {
		return fmt.Errorf("%w: a Core model name is required", ErrInvalidInput)
	}
	return nil
}
