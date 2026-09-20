package store

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// CoreEnvironmentSelection contains only non-confidential Session selectors.
// Initialization payloads belong to Core environment templates, not conversation
// metadata, which is visible in product history and shared views.
type CoreEnvironmentSelection struct {
	Type                  string   `json:"type"`
	EnvironmentTemplateID string   `json:"environment_template_id,omitempty"`
	WorkspaceDirectory    string   `json:"workspace_directory,omitempty"`
	CapabilityDirectories []string `json:"capability_directories,omitempty"`
}

func (e CoreEnvironmentSelection) Validate() error {
	switch e.Type {
	case "openai_hosted":
		if e.WorkspaceDirectory != "" || len(e.CapabilityDirectories) > 0 {
			return fmt.Errorf("%w: use an environment template for hosted initialization", ErrInvalidInput)
		}
	case "self_hosted":
		if e.EnvironmentTemplateID != "" || !path.IsAbs(e.WorkspaceDirectory) {
			return fmt.Errorf("%w: self-hosted environments require an absolute workspace_directory and no hosted template", ErrInvalidInput)
		}
		for _, directory := range e.CapabilityDirectories {
			if !path.IsAbs(directory) {
				return fmt.Errorf("%w: capability directories must be absolute", ErrInvalidInput)
			}
		}
	case "none":
		if e.EnvironmentTemplateID != "" || e.WorkspaceDirectory != "" || len(e.CapabilityDirectories) > 0 {
			return fmt.Errorf("%w: none environments have no settings", ErrInvalidInput)
		}
	default:
		return fmt.Errorf("%w: invalid environment type", ErrInvalidInput)
	}
	if strings.TrimSpace(e.EnvironmentTemplateID) != e.EnvironmentTemplateID {
		return fmt.Errorf("%w: invalid environment_template_id", ErrInvalidInput)
	}
	return nil
}

func conversationCoreMetadata(input CreateWorkspaceConversationInput) (map[string]any, error) {
	metadata := make(map[string]any, len(input.Metadata)+1)
	for key, value := range input.Metadata {
		if key == "core_environment" {
			return nil, fmt.Errorf("%w: core_environment metadata is reserved; use environment", ErrInvalidInput)
		}
		metadata[key] = value
	}
	environment := CoreEnvironmentSelection{Type: "openai_hosted"}
	if input.Environment != nil {
		environment = *input.Environment
	}
	if err := environment.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(environment)
	if err != nil {
		return nil, err
	}
	metadata["core_environment"] = json.RawMessage(raw)
	return metadata, nil
}
