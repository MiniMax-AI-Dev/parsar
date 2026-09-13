package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestDurableSteeringBypassesOnlyNativeResponseDeadline(t *testing.T) {
	for _, durable := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "durable"}[durable], func(t *testing.T) {
			client, server, cleanup := NewTestClient()
			defer cleanup()
			client.cfg.RequestTimeout = 20 * time.Millisecond
			s := &Session{rpc: client.JSONRPCClient, cancelCtx: context.Background()}
			s.setThreadID("thread")
			s.startSteering(json.RawMessage(`{"threadId":"thread","turn":{"id":"turn"}}`))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			written := make(chan struct{})
			reply := make(chan error, 1)
			go func() {
				input := proto.PromptSteerPayload{InputID: "extra", Text: "text"}
				if durable {
					reply <- s.SteerWithReceipt(ctx, input, func() { close(written) })
				} else {
					reply <- s.Steer(ctx, input)
				}
			}()
			if durable {
				select {
				case <-written:
					t.Fatal("written before frame was read")
				default:
				}
			}
			var request JsonRpcRequest
			if err := json.NewDecoder(server.FromClient).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if durable {
				select {
				case <-written:
				case <-ctx.Done():
					t.Fatal("missing write phase")
				}
			}
			select {
			case err := <-reply:
				if durable || err == nil {
					t.Fatal("unexpected response deadline", err)
				}
			case <-time.After(60 * time.Millisecond):
				if !durable {
					t.Fatal("legacy timeout disappeared")
				}
			}
			if durable {
				if err := json.NewEncoder(server.ToClient).Encode(map[string]any{"id": request.ID, "result": map[string]string{"turnId": "turn"}}); err != nil {
					t.Fatal(err)
				}
				if err := <-reply; err != nil {
					t.Fatal(err)
				}
			}
			if !client.Alive() {
				t.Fatal("receipt wait killed process")
			}
		})
	}
}

func TestDurableSteeringKeepsConfirmedReceiptAtCompletion(t *testing.T) {
	for _, confirmed := range []bool{false, true} {
		failures := 0
		for range 40 {
			client, server, cleanup := NewTestClient()
			s := &Session{rpc: client.JSONRPCClient, cancelCtx: context.Background()}
			s.setThreadID("thread")
			s.startSteering(json.RawMessage(`{"threadId":"thread","turn":{"id":"turn"}}`))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			inWritten, releaseWritten := make(chan struct{}), make(chan struct{})
			reply := make(chan error, 1)
			go func() {
				reply <- s.SteerWithReceipt(ctx, proto.PromptSteerPayload{InputID: "extra", Text: "text"}, func() { close(inWritten); <-releaseWritten })
			}()
			var request JsonRpcRequest
			if err := json.NewDecoder(server.FromClient).Decode(&request); err != nil {
				t.Fatal(err)
			}
			<-inWritten
			if confirmed {
				id, _ := json.Marshal(request.ID)
				client.handleResponse(id, json.RawMessage(`{"turnId":"turn"}`), nil)
			}
			s.stopSteering()
			close(releaseWritten)
			err := <-reply
			cancel()
			cleanup()
			if (err == nil) != confirmed {
				failures++
			}
		}
		if failures != 0 {
			t.Fatalf("confirmed=%v: incorrect native evidence in %d/40 completions", confirmed, failures)
		}
	}
}
