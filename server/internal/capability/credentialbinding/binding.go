package credentialbinding

import (
	"errors"
	"fmt"
	"strings"
)

type Source string

const (
	SourcePersonal Source = "personal"
	SourceShared   Source = "shared"
)

type Binding struct {
	Source   Source
	SecretID string
}

func (b Binding) IsShared() bool {
	return b.Source == SourceShared && strings.TrimSpace(b.SecretID) != ""
}

// ParseStrict validates the complete credential_bindings payload for API
// writes. Malformed entries are rejected instead of silently falling back.
func ParseStrict(config map[string]any) (map[string]Binding, error) {
	return parse(config, true)
}

// ParseLenient reads persisted bindings for runtime use. Invalid legacy rows
// are ignored so one malformed entry does not prevent an agent from starting.
func ParseLenient(config map[string]any) map[string]Binding {
	bindings, _ := parse(config, false)
	return bindings
}

func MergeLenient(target map[string]Binding, config map[string]any) {
	for kind, binding := range ParseLenient(config) {
		target[kind] = binding
	}
}

func parse(config map[string]any, strict bool) (map[string]Binding, error) {
	result := map[string]Binding{}
	if len(config) == 0 {
		return result, nil
	}
	raw, exists := config["credential_bindings"]
	if !exists || raw == nil {
		return result, nil
	}
	bindings, ok := raw.(map[string]any)
	if !ok {
		if strict {
			return nil, errors.New("credential_bindings must be an object")
		}
		return result, nil
	}
	for rawKind, rawBinding := range bindings {
		kind := strings.TrimSpace(rawKind)
		binding, ok := rawBinding.(map[string]any)
		if kind == "" || !ok {
			if strict {
				return nil, errors.New("credential_bindings entries must be non-empty objects")
			}
			continue
		}
		source := Source(strings.TrimSpace(stringValue(binding["source"])))
		secretID := strings.TrimSpace(stringValue(binding["secret_id"]))
		switch source {
		case SourcePersonal:
			result[kind] = Binding{Source: SourcePersonal}
		case SourceShared:
			if secretID == "" {
				if strict {
					return nil, fmt.Errorf("credential_bindings[%s].secret_id is required for shared source", kind)
				}
				continue
			}
			result[kind] = Binding{Source: SourceShared, SecretID: secretID}
		case "":
			if strict {
				return nil, fmt.Errorf("credential_bindings[%s].source must be personal or shared", kind)
			}
			result[kind] = Binding{Source: SourcePersonal}
		default:
			if strict {
				return nil, fmt.Errorf("credential_bindings[%s].source must be personal or shared", kind)
			}
		}
	}
	return result, nil
}

func stringValue(value any) string {
	valueString, _ := value.(string)
	return valueString
}
