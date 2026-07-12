package pi

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// TestNewSessionMaterialisesPiProviderModelsJSON proves agent_options["pi_provider"]
// flows through newSession → models.json on disk at the per-conversation agent
// dir. The env-forwarding of PI_CODING_AGENT_DIR is covered by the
// applyPiRuntimeState + buildEnv unit tests.
func TestNewSessionMaterialisesPiProviderModelsJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out := make(chan proto.Envelope, 64)
	req := proto.PromptRequestPayload{
		RunID:          "run_prov",
		ConversationID: "conv-prov",
		AgentStateKey:  "conv-prov/agent-prov/pi",
		Prompt:         "hello",
		AgentOptions: map[string]any{
			"model": "parsar/claude-opus-4-6-thinking-max",
			"pi_provider": map[string]any{
				"base_url":    "https://platform-api.example.com",
				"api":         "anthropic-messages",
				"api_key_env": "PARSAR_PI_API_KEY",
				"model":       "claude-opus-4-6-thinking-max",
				"headers":     map[string]any{"X-Sub-Module": "claude-code-internal"},
			},
			"env": map[string]any{
				"PI_TESTHELPER_ROLE": "json-success",
				"PARSAR_PI_API_KEY":  "sk-proxy",
			},
		},
	}
	sess, err := newSession(context.Background(), req, out, sessionConfig{
		piBinary:    os.Args[0],
		extraArgs:   []string{"-test.run=^$"},
		killTimeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	defer sess.Cancel(context.Background())

	deadline := time.After(5 * time.Second)
	for draining := true; draining; {
		select {
		case _, ok := <-out:
			if !ok {
				draining = false
			}
		case <-deadline:
			t.Fatal("out did not close")
		}
	}

	agentDir := filepath.Join(home, ".parsar", "runtime", "pi", "state", "conv-prov", "agent-prov", "pi", "agent")
	p := readModelsJSON(t, agentDir).Providers[piManagedProviderSlug]
	if p.BaseURL != "https://platform-api.example.com" {
		t.Fatalf("models.json baseUrl wrong: %+v", p)
	}
	if p.APIKey != "$PARSAR_PI_API_KEY" {
		t.Fatalf("models.json apiKey = %q, want $PARSAR_PI_API_KEY", p.APIKey)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "sessions")); err != nil {
		t.Fatalf("session dir not created: %v", err)
	}
}
