package dev

import (
	"strings"
	"testing"
)

func TestValidateAgentVisibilityBindings(t *testing.T) {
	t.Run("non-public visibility is always ok", func(t *testing.T) {
		if err := validateAgentVisibilityBindings("workspace", map[string]any{
			"credential_bindings": map[string]any{
				"k": map[string]any{"source": "personal"},
			},
		}); err != nil {
			t.Fatalf("workspace + personal should be ok, got %v", err)
		}
		if err := validateAgentVisibilityBindings("tenant", map[string]any{
			"credential_bindings": map[string]any{
				"k": map[string]any{"source": "personal"},
			},
		}); err != nil {
			t.Fatalf("tenant + personal should be ok, got %v", err)
		}
	})

	t.Run("public + personal capability binding rejected", func(t *testing.T) {
		err := validateAgentVisibilityBindings("public", map[string]any{
			"credential_bindings": map[string]any{
				"gitlab_token": map[string]any{"source": "personal"},
			},
		})
		if err == nil {
			t.Fatal("expected rejection for public + personal")
		}
		if !strings.Contains(err.Error(), "gitlab_token") {
			t.Fatalf("error should name the kind, got %q", err)
		}
	})

	t.Run("public + shared capability binding ok", func(t *testing.T) {
		err := validateAgentVisibilityBindings("public", map[string]any{
			"credential_bindings": map[string]any{
				"gitlab_token": map[string]any{
					"source":    "shared",
					"secret_id": "sec-1",
				},
			},
		})
		if err != nil {
			t.Fatalf("public + shared should be ok, got %v", err)
		}
	})

	t.Run("public + shared without secret_id rejected", func(t *testing.T) {
		err := validateAgentVisibilityBindings("public", map[string]any{
			"credential_bindings": map[string]any{
				"gitlab_token": map[string]any{
					"source": "shared",
				},
			},
		})
		if err == nil {
			t.Fatal("expected rejection for shared without secret_id")
		}
	})

	t.Run("public + personal model binding rejected", func(t *testing.T) {
		err := validateAgentVisibilityBindings("public", map[string]any{
			"model_credential_binding": map[string]any{
				"source": "personal",
			},
		})
		if err == nil {
			t.Fatal("expected rejection for public + personal model binding")
		}
	})

	t.Run("public + shared model binding ok", func(t *testing.T) {
		err := validateAgentVisibilityBindings("public", map[string]any{
			"model_credential_binding": map[string]any{
				"source":    "shared",
				"secret_id": "model-sec",
			},
		})
		if err != nil {
			t.Fatalf("public + shared model binding should be ok, got %v", err)
		}
	})

	t.Run("public with no bindings at all is ok", func(t *testing.T) {
		// Public agent with no credential needs (skill-only) is legal.
		if err := validateAgentVisibilityBindings("public", map[string]any{}); err != nil {
			t.Fatalf("public + no bindings should be ok, got %v", err)
		}
	})
}

func TestMaskSecretValue(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"short", ""},
		{"abcdefgh", "ab…gh"},
		{"verylongtoken123456", "ve…56"},
	}
	for _, c := range cases {
		got := maskSecretValue(c.in)
		if got != c.want {
			t.Errorf("maskSecretValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
