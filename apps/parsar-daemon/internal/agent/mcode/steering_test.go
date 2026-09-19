package mcode

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestNativeSteeringReceipt(t *testing.T) {
	for _, scenario := range []string{"steering", "steer-rejected", "steer-lost"} {
		t.Run(scenario, func(t *testing.T) {
			s, out := helperSession(t, scenario, false)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			select {
			case event := <-out:
				if event.Type != proto.TypeDelta {
					t.Fatalf("first event %s", event.Type)
				}
			case <-ctx.Done():
				t.Fatal("not ready")
			}
			written := false
			reply := make(chan error, 1)
			go func() {
				reply <- s.SteerWithReceipt(ctx, proto.PromptSteerPayload{InputID: "input-1", Text: "next"}, func() { written = true })
			}()
			// Drain native output concurrently, as the router does.
			drained := make(chan struct{})
			go func() {
				for range out {
				}
				close(drained)
			}()
			err := <-reply
			if !written {
				t.Fatal("complete write was not reported")
			}
			switch scenario {
			case "steering":
				if err != nil {
					t.Fatal(err)
				}
			case "steer-rejected":
				if !errors.Is(err, agent.ErrSteeringRejected) {
					t.Fatalf("got %v", err)
				}
			case "steer-lost":
				if err == nil || errors.Is(err, agent.ErrSteeringRejected) {
					t.Fatalf("unknown outcome became rejection/success: %v", err)
				}
			}
			s.Cancel(ctx)
			select {
			case <-drained:
			case <-ctx.Done():
				t.Fatal("output not closed")
			}
		})
	}
}

func TestTerminalFollowsAllNativeFrames(t *testing.T) {
	_, out := helperSession(t, "many-frames", false)
	count := 0
	for e := range out {
		switch e.Type {
		case proto.TypeDelta:
			count++
		case proto.TypeDone:
			var done proto.DonePayload
			_ = json.Unmarshal(e.Payload, &done)
			if count != 100 || len(done.Content) != 100 {
				t.Fatalf("terminal overtook frames: %d/%d", count, len(done.Content))
			}
		case proto.TypeError:
			t.Fatalf("unexpected error %s", e.Payload)
		}
	}
}

func TestExecutionCancellationWaitsForOutputAndProcess(t *testing.T) {
	s, out := helperSession(t, "strict-cancel", false)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	select {
	case <-out:
	case <-ctx.Done():
		t.Fatal("not ready")
	}
	drained := make(chan struct{})
	go func() {
		for range out {
		}
		close(drained)
	}()
	if err := s.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.exited:
	default:
		t.Fatal("cancel returned before process exit")
	}
	select {
	case <-s.finished:
	default:
		t.Fatal("cancel returned before output settlement")
	}
	if err := s.Cancel(ctx); err != nil {
		t.Fatal("duplicate cancellation", err)
	}
	select {
	case <-drained:
	case <-ctx.Done():
		t.Fatal("output not closed")
	}
	if s.CancellationOutcome().Metadata[proto.DoneMetaAgentSessionID] != "native-1" {
		t.Fatal("lost native binding")
	}
}
