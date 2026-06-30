package agentdaemon

import (
	"strings"
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
type CredentialBindingSource string

const (
	CredentialBindingPersonal CredentialBindingSource = "personal"
	CredentialBindingShared   CredentialBindingSource = "shared"
)

// CredentialBinding is the parsed agent-level binding for one credential
// kind. Source=="" is treated as personal (back-compat with agents created
// before credential_bindings existed).
type CredentialBinding struct {
	Source   CredentialBindingSource
	SecretID string
}

// IsShared returns true when this binding should bypass user_credentials
// lookup and serve a workspace secret instead.
func (b CredentialBinding) IsShared() bool {
	return b.Source == CredentialBindingShared && strings.TrimSpace(b.SecretID) != ""
}

// ParseCredentialBindings extracts the credential_bindings map from the
// agent_config.
//
// Returns map[kind_code]CredentialBinding. Unknown source values are
// dropped silently to avoid hard-failing a run on a malformed config;
// callers fall back to personal in that case.
func ParseCredentialBindings(agentConfig map[string]any) map[string]CredentialBinding {
	out := map[string]CredentialBinding{}
	mergeBindings(out, agentConfig)
	return out
}

func mergeBindings(out map[string]CredentialBinding, cfg map[string]any) {
	if len(cfg) == 0 {
		return
	}
	raw, ok := cfg["credential_bindings"]
	if !ok {
		return
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return
	}
	for kind, entry := range m {
		kind = strings.TrimSpace(kind)
		if kind == "" {
			continue
		}
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		source, _ := obj["source"].(string)
		secretID, _ := obj["secret_id"].(string)
		switch CredentialBindingSource(strings.TrimSpace(source)) {
		case CredentialBindingShared:
			if strings.TrimSpace(secretID) == "" {
				continue
			}
			out[kind] = CredentialBinding{
				Source:   CredentialBindingShared,
				SecretID: strings.TrimSpace(secretID),
			}
		case CredentialBindingPersonal, "":
			out[kind] = CredentialBinding{Source: CredentialBindingPersonal}
		}
	}
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
