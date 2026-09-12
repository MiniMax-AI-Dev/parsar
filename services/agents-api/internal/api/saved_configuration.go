package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"unicode/utf8"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func resolveSavedAgent(input v1.CreateAgentRequest) (store.CreateAgentInput, error) {
	if input.Model == nil {
		return store.CreateAgentInput{}, errors.New("model is required and must be a string.")
	}
	if input.Name != nil && utf8.RuneCountInString(*input.Name) > 128 {
		return store.CreateAgentInput{}, errors.New("name must be at most 128 characters.")
	}
	metadata, err := stringMetadata(input.Metadata)
	if err != nil {
		return store.CreateAgentInput{}, errors.New("metadata values must be strings.")
	}
	if err := validateMetadata(metadata); err != nil {
		return store.CreateAgentInput{}, err
	}
	cfg := v1.SavedAgentConfiguration{Model: *input.Model, Name: input.Name, Instructions: input.Instructions, ServiceTier: "auto"}
	cfg.MultiAgent, err = resolveSavedMultiAgent(input.MultiAgent)
	if err != nil {
		return store.CreateAgentInput{}, err
	}
	if input.ServiceTier != nil {
		if !slices.Contains([]string{"auto", "default", "flex", "priority", "fast"}, *input.ServiceTier) {
			return store.CreateAgentInput{}, errors.New("service_tier must be auto, default, flex, priority or fast.")
		}
		cfg.ServiceTier = *input.ServiceTier
	}
	if input.Reasoning != nil {
		cfg.Reasoning = *input.Reasoning
		if cfg.Reasoning.Effort != nil && !slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}, *cfg.Reasoning.Effort) {
			return store.CreateAgentInput{}, errors.New("reasoning.effort is not a supported protocol value.")
		}
		if cfg.Reasoning.Summary != nil && !slices.Contains([]string{"concise", "detailed", "auto"}, *cfg.Reasoning.Summary) {
			return store.CreateAgentInput{}, errors.New("reasoning.summary must be concise, detailed or auto.")
		}
	}
	// Model-derived effort resolution is a recorded gap. Do not manufacture a
	// default from the operator's execution engine or another model's catalog.
	cfg.Text, err = resolveSavedText(input.Text)
	if err != nil {
		return store.CreateAgentInput{}, err
	}
	cfg.Tools, err = resolveSavedTools(input.Tools)
	if err != nil {
		return store.CreateAgentInput{}, err
	}
	configuration, err := json.Marshal(cfg)
	return store.CreateAgentInput{Metadata: metadata, Configuration: configuration}, err
}

func resolveSavedMultiAgent(raw json.RawMessage) (v1.MultiAgentConfig, error) {
	result := v1.MultiAgentConfig{}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return result, nil
	}
	var input struct {
		Enabled *bool           `json:"enabled"`
		Max     json.RawMessage `json:"max_concurrent_subagents"`
	}
	if decodeInputObject(raw, &input, "enabled", "max_concurrent_subagents") != nil || input.Enabled == nil {
		return result, errors.New("multi_agent requires enabled as a boolean.")
	}
	maximum := uint32(6)
	if len(input.Max) > 0 && (bytes.Equal(bytes.TrimSpace(input.Max), []byte("null")) || json.Unmarshal(input.Max, &maximum) != nil || maximum == 0) {
		return result, errors.New("max_concurrent_subagents must be an integer from 1 to 4294967295.")
	}
	result.Enabled = *input.Enabled
	if result.Enabled {
		value := int(maximum)
		result.MaxConcurrentSubagents = &value
	}
	return result, nil
}

func resolveSavedText(input *v1.SavedAgentTextInput) (v1.SavedAgentText, error) {
	result := v1.SavedAgentText{Format: v1.SavedAgentTextFormat{Type: "text"}, Verbosity: "medium"}
	if input == nil {
		return result, nil
	}
	// Reuse the Session verbosity policy without its narrower format admission.
	text, err := resolveText(&v1.TextConfigInput{Verbosity: input.Verbosity})
	if err != nil {
		return result, err
	}
	result.Verbosity = text.Verbosity
	if len(input.Format) == 0 || bytes.Equal(bytes.TrimSpace(input.Format), []byte("null")) {
		return result, nil
	}
	result.Format = v1.SavedAgentTextFormat{}
	if decodeInputObject(input.Format, &result.Format, "type", "schema") != nil {
		return result, errors.New("text.format must be a supported format object.")
	}
	switch result.Format.Type {
	case "text":
		if len(result.Format.Schema) > 0 {
			return result, errors.New("text format does not accept schema.")
		}
	case "json_schema":
		var schema map[string]json.RawMessage
		if json.Unmarshal(result.Format.Schema, &schema) != nil || schema == nil {
			return result, errors.New("json_schema format requires a schema object.")
		}
	default:
		return result, errors.New("text.format.type must be text or json_schema.")
	}
	return result, nil
}
