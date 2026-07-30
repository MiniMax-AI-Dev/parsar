package agentdaemon

import (
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/credentialbinding"
)

// CredentialBindingSource discriminates how an agent-level credential
// binding resolves at runtime.
//
//   - "personal" — default; resolves per-user from user_credentials by
//     credential_kind_code + caller user_id.
//   - "shared"   — resolves once from the workspace secrets table by
//     SecretID; the same plaintext is served to every caller.
//
// Bindings are read from agent_config.credential_bindings[<kind_code>].
type CredentialBindingSource = credentialbinding.Source

const (
	CredentialBindingPersonal = credentialbinding.SourcePersonal
	CredentialBindingShared   = credentialbinding.SourceShared
)

// CredentialBinding is the parsed agent-level binding for one credential
// kind. Source=="" is treated as personal (back-compat with agents created
// before credential_bindings existed).
type CredentialBinding = credentialbinding.Binding

// ParseCredentialBindings extracts the credential_bindings map from the
// agent_config.
//
// Returns map[kind_code]CredentialBinding. Unknown source values are
// dropped silently to avoid hard-failing a run on a malformed config;
// callers fall back to personal in that case.
func ParseCredentialBindings(agentConfig map[string]any) map[string]CredentialBinding {
	return credentialbinding.ParseLenient(agentConfig)
}

func mergeBindings(out map[string]CredentialBinding, cfg map[string]any) {
	credentialbinding.MergeLenient(out, cfg)
}

// ParseModelCredentialBinding extracts the optional model_credential_binding
// object from agent_config. Returns (binding, true) when a usable shared
// binding is found.
func ParseModelCredentialBinding(agentConfig map[string]any) (CredentialBinding, bool) {
	if b, ok := pickModelBinding(agentConfig); ok {
		return b, true
	}
	return CredentialBinding{}, false
}

func pickModelBinding(cfg map[string]any) (CredentialBinding, bool) {
	if len(cfg) == 0 {
		return CredentialBinding{}, false
	}
	raw, ok := cfg["model_credential_binding"]
	if !ok {
		return CredentialBinding{}, false
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return CredentialBinding{}, false
	}
	source, _ := obj["source"].(string)
	secretID, _ := obj["secret_id"].(string)
	if CredentialBindingSource(strings.TrimSpace(source)) != CredentialBindingShared {
		return CredentialBinding{}, false
	}
	if strings.TrimSpace(secretID) == "" {
		return CredentialBinding{}, false
	}
	return CredentialBinding{
		Source:   CredentialBindingShared,
		SecretID: strings.TrimSpace(secretID),
	}, true
}
