package cli

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/authoring"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestAuthoringSocketEndsWithTurnWhileSessionIsRetained(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "pa-wrap-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	t.Setenv("HOME", home)
	env := map[string]any{"MODEL_KEY": "preserved"}
	var path string
	var events chan<- proto.Envelope
	factory := withAuthoringBridge(func(_ context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		injected := req.AgentOptions["env"].(map[string]any)
		path, _ = injected[proto.AuthoringSocketEnv].(string)
		if path == "" || injected["MODEL_KEY"] != "preserved" {
			t.Fatal("missing per-run context")
		}
		events = out
		return nil, nil
	}, authoring.New(nil))
	out := make(chan proto.Envelope, 1)
	_, err = factory(t.Context(), proto.PromptRequestPayload{RunID: "run", WorkspaceAuthoring: true, AgentOptions: map[string]any{"env": env}}, out)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := env[proto.AuthoringSocketEnv]; exists {
		t.Fatal("mutated caller environment")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	done, _ := proto.NewEnvelope(proto.TypeDone, "run", proto.DonePayload{})
	events <- done
	select {
	case <-out:
	case <-time.After(time.Second):
		t.Fatal("terminal event blocked")
	}
	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		_ = conn.Close()
		t.Fatal("authoring remained available after done")
	}
	close(events)
}
