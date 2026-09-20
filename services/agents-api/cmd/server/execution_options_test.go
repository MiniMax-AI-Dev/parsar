package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestExecutionOptionsStayTransientAndPerRequest(t *testing.T) {
	t.Setenv("AGENTS_API_EXECUTION_OPTIONS_FILE", "")
	if options, err := executionOptions(); options != nil || err != nil {
		t.Fatal("implicit options", err)
	}
	file := filepath.Join(t.TempDir(), "options.json")
	t.Setenv("AGENTS_API_EXECUTION_OPTIONS_FILE", file)
	if err := os.WriteFile(file, []byte(`{"codex_provider":{"bearer_token":"synthetic-private-canary","wire_api":"responses"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	resolve, err := executionOptions()
	if err != nil {
		t.Fatal(err)
	}
	first, err := resolve(t.Context(), store.Session{ID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	first["codex_provider"].(map[string]any)["bearer_token"] = "modified"
	next, err := resolve(t.Context(), store.Session{ID: "second"})
	if err != nil || next["codex_provider"].(map[string]any)["bearer_token"] != "synthetic-private-canary" {
		t.Fatal("options shared mutable state", err)
	}
	for _, raw := range []string{`null`, `[]`, `{"secret":"synthetic-private-canary"`} {
		if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := executionOptions(); err == nil || strings.Contains(err.Error(), "synthetic-private-canary") {
			t.Fatal("invalid or sensitive options error")
		}
	}
}

func TestExecutionOptionsSeparateHarnessCredentials(t *testing.T) {
	file := filepath.Join(t.TempDir(), "options.json")
	t.Setenv("AGENTS_API_EXECUTION_OPTIONS_FILE", file)
	t.Setenv("AGENTS_API_ENGINE", "codex")
	if err := os.WriteFile(file, []byte(`{"by_harness":{"codex":{"codex_provider":{"bearer_token":"codex-secret"}},"mcode":{"mcode_provider":{"api_key":"mcode-secret"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	resolve, err := executionOptions()
	if err != nil {
		t.Fatal(err)
	}
	options, err := resolve(t.Context(), store.Session{Engine: "mcode"})
	if err != nil || options["codex_provider"] != nil || options["mcode_provider"] == nil {
		t.Fatalf("wrong credential partition: %v", err)
	}
	if _, err := resolve(t.Context(), store.Session{Engine: "claude_sdk"}); err == nil {
		t.Fatal("missing harness fell back")
	}
	if err := os.WriteFile(file, []byte(`{"codex_provider":{"bearer_token":"codex-secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	resolve, err = executionOptions()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(t.Context(), store.Session{Engine: "mcode"}); err == nil {
		t.Fatal("legacy credentials crossed harness boundary")
	}
}
