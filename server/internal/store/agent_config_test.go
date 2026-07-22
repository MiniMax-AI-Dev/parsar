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
