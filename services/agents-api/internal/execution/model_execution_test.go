package execution

import (
	"context"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"testing"
)

func TestSessionModelExecutionNeverFallsBackToOperatorCredentials(t *testing.T) {
	called := false
	d := Dispatcher{Options: func(context.Context, store.Session) (map[string]any, error) {
		called = true
		return map[string]any{"model": "operator-fallback"}, nil
	}}
	_, err := d.executionRequest(t.Context(), store.Session{Engine: "codex"}, Snapshot{ModelProviderConfigured: true}, device.KindCapabilities{}, store.SessionExecutionBinding{})
	if err == nil || called {
		t.Fatal("missing Session credentials used operator fallback")
	}
	if _, err = d.executionRequest(t.Context(), store.Session{Engine: "codex"}, Snapshot{}, device.KindCapabilities{}, store.SessionExecutionBinding{}); err != nil || !called {
		t.Fatal("legacy operator configuration lost", err)
	}
}
