package agentdaemon

import (
	"testing"
)

func TestParseCredentialBindings(t *testing.T) {
	t.Run("nil config returns empty map", func(t *testing.T) {
		got := ParseCredentialBindings(nil)
		if len(got) != 0 {
			t.Fatalf("expected empty map, got %v", got)
		}
	})

	t.Run("shared binding from agent config", func(t *testing.T) {
		got := ParseCredentialBindings(map[string]any{
			"credential_bindings": map[string]any{
				"gitlab_token": map[string]any{
					"source":    "shared",
					"secret_id": "sec-1",
				},
			},
		})
		b, ok := got["gitlab_token"]
		if !ok {
			t.Fatal("expected gitlab_token binding")
		}
		if !b.IsShared() {
			t.Fatalf("expected shared binding, got %+v", b)
		}
		if b.SecretID != "sec-1" {
			t.Fatalf("expected secret_id sec-1, got %q", b.SecretID)
		}
	})

	t.Run("shared without secret_id is dropped", func(t *testing.T) {
		got := ParseCredentialBindings(map[string]any{
			"credential_bindings": map[string]any{
				"gitlab_token": map[string]any{
					"source": "shared",
				},
			},
		})
		if _, ok := got["gitlab_token"]; ok {
			t.Fatalf("expected dropped, got %v", got)
		}
	})

	t.Run("personal source maps to non-shared", func(t *testing.T) {
		got := ParseCredentialBindings(map[string]any{
			"credential_bindings": map[string]any{
				"gitlab_token": map[string]any{
					"source": "personal",
				},
			},
		})
		b := got["gitlab_token"]
		if b.IsShared() {
			t.Fatalf("personal binding should not be shared, got %+v", b)
		}
		if b.Source != CredentialBindingPersonal {
			t.Fatalf("expected personal source, got %q", b.Source)
		}
	})

	t.Run("unknown source values dropped", func(t *testing.T) {
		got := ParseCredentialBindings(map[string]any{
			"credential_bindings": map[string]any{
				"gitlab_token": map[string]any{
					"source": "magic",
				},
			},
		})
		if _, ok := got["gitlab_token"]; ok {
			t.Fatalf("expected unknown source dropped, got %v", got)
		}
	})
}

func TestParseModelCredentialBinding(t *testing.T) {
	t.Run("nil config returns false", func(t *testing.T) {
		_, ok := ParseModelCredentialBinding(nil)
		if ok {
			t.Fatal("expected ok=false for nil config")
		}
	})

	t.Run("shared binding parsed", func(t *testing.T) {
		b, ok := ParseModelCredentialBinding(map[string]any{
			"model_credential_binding": map[string]any{
				"source":    "shared",
				"secret_id": "model-sec",
			},
		})
		if !ok || !b.IsShared() || b.SecretID != "model-sec" {
			t.Fatalf("expected shared model binding, got %+v ok=%v", b, ok)
		}
	})

	t.Run("personal source rejected", func(t *testing.T) {
		_, ok := ParseModelCredentialBinding(map[string]any{
			"model_credential_binding": map[string]any{
				"source": "personal",
			},
		})
		if ok {
			t.Fatal("personal model binding should return ok=false (no override needed)")
		}
	})
}
