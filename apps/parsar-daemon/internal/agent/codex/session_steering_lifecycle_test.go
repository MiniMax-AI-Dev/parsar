package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

func TestSteeringReceiptTimeoutAndCompletionKeepProcessAlive(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(map[bool]string{false: "receipt timeout", true: "normal completion"}[complete], func(t *testing.T) {
			client, server, cleanup := NewTestClient()
			defer cleanup()
			out := make(chan proto.Envelope, 4)
			s := &Session{rpc: client.JSONRPCClient, cancelCtx: context.Background(), out: out, cfg: sessionConfig{logger: obslog.Bg()}}
			s.setThreadID("thread")
			s.startSteering(json.RawMessage(`{"threadId":"thread","turn":{"id":"turn"}}`))
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- s.Steer(ctx, proto.PromptSteerPayload{InputID: "input", Text: "extra"}) }()
			var request JsonRpcRequest
			if err := json.NewDecoder(server.FromClient).Decode(&request); err != nil {
				t.Fatal(err)
			}
			// Withhold the response after reading the entire request frame.
			if complete {
				s.onTurnCompleted(json.RawMessage(`{"threadId":"thread","turn":{"id":"turn","status":"completed"}}`))
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("missing receipt reported as accepted")
				}
			case <-time.After(time.Second):
				t.Fatal("steering response wait did not stop")
			}
			if !client.Alive() {
				t.Fatal("response wait killed retained process")
			}
			if complete {
				s.emitTerminal("late disconnect", true)
				var frames []proto.Envelope
				for env := range out {
					frames = append(frames, env)
				}
				if len(frames) != 1 || frames[0].Type != proto.TypeDone {
					t.Fatalf("terminal frames: %+v", frames)
				}
			}
		})
	}
}

func TestBlockedSteeringWriteEndsRunWithTerminalFrames(t *testing.T) {
	client, server, cleanup := NewTestClient()
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out := make(chan proto.Envelope, 8)
	s := &Session{
		runID: "run", rpc: client.JSONRPCClient, cancelCtx: ctx, out: out,
		cfg: sessionConfig{logger: obslog.Bg()}, waitDone: make(chan struct{}), cleanup: func() {},
		bufs: NewItemBuffers(), interactions: newPendingCodexInteractions(),
	}
	s.registerHandlers()
	ready := make(chan error, 1)
	go func() {
		decoder := json.NewDecoder(server.FromClient)
		encoder := json.NewEncoder(server.ToClient)
		for _, method := range []string{"thread/start", "turn/start"} {
			var request JsonRpcRequest
			if err := decoder.Decode(&request); err != nil {
				ready <- err
				return
			}
			if request.Method != method {
				t.Errorf("method = %s, expected %s", request.Method, method)
			}
			if method == "turn/start" {
				if err := SendNotification(server, "turn/started", map[string]any{"threadId": "thread", "turn": map[string]any{"id": "turn"}}); err != nil {
					ready <- err
					return
				}
			}
			if err := encoder.Encode(map[string]any{"id": request.ID, "result": map[string]any{"thread": map[string]any{"id": "thread"}}}); err != nil {
				ready <- err
				return
			}
		}
		ready <- nil
	}()
	go s.run(SessionPlan{Model: "synthetic"}, proto.PromptRequestPayload{Prompt: "first"})
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	callCtx, callCancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer callCancel()
	if err := s.Steer(callCtx, proto.PromptSteerPayload{InputID: "blocked", Text: "extra"}); err == nil {
		t.Fatal("blocked write accepted")
	}
	if client.Alive() {
		t.Fatal("blocked transport still alive")
	}
	// TestClient has no child waiter; emulate the process exit after pipe close.
	close(client.doneCh)
	select {
	case <-s.waitDone:
	case <-ctx.Done():
		t.Fatal("run did not end after native disconnect")
	}
	var frames []proto.Envelope
	for env := range out {
		frames = append(frames, env)
	}
	if len(frames) != 2 || frames[0].Type != proto.TypeError || frames[1].Type != proto.TypeDone {
		t.Fatalf("missing honest terminal outcome: %+v", frames)
	}
}
