package codex

import (
	"context"
	"encoding/json"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"testing"
	"time"
)

func TestResumeRefreshesReferenceContext(t *testing.T) {
	for _, prompt := range []string{"updated knowledge reference", ""} {
		t.Run(prompt, func(t *testing.T) {
			client, server, cleanup := NewTestClient()
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			s := &Session{rpc: client.JSONRPCClient, cancelCtx: ctx, cfg: sessionConfig{logger: log.With("component", "knowledge-test")}}
			done := make(chan error, 1)
			go func() { done <- s.resumeThread("same-thread", SessionPlan{SystemPrompt: prompt}) }()
			var request struct {
				ID     string         `json:"id"`
				Params map[string]any `json:"params"`
			}
			if err := json.NewDecoder(server.FromClient).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if value, ok := request.Params["developerInstructions"]; !ok || value != prompt {
				t.Fatal("resume did not replace the old instructions")
			}
			if request.Params["threadId"] != "same-thread" {
				t.Fatal("lost history")
			}
			if err := json.NewEncoder(server.ToClient).Encode(map[string]any{"id": request.ID, "result": map[string]any{}}); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
