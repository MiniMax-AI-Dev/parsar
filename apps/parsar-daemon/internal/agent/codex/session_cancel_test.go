package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type interruptRequest struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params struct {
		ThreadID string  `json:"threadId"`
		TurnID   *string `json:"turnId"`
	} `json:"params"`
}

func TestCancelUsesNativeTurnIdentity(t *testing.T) {
	for _, name := range []string{"running", "foreign notification", "startup", "completed", "before thread", "rejected", "response timeout"} {
		t.Run(name, func(t *testing.T) {
			s, client, server := cancellationTestSession(t)
			if name != "before thread" {
				s.setThreadID("native-thread")
			}
			if name != "startup" && name != "before thread" {
				s.onTurnStarted(json.RawMessage(`{"threadId":"native-thread","turn":{"id":"native-turn"}}`))
			}
			if name == "foreign notification" {
				s.onTurnStarted(json.RawMessage(`{"threadId":"foreign-thread","turn":{"id":"foreign-turn"}}`))
			}
			if name == "completed" {
				s.onTurnCompleted(json.RawMessage(`{"threadId":"native-thread","turn":{"id":"native-turn","status":"completed"}}`))
			}
			requests := collectCancellationRequests(t, server, name)
			var calls sync.WaitGroup
			for range 3 {
				calls.Add(1)
				go func() {
					defer calls.Done()
					if err := s.Cancel(context.Background()); err != nil {
						t.Errorf("best-effort cancel: %v", err)
					}
				}()
			}
			finished := make(chan struct{})
			go func() { calls.Wait(); close(finished) }()
			select {
			case <-finished:
			case <-time.After(4 * time.Second):
				t.Fatal("cancellation did not finish after the response deadline")
			}
			if client.Alive() || s.cancelCtx.Err() == nil {
				t.Fatal("cancellation did not close the native client and context")
			}
			got := <-requests
			if name == "completed" || name == "before thread" {
				if len(got) != 0 {
					t.Fatalf("unexpected native interrupt: %+v", got)
				}
			} else {
				want := "native-turn"
				if name == "startup" {
					want = ""
				}
				if len(got) != 1 || got[0].Method != "turn/interrupt" || got[0].Params.ThreadID != "native-thread" || got[0].Params.TurnID == nil || *got[0].Params.TurnID != want {
					t.Fatalf("incorrect or repeated native cancellation: %+v", got)
				}
			}
			if name != "before thread" && s.CancellationOutcome().Metadata[proto.DoneMetaAgentSessionID] != "native-thread" {
				t.Fatal("cancellation lost native continuation identity")
			}
		})
	}
}

func TestCancelRacingTurnStartedKeepsValidNativeTarget(t *testing.T) {
	for range 32 {
		s, _, server := cancellationTestSession(t)
		s.setThreadID("native-thread")
		requests := collectCancellationRequests(t, server, "running")
		start := make(chan struct{})
		observed := make(chan struct{})
		go func() {
			<-start
			s.onTurnStarted(json.RawMessage(`{"threadId":"native-thread","turn":{"id":"native-turn"}}`))
			close(observed)
		}()
		close(start)
		if err := s.Cancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		<-observed
		got := <-requests
		if len(got) != 1 || got[0].Params.ThreadID != "native-thread" || got[0].Params.TurnID == nil {
			t.Fatalf("missing cancellation identity: %+v", got)
		}
		if id := *got[0].Params.TurnID; id != "" && id != "native-turn" {
			t.Fatalf("foreign native Turn ID: %q", id)
		}
		if id, active := s.stopSteering(); active || id != *got[0].Params.TurnID {
			t.Fatalf("late notification changed a stopped target: %q, %v", id, active)
		}
	}
}

func cancellationTestSession(t *testing.T) (*Session, *TestClient, ServerSide) {
	t.Helper()
	client, server, cleanup := NewTestClient()
	t.Cleanup(cleanup)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &Session{rpc: client.JSONRPCClient, cancelCtx: ctx, cancelFn: cancel,
		cfg: defaultSessionConfig(), interactions: newPendingCodexInteractions(), out: make(chan proto.Envelope, 8), bufs: NewItemBuffers()}
	return s, client, server
}

func collectCancellationRequests(t *testing.T, server ServerSide, mode string) <-chan []interruptRequest {
	t.Helper()
	done := make(chan []interruptRequest, 1)
	go func() {
		var requests []interruptRequest
		defer func() { done <- requests }()
		decoder := json.NewDecoder(server.FromClient)
		for {
			var request interruptRequest
			if err := decoder.Decode(&request); err != nil {
				if !errors.Is(err, io.EOF) {
					t.Errorf("read cancellation: %v", err)
				}
				return
			}
			requests = append(requests, request)
			if mode == "response timeout" {
				continue
			}
			reply := map[string]any{"id": request.ID, "result": map[string]any{}}
			if mode == "rejected" {
				delete(reply, "result")
				reply["error"] = map[string]any{"code": -32600, "message": "no active turn to interrupt"}
			}
			if err := json.NewEncoder(server.ToClient).Encode(reply); err != nil {
				t.Errorf("reply to cancellation: %v", err)
				return
			}
		}
	}()
	return done
}
