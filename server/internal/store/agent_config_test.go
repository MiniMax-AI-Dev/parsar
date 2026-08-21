package store

import (
	"errors"
	"testing"
)

func TestMergeAgentProfileOwnedConfigCodexMode(t *testing.T) {
	t.Run("persists plan mode", func(t *testing.T) {
		config, err := mergeAgentProfileOwnedConfig(
			map[string]any{"agent_kind": "codex", "mode": "default"},
			map[string]any{"mode": "plan"},
		)
		if err != nil {
			t.Fatal(err)
		}
		if config["mode"] != "plan" {
			t.Fatalf("mode = %#v, want plan", config["mode"])
		}
	})

	for _, badMode := range []any{"autopilot", true} {
		t.Run("rejects invalid Codex mode", func(t *testing.T) {
			_, err := mergeAgentProfileOwnedConfig(
				map[string]any{"agent_kind": "codex", "mode": "default"},
				map[string]any{"mode": badMode},
			)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("mode %#v returned %v, want ErrInvalidInput", badMode, err)
			}
		})
	}

	t.Run("clears Codex mode when engine changes", func(t *testing.T) {
		config, err := mergeAgentProfileOwnedConfig(
			map[string]any{"agent_kind": "codex", "mode": "plan"},
			map[string]any{"agent_kind": "claude_code"},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := config["mode"]; ok {
			t.Fatalf("Codex mode leaked into Claude config: %#v", config)
		}
	})

	t.Run("preserves another engine mode when omitted", func(t *testing.T) {
		config, err := mergeAgentProfileOwnedConfig(
			map[string]any{"agent_kind": "claude_code", "mode": "acceptEdits"},
			map[string]any{"agent_kind": "claude_code"},
		)
		if err != nil {
			t.Fatal(err)
		}
		if config["mode"] != "acceptEdits" {
			t.Fatalf("Claude mode was not preserved: %#v", config)
		}
	})
}

func TestMergeAgentProfileOwnedConfigSandboxLease(t *testing.T) {
	config, err := mergeAgentProfileOwnedConfig(map[string]any{}, map[string]any{
		"sandbox_ttl":        "2h",
		"sandbox_auto_renew": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if config["sandbox_ttl"] != "2h" || config["sandbox_auto_renew"] != true {
		t.Fatalf("sandbox lease config not persisted: %#v", config)
	}

	if _, err := mergeAgentProfileOwnedConfig(config, map[string]any{"sandbox_auto_renew": "yes"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid sandbox_auto_renew returned %v, want ErrInvalidInput", err)
	}
}

func TestFoldAgentDaemonConfigSandboxLease(t *testing.T) {
	config := map[string]any{}
	if err := foldAgentDaemonConfig(config, map[string]any{
		"sandbox_ttl":        "90m",
		"sandbox_auto_renew": false,
	}); err != nil {
		t.Fatal(err)
	}
	if config["sandbox_ttl"] != "90m" || config["sandbox_auto_renew"] != false {
		t.Fatalf("sandbox lease config not folded: %#v", config)
	}
}
