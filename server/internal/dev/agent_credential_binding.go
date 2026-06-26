package dev

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// agentVisibilityPublic is the agents.visibility value that opens the
// agent to any lark user, including those without a parsar account.
// Such callers cannot have personal credentials, so any required
// credential must resolve via a shared workspace secret.
const agentVisibilityPublic = "public"

// materialiseInlineSecrets creates a CreateSecret row for every entry in
// the request's inline_new_secrets array and writes the freshly minted
// secret_id into req.Config under credential_bindings[Kind] (or
// model_credential_binding when IsModel). Returns the mutated config and
// true on success; nil + false when any secret create fails or when the
// inputs are malformed.
//
// The mutation is in-place on a clone of cfg — the caller is responsible
// for assigning the returned map back. cfg may be nil; the function
// allocates as needed.
func materialiseInlineSecrets(
	ctx context.Context,
	rs RuntimeStore,
	cfg map[string]any,
	inputs []createAgentInlineSecretBody,
	actorID string,
) (map[string]any, bool) {
	if len(inputs) == 0 {
		return cfg, true
	}
	if rs == nil {
		return nil, false
	}
	masterKey := os.Getenv("PARSAR_MASTER_KEY")
	if masterKey == "" {
		return nil, false
	}
	secretService, err := secrets.New(masterKey)
	if err != nil {
		return nil, false
	}
	out := cloneAnyMap(cfg)
	for _, ins := range inputs {
		kind := strings.TrimSpace(ins.Kind)
		plaintext := strings.TrimSpace(ins.Plaintext)
		if kind == "" || plaintext == "" {
			return nil, false
		}
		displayName := strings.TrimSpace(ins.DisplayName)
		if displayName == "" {
			displayName = kind
		}
		payload := map[string]any{"value": plaintext}
		encrypted, err := secretService.Encrypt(payload)
		if err != nil {
			return nil, false
		}
		secret, err := rs.CreateSecret(ctx, store.CreateSecretInput{
			Name:               displayName,
			Kind:               "capability_inline",
			Provider:           "inline",
			AuthType:           "literal",
			Payload:            payload,
			Masked:             maskSecretValue(plaintext),
			CreatedBy:          actorID,
			CredentialKindCode: kind,
		}, encrypted)
		if err != nil {
			return nil, false
		}
		assignSecretIDToBinding(out, kind, secret.ID, ins.IsModel)
	}
	return out, true
}

// assignSecretIDToBinding writes secret_id into cfg.credential_bindings[kind]
// (or cfg.model_credential_binding when isModel=true), creating nested maps
// as needed. Existing entries are overwritten — the secret the user just
// pasted is the source of truth at create time.
func assignSecretIDToBinding(cfg map[string]any, kind, secretID string, isModel bool) {
	if cfg == nil {
		return
	}
	if isModel {
		cfg["model_credential_binding"] = map[string]any{
			"source":    "shared",
			"secret_id": secretID,
		}
		return
	}
	bindings, ok := cfg["credential_bindings"].(map[string]any)
	if !ok {
		bindings = map[string]any{}
		cfg["credential_bindings"] = bindings
	}
	bindings[kind] = map[string]any{
		"source":    "shared",
		"secret_id": secretID,
	}
}

// validateAgentVisibilityBindings enforces the rule that public agents
// cannot rely on per-user credentials: every credential binding (and the
// optional model binding) must be source=shared. Workspace/tenant agents
// are permissive — the UI warns but the backend allows personal here.
func validateAgentVisibilityBindings(visibility string, cfg map[string]any) error {
	if strings.TrimSpace(visibility) != agentVisibilityPublic {
		return nil
	}
	// Per-capability credential bindings.
	if raw, ok := cfg["credential_bindings"]; ok {
		bindings, ok := raw.(map[string]any)
		if !ok {
			return errors.New("credential_bindings must be an object")
		}
		for kind, entry := range bindings {
			obj, ok := entry.(map[string]any)
			if !ok {
				return fmt.Errorf("credential_bindings[%s] must be an object", kind)
			}
			source, _ := obj["source"].(string)
			if strings.TrimSpace(source) != "shared" {
				return fmt.Errorf("public agents cannot use personal credentials (credential_bindings[%s].source=%q)", kind, source)
			}
			secretID, _ := obj["secret_id"].(string)
			if strings.TrimSpace(secretID) == "" {
				return fmt.Errorf("credential_bindings[%s].secret_id is required for shared source", kind)
			}
		}
	}
	// Optional model-level binding.
	if raw, ok := cfg["model_credential_binding"]; ok {
		obj, ok := raw.(map[string]any)
		if !ok {
			return errors.New("model_credential_binding must be an object")
		}
		source, _ := obj["source"].(string)
		if strings.TrimSpace(source) != "shared" {
			return fmt.Errorf("public agents cannot use personal model credentials (model_credential_binding.source=%q)", source)
		}
		secretID, _ := obj["secret_id"].(string)
		if strings.TrimSpace(secretID) == "" {
			return errors.New("model_credential_binding.secret_id is required for shared source")
		}
	}
	return nil
}

func cloneAnyMap(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		out[k] = v
	}
	return out
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
