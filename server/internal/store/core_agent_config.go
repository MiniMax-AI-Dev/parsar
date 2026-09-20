package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/credentialbinding"
	"github.com/openai/openai-go/v3"
	"strings"
)

func foldCoreAgentConfig(dst, src map[string]any) error {
	for key, value := range src {
		switch key {
		case "model_id":
			id, ok := value.(string)
			if !ok {
				return fmt.Errorf("%w: invalid catalog model ID", ErrInvalidInput)
			}
			if _, err := uuid(id); err != nil {
				return fmt.Errorf("%w: invalid catalog model ID", ErrInvalidInput)
			}
			dst[key] = id
		case "model":
			model, ok := value.(string)
			if !ok || strings.TrimSpace(model) == "" {
				return fmt.Errorf("%w: model must be a nonempty Core model name", ErrInvalidInput)
			}
			dst[key] = strings.TrimSpace(model)
		case "x_agents_core":
			raw, err := json.Marshal(value)
			var extension *v1.AgentsCore
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if err != nil || decoder.Decode(&extension) != nil {
				return fmt.Errorf("%w: invalid x_agents_core configuration", ErrInvalidInput)
			}
			if err := extension.Validate(); err != nil {
				return fmt.Errorf("%w: %s", ErrInvalidInput, err)
			}
			dst[key] = extension
		case "environment":
			selection, err := ParseCoreEnvironment(value)
			if err != nil {
				return err
			}
			dst[key] = selection
		case "tools", "multi_agent", "reasoning", "text", "service_tier":
			dst[key] = value
		case "credential_bindings":
			if _, err := credentialbinding.ParseStrict(map[string]any{key: value}); err != nil {
				return fmt.Errorf("%w: %s", ErrInvalidInput, err)
			}
			dst[key] = value
		case "model_credential_binding":
			if value == nil {
				continue
			}
			obj, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("%w: invalid model credential binding", ErrInvalidInput)
			}
			kind, _ := obj["kind"].(string)
			if !validModelCredentialKind(kind) {
				return fmt.Errorf("%w: model credentials require openai_api_key or anthropic_api_key", ErrInvalidInput)
			}
			if _, err := credentialbinding.ParseStrict(map[string]any{"credential_bindings": map[string]any{kind: obj}}); err != nil {
				return fmt.Errorf("%w: %s", ErrInvalidInput, err)
			}
			dst[key] = value
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
