package dev

import (
	"context"
	"fmt"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func requestedAgentResources(ctx context.Context, st RuntimeStore, agentID string, requested *[]store.InitialAgentCapabilityInput) ([]store.InitialAgentCapabilityInput, error) {
	if requested != nil {
		return *requested, nil
	}
	bindings, err := st.ListAgentCapabilities(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]store.InitialAgentCapabilityInput, 0, len(bindings))
	for _, b := range bindings {
		if b.Enabled {
			out = append(out, store.InitialAgentCapabilityInput{CapabilityVersionID: b.CapabilityVersionID, Configuration: b.Configuration, PinningMode: b.PinningMode})
		}
	}
	return out, nil
}

func validateAgentResources(ctx context.Context, st RuntimeStore, workspaceID, visibility string, config map[string]any, bindings []store.InitialAgentCapabilityInput) error {
	if len(bindings) > 64 {
		return fmt.Errorf("%w: select at most 64 resources", store.ErrInvalidInput)
	}
	invalid := func(err error) error { return fmt.Errorf("%w: %s", store.ErrInvalidInput, err) }
	if err := validateAgentVisibilityBindings(visibility, config); err != nil {
		return invalid(err)
	}
	if raw, ok := config["model_credential_binding"].(map[string]any); ok && raw["source"] == "shared" {
		kind, _ := raw["kind"].(string)
		id, _ := raw["secret_id"].(string)
		if err := validateSharedCapabilityCredential(ctx, st, workspaceID, kind, id, nil); err != nil {
			return invalid(err)
		}
	}
	for _, binding := range bindings {
		if !isUUID(binding.CapabilityVersionID) {
			return fmt.Errorf("%w: invalid capability version", store.ErrInvalidInput)
		}
		version, err := st.GetCapabilityVersion(ctx, binding.CapabilityVersionID)
		if err != nil {
			return err
		}
		capability, err := st.GetCapability(ctx, version.CapabilityID)
		if err != nil {
			return err
		}
		if capability.WorkspaceID != workspaceID && capability.Visibility != "public" {
			return store.ErrUnknownCapability
		}
		if binding.PinningMode == "latest" && capability.LatestVersionID != "" {
			version, err = st.GetCapabilityVersion(ctx, capability.LatestVersionID)
			if err != nil {
				return err
			}
		}
		if err := validateCapabilityCredentialBindings(ctx, st, capabilityCredentialBindingValidationInput{WorkspaceID: workspaceID, AgentVisibility: visibility, AgentConfig: config, Version: version, Configuration: binding.Configuration}); err != nil {
			return invalid(err)
		}
	}
	return nil
}
