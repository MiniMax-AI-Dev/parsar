package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

func TestStrictResumeDoesNotStartFresh(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "strict"}[strict], func(t *testing.T) {
			client, server, cleanup := NewTestClient()
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			s := &Session{rpc: client.JSONRPCClient, cancelCtx: ctx, cfg: sessionConfig{logger: log.With("component", "resume-test")}}
			result := make(chan error, 1)
			go func() {
				result <- s.resolveThread(proto.PromptRequestPayload{AgentSessionID: "existing", StrictResume: strict}, SessionPlan{})
			}()
			var req struct {
				ID     string `json:"id"`
				Method string `json:"method"`
			}
			decoder := json.NewDecoder(server.FromClient)
			encoder := json.NewEncoder(server.ToClient)
			if err := decoder.Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req.Method != "thread/resume" {
				t.Fatal(req.Method)
			}
			if err := encoder.Encode(map[string]any{"id": req.ID, "error": map[string]any{"code": -32600, "message": "native history unavailable"}}); err != nil {
				t.Fatal(err)
			}
			if !strict {
				if err := decoder.Decode(&req); err != nil {
					t.Fatal(err)
				}
				if req.Method != "thread/start" {
					t.Fatal(req.Method)
				}
				if err := encoder.Encode(map[string]any{"id": req.ID, "result": map[string]any{"thread": map[string]string{"id": "fresh"}}}); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-result:
				if (err != nil) != strict {
					t.Fatalf("strict=%v err=%v", strict, err)
				}
			case <-ctx.Done():
				t.Fatal("thread resolution did not finish")
			}
		})
	}
}

func TestCancellationKeepsConsumedTerminalOutputAndUsage(t *testing.T) {
	out := make(chan proto.Envelope, 8)
	s := &Session{runID: "run", out: out, cancelCtx: context.Background(), cfg: sessionConfig{logger: log.With("component", "cancel-test")}}
	s.setThreadID("native")
	s.appendFinalText("Already produced")
	s.onTurnCompleted(json.RawMessage(`{"threadId":"native","turn":{"id":"turn","status":"interrupted","usage":{"inputTokens":31,"outputTokens":7}}}`))
	snapshot := s.CancellationOutcome()
	if snapshot.Content != "Already produced" || snapshot.Usage.InputTokens != 31 || snapshot.Usage.OutputTokens != 7 || snapshot.Metadata[proto.DoneMetaAgentSessionID] != "native" {
		t.Fatalf("incomplete terminal snapshot: %+v", snapshot)
	}
}
