package dev

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/credentialbinding"
)

// agentVisibilityPublic is the agents.visibility value that opens the
// agent to any lark user, including those without a parsar account.
// Such callers cannot have personal credentials, so any required
// credential must resolve via a shared workspace secret.
const agentVisibilityPublic = "public"

// validateAgentVisibilityBindings enforces the rule that public agents
// cannot rely on per-user credentials: every credential binding (and the
// optional model binding) must be source=shared. Workspace/tenant agents
// are permissive — the UI warns but the backend allows personal here.
func validateAgentVisibilityBindings(visibility string, cfg map[string]any) error {
	if strings.TrimSpace(visibility) != agentVisibilityPublic {
		return nil
	}
	bindings, err := credentialbinding.ParseStrict(cfg)
	if err != nil {
		return err
	}
	for kind, binding := range bindings {
		if !binding.IsShared() {
			return fmt.Errorf("public agents cannot use personal credentials (credential_bindings[%s].source=%q)", kind, binding.Source)
		}
	}
	// Optional model-level binding.
	if raw, ok := cfg["model_credential_binding"]; ok {
		if raw != nil {
			obj, ok := raw.(map[string]any)
			if !ok {
				return errors.New("model_credential_binding must be an object")
			}
			if len(obj) > 0 {
				source, _ := obj["source"].(string)
				if strings.TrimSpace(source) != "shared" {
					return fmt.Errorf("public agents cannot use personal model credentials (model_credential_binding.source=%q)", source)
				}
				secretID, _ := obj["secret_id"].(string)
				if strings.TrimSpace(secretID) == "" {
					return errors.New("model_credential_binding.secret_id is required for shared source")
				}
			}
		}
	}
	return nil
}

// maskSecretValue returns a UI-safe masked preview of plaintext:
// keep the first 2 and last 2 characters, replace the rest with "…".
// Empty / very short plaintext returns "".
func maskSecretValue(plaintext string) string {
	plaintext = strings.TrimSpace(plaintext)
	if len(plaintext) <= 6 {
		return ""
	}
	return plaintext[:2] + "…" + plaintext[len(plaintext)-2:]
}
