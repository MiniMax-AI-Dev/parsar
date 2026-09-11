package codex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestSteeringUsesNativeActiveTurnAndReceipt(t *testing.T) {
	for _, test := range []struct {
		name   string
		result any
		err    any
		wantOK bool
	}{
		{name: "accepted", result: map[string]any{"turnId": "native-turn"}, wantOK: true},
		{name: "wrong turn", result: map[string]any{"turnId": "other-turn"}},
		{name: "missing turn", result: map[string]any{}},
		{name: "rejected", err: map[string]any{"code": -32600, "message": "no active turn"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, server, cleanup := NewTestClient()
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			s := &Session{runID: "daemon-run", rpc: client.JSONRPCClient, cancelCtx: ctx}
			s.setThreadID("native-thread")
			s.startSteering(json.RawMessage(`{"threadId":"native-thread","turn":{"id":"native-turn"}}`))
			done := make(chan error, 1)
			go func() {
				done <- s.Steer(ctx, proto.PromptSteerPayload{InputID: "input-1", Text: "追加输入"})
			}()
			var request struct {
				ID     string          `json:"id"`
				Method string          `json:"method"`
				Params TurnSteerParams `json:"params"`
			}
			if err := json.NewDecoder(server.FromClient).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Method != "turn/steer" || request.Params.ThreadID != "native-thread" || request.Params.ExpectedTurnID != "native-turn" {
				t.Fatalf("wrong native destination: %+v", request)
			}
			if len(request.Params.Input) != 1 || request.Params.Input[0].Text != "追加输入" {
				t.Fatalf("input lost: %+v", request.Params.Input)
			}
			select {
			case err := <-done:
				t.Fatalf("returned before native ack: %v", err)
			default:
			}
			if err := json.NewEncoder(server.ToClient).Encode(map[string]any{"id": request.ID, "result": test.result, "error": test.err}); err != nil {
				t.Fatal(err)
			}
			if err := <-done; (err == nil) != test.wantOK {
				t.Fatalf("want success=%v: %v", test.wantOK, err)
			}
		})
	}
}

func TestSteeringDoesNotStartOrReviveTurns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Session{cancelCtx: ctx}
	s.setThreadID("native-thread")
	input := proto.PromptSteerPayload{InputID: "input-1", Text: "extra"}
	// A nil RPC client proves these states do not issue a request.
	for _, notification := range []json.RawMessage{nil, json.RawMessage(`{"threadId":"other-thread","turn":{"id":"other-turn"}}`)} {
		s.startSteering(notification)
		if err := s.Steer(ctx, input); !errors.Is(err, agent.ErrSteeringNotReady) {
			t.Fatalf("starting turn: %v", err)
		}
	}
	s.stopSteering()
	s.startSteering(json.RawMessage(`{"threadId":"native-thread","turn":{"id":"late-turn"}}`))
	if err := s.Steer(ctx, input); !errors.Is(err, agent.ErrSteeringInactive) {
		t.Fatalf("terminal turn revived: %v", err)
	}
	s = &Session{cancelCtx: ctx}
	cancel()
	if err := s.Steer(context.Background(), input); !errors.Is(err, agent.ErrSteeringInactive) {
		t.Fatalf("cancelled run: %v", err)
	}
}
