package api

import (
	"bytes"
	"context"
	"encoding/json"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// Validate the reference and inline shape without looking up mutable resources.
// Caller intent remains available before any reference is resolved.
func decodeTemplateEnvironment(raw json.RawMessage) (*v1.Environment, string, json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, "", nil, store.ErrInvalidInput
	}
	reference, supplied := fields["environment_template_id"]
	if !supplied {
		environment, err := decodeSessionEnvironment(raw)
		return environment, "", nil, err
	}
	var id, kind string
	if json.Unmarshal(reference, &id) != nil || id == "" || json.Unmarshal(fields["type"], &kind) != nil || kind != "openai_hosted" {
		return nil, "", nil, store.ErrInvalidInput
	}
	// Explicit null override semantics are unconfirmed; do not guess inheritance.
	if value, exists := fields["network"]; exists && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil, "", nil, store.ErrInvalidInput
	}
	for _, name := range []string{"files", "env", "setup_commands", "packages", "skills"} {
		if _, supplied := fields[name]; supplied {
			return nil, "", nil, store.ErrInvalidInput
		}
	}
	delete(fields, "environment_template_id")
	inline, err := json.Marshal(fields)
	if err != nil {
		return nil, "", nil, err
	}
	environment, err := decodeHostedEnvironment(inline)
	return environment, id, raw, err
}

func (h *Handler) resolveTemplateEnvironment(ctx context.Context, tenant string, input *sessionRequest) error {
	if input.templateID == "" {
		return nil
	}
	template, files, err := h.store.ResolveEnvironmentTemplate(ctx, tenant, input.templateID)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(input.templateEnvironment, &fields) != nil {
		return store.ErrInvalidInput
	}
	if _, supplied := fields["network"]; !supplied {
		input.Environment.Network = &v1.EnvironmentNetworkInput{Access: template.NetworkAccess}
	} else if template.NetworkAccess == "disabled" && input.Environment.Network.Access != "disabled" {
		return store.ErrInvalidInput
	}
	input.initialization = template.Initialization
	input.Environment.Skills = skillResponse(template.Skills)
	packages := template.Initialization.PackageMetadata()
	input.Environment.Packages = &packages
	input.initialFiles = files
	input.Environment.Files = initialFileResponse(files)
	// Runtime sees only the effective ordinary hosted configuration.
	return nil
}
