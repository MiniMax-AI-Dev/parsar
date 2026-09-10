package cli

import (
	"context"
	"os"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestSkillUploadUsesPairedAddressPerRequest(t *testing.T) {
	t.Setenv("PARSAR_SERVER_URL", "unchanged-process-env")
	env := map[string]any{"PARSAR_CAPABILITY_UPLOAD_TOKEN": "run-a", "PARSAR_SERVER_URL": "http://localhost:1234", "MODEL_KEY": "preserve"}
	opts := map[string]any{"env": env, "model": "preserve"}
	calls := 0
	factory := withSkillUploadServer(func(_ context.Context, req proto.PromptRequestPayload, _ chan<- proto.Envelope) (agent.Session, error) {
		calls++
		if calls == 1 {
			got := req.AgentOptions["env"].(map[string]any)
			if got["PARSAR_SERVER_URL"] != "http://parsar-server:8080" || got["PARSAR_CAPABILITY_UPLOAD_TOKEN"] != "run-a" || got["MODEL_KEY"] != "preserve" || req.AgentOptions["model"] != "preserve" {
				t.Fatal("per-run context incorrect")
			}
			if _, exists := got["PARSAR_RUNNER_TOKEN"]; exists {
				t.Fatal("exported device credential")
			}
		} else if len(req.AgentOptions) != 0 {
			t.Fatal("previous run's context leaked")
		}
		return nil, nil
	}, "http://parsar-server:8080")
	if _, err := factory(t.Context(), proto.PromptRequestPayload{AgentOptions: opts}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := factory(t.Context(), proto.PromptRequestPayload{}, nil); err != nil {
		t.Fatal(err)
	}
	if env["PARSAR_SERVER_URL"] != "http://localhost:1234" || os.Getenv("PARSAR_SERVER_URL") != "unchanged-process-env" {
		t.Fatal("mutated caller or process environment")
	}
}
