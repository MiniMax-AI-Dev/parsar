package render

import (
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

// envValueToString turns a canonical.EnvValue into the literal-or-placeholder
// string scaffold runtimes will see. Placeholder format is identical across
// targets so the connector's substitution pass is target-agnostic. Cleartext
// for secrets/credentials is never materialized here — only at session spawn.
func envValueToString(v canonical.EnvValue) (string, error) {
	switch v.Mode {
	case canonical.EnvModeLiteral:
		return v.Literal, nil
	case canonical.EnvModeInlineSecret:
		return fmt.Sprintf("${PARSAR_SECRET:%s}", v.SecretID), nil
	case canonical.EnvModeCredentialRef:
		return fmt.Sprintf("${PARSAR_CREDENTIAL:%s}", v.CredentialKindCode), nil
	default:
		return "", fmt.Errorf("render: unknown env mode %q", v.Mode)
	}
}

// renderEnvMap flattens a canonical env map into string→string. Iteration
// order is not stable but JSON encoders sort map keys, so rendered bytes are.
func renderEnvMap(in map[string]canonical.EnvValue) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		s, err := envValueToString(v)
		if err != nil {
			return nil, fmt.Errorf("env %q: %w", k, err)
		}
		out[k] = s
	}
	return out, nil
}
