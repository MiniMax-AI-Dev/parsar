//go:build unix

package claudesdk

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestTextFactoryAcceptsRestrictiveCapabilities(t *testing.T) {
	for _, test := range []struct {
		name                   string
		environment, subagents bool
	}{
		{"environment", true, false}, {"subagents", false, true}, {"both", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PARSAR_HOME", root)
			config := Config{Node: os.Args[0], Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state"), Env: []string{"GO_CLAUDE_SDK_HELPER=1", "SDK_HELPER_MODE=success", "GORACE=atexit_sleep_ms=0"}}
			request := proto.PromptRequestPayload{RunID: "restricted-run", Prompt: "hello", AgentSessionID: "native-session", DisableExecutionEnvironment: test.environment, DisableSubagents: test.subagents, AgentOptions: map[string]any{"model": "fake-model", "system_prompt": "instructions"}}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			out := make(chan proto.Envelope, 16)
			running, err := NewFactory(config)(ctx, request, out)
			if err != nil {
				t.Fatal(err)
			}
			defer running.Cancel(context.Background())
			done := false
			for event := range out {
				if event.Type == proto.TypeError {
					t.Fatal("restricted execution failed", string(event.Payload))
				}
				if event.Type == proto.TypeDone {
					var payload proto.DonePayload
					if err := event.DecodePayload(&payload); err != nil || payload.Content != "final" || payload.Metadata[proto.DoneMetaAgentSessionID] != "native-session" {
						t.Fatal(payload, err)
					}
					done = true
				}
			}
			if !done {
				t.Fatal("restricted execution did not complete")
			}
		})
	}
}
