package credentialbinding

import (
	"strings"
	"testing"
)

func TestParseStrict(t *testing.T) {
	bindings, err := ParseStrict(map[string]any{
		"credential_bindings": map[string]any{
			"github_pat": map[string]any{"source": "personal"},
			"mcp_oauth":  map[string]any{"source": "shared", "secret_id": "secret-1"},
		},
	})
	if err != nil {
		t.Fatalf("ParseStrict returned error: %v", err)
	}
	if bindings["github_pat"].Source != SourcePersonal {
		t.Fatalf("personal binding = %#v", bindings["github_pat"])
	}
	if !bindings["mcp_oauth"].IsShared() || bindings["mcp_oauth"].SecretID != "secret-1" {
		t.Fatalf("shared binding = %#v", bindings["mcp_oauth"])
	}

	_, err = ParseStrict(map[string]any{
		"credential_bindings": map[string]any{
			"mcp_oauth": map[string]any{"source": "shared"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "secret_id") {
		t.Fatalf("missing secret_id error = %v", err)
	}
}

func TestParseLenientDropsMalformedBindings(t *testing.T) {
	bindings := ParseLenient(map[string]any{
		"credential_bindings": map[string]any{
			"valid":   map[string]any{"source": "shared", "secret_id": "secret-1"},
			"missing": map[string]any{"source": "shared"},
			"unknown": map[string]any{"source": "other"},
			"legacy":  map[string]any{},
		},
	})
	if len(bindings) != 2 || !bindings["valid"].IsShared() || bindings["legacy"].Source != SourcePersonal {
		t.Fatalf("bindings = %#v", bindings)
	}
}
