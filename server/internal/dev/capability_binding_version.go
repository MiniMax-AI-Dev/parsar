package dev

import (
	"context"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type capabilityBindingVersionStore interface {
	credentialBindingSecretStore
	GetCapability(context.Context, string) (store.CapabilityRead, error)
	ListCapabilityVersions(context.Context, string) ([]store.CapabilityVersionRead, error)
}

func validateBoundCapabilityCredentials(ctx context.Context, source capabilityBindingVersionStore, input capabilityCredentialBindingValidationInput) error {
	boundVersionID := input.Version.ID
	if strings.TrimSpace(input.PinningMode) == store.PinningModeLatest {
		capability, err := source.GetCapability(ctx, input.Version.CapabilityID)
		if err != nil {
			return err
		}
		if capability.Type == "mcp" {
			versions, err := source.ListCapabilityVersions(ctx, capability.ID)
			if err != nil {
				return err
			}
			found := false
			for _, version := range versions {
				if capability.DeprecatedAt == nil || !version.CreatedAt.After(*capability.DeprecatedAt) {
					input.Version, found = version, true
					break
				}
			}
			if !found {
				return fmt.Errorf("no available version for capability %s", capability.ID)
			}
		}
	}
	// A newer version may retire credentials retained in an existing binding.
	input.AllowUnusedBindings = input.AllowUnusedBindings && input.Version.ID != boundVersionID
	return validateCapabilityCredentialBindings(ctx, source, input)
}
