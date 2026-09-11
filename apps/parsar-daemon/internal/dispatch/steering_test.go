package dispatch_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type steeringSession struct {
	*fakeSession
	steer func(context.Context, proto.PromptSteerPayload) error
}

func (s *steeringSession) Steer(ctx context.Context, input proto.PromptSteerPayload) error {
	return s.steer(ctx, input)
}

func TestSteeringReceiptsAndRetries(t *testing.T) {
	for _, engineError := range []error{nil, errors.New("native connection lost after write")} {
		name := "accepted"
		if engineError != nil {
			name = "uncertain"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			calls, starts := 0, 0
			h.reg.Register("codex", func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
				starts++
				return &steeringSession{
					fakeSession: &fakeSession{out: out, closeOutOnCancel: true},
					steer: func(ctx context.Context, input proto.PromptSteerPayload) error {
						calls++
						if _, ok := ctx.Deadline(); !ok {
							t.Error("native request has no deadline")
						}
						if input.InputID != "input-1" || input.Text != "additional text" {
							t.Errorf("input lost: %+v", input)
						}
						if len(h.sender.snapshot()) != 0 {
							t.Error("ack sent before engine accepted input")
						}
						return engineError
					},
				}, nil
			})
			ctx := context.Background()
			if err := h.router.Handle(ctx, mustEnv(t, proto.TypePromptRequest, "run-1", proto.PromptRequestPayload{AgentKind: "codex"})); err != nil {
				t.Fatal(err)
			}
			input := proto.PromptSteerPayload{InputID: "input-1", Text: "additional text"}
			env := mustEnv(t, proto.TypePromptSteer, "run-1", input)
			// An ack transport failure must not cause another native invocation.
			h.sender.failNow = true
			if err := h.router.Handle(ctx, env); err == nil {
				t.Fatal("expected ack-send failure")
			}
			for range 2 {
				if err := h.router.Handle(ctx, env); err != nil {
					t.Fatal(err)
				}
				ack := lastSteeringAck(t, h.sender, "run-1", "input-1")
				if ack.Accepted != (engineError == nil) {
					t.Fatalf("receipt: %+v", ack)
				}
				if engineError != nil && ack.ErrorCode != "outcome_unknown" {
					t.Fatalf("uncertainty lost: %+v", ack)
				}
			}
			input.Text = "changed text"
			if err := h.router.Handle(ctx, mustEnv(t, proto.TypePromptSteer, "run-1", input)); err != nil {
				t.Fatal(err)
			}
			if ack := lastSteeringAck(t, h.sender, "run-1", "input-1"); ack.ErrorCode != "input_conflict" {
				t.Fatalf("conflict: %+v", ack)
			}
			if calls != 1 || starts != 1 {
				t.Fatalf("native calls=%d, runs started=%d", calls, starts)
			}
		})
	}
}

func TestSteeringReadinessAndUnsupportedRuns(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	calls := 0
	h.reg.Register("codex", func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		return &steeringSession{
			fakeSession: &fakeSession{out: out, closeOutOnCancel: true},
			steer: func(context.Context, proto.PromptSteerPayload) error {
				calls++
				if calls == 1 {
					return agent.ErrSteeringNotReady
				}
				return nil
			},
		}, nil
	})
	ctx := context.Background()
	if err := h.router.Handle(ctx, mustEnv(t, proto.TypePromptRequest, "run-1", proto.PromptRequestPayload{AgentKind: "codex"})); err != nil {
		t.Fatal(err)
	}
	input := proto.PromptSteerPayload{InputID: "input-1", Text: "extra"}
	env := mustEnv(t, proto.TypePromptSteer, "run-1", input)
	for _, expected := range []string{"not_ready", ""} {
		if err := h.router.Handle(ctx, env); err != nil {
			t.Fatal(err)
		}
		if ack := lastSteeringAck(t, h.sender, "run-1", "input-1"); ack.ErrorCode != expected {
			t.Fatalf("expected %q: %+v", expected, ack)
		}
	}
	if err := h.router.Handle(ctx, mustEnv(t, proto.TypePromptCancel, "run-1", nil)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return h.router.ActiveRuns() == 0 }, "cancel cleanup")
	if err := h.router.Handle(ctx, env); err != nil {
		t.Fatal(err)
	}
	if ack := lastSteeringAck(t, h.sender, "run-1", "input-1"); ack.ErrorCode != "run_inactive" {
		t.Fatalf("inactive: %+v", ack)
	}
	if err := h.router.Handle(ctx, mustEnv(t, proto.TypePromptRequest, "run-2", proto.PromptRequestPayload{AgentKind: "claude_code"})); err != nil {
		t.Fatal(err)
	}
	session := <-h.gotSess
	defer close(session.out)
	if err := h.router.Handle(ctx, mustEnv(t, proto.TypePromptSteer, "run-2", input)); err != nil {
		t.Fatal(err)
	}
	if ack := lastSteeringAck(t, h.sender, "run-2", "input-1"); ack.ErrorCode != "unsupported" {
		t.Fatalf("unsupported: %+v", ack)
	}
	input.Text = " \n "
	if err := h.router.Handle(ctx, mustEnv(t, proto.TypePromptSteer, "run-2", input)); err != nil {
		t.Fatal(err)
	}
	if ack := lastSteeringAck(t, h.sender, "run-2", "input-1"); ack.ErrorCode != "invalid_input" {
		t.Fatalf("invalid: %+v", ack)
	}
}

func lastSteeringAck(t *testing.T, sender *recSender, runID, inputID string) proto.PromptSteerAckPayload {
	t.Helper()
	frames := sender.snapshot()
	if len(frames) == 0 {
		t.Fatal("missing ack")
	}
	env := frames[len(frames)-1]
	var ack proto.PromptSteerAckPayload
	if env.Type != proto.TypePromptSteerAck || env.ID != runID {
		t.Fatalf("incorrect routing: %+v", env)
	}
	if err := env.DecodePayload(&ack); err != nil {
		t.Fatal(err)
	}
	if ack.InputID != inputID {
		t.Fatalf("incorrect input: %+v", ack)
	}
	return ack
}

func TestSteeringCapacityPreservesExistingReceipts(t *testing.T) {
	h := newHarness(t)
	defer h.router.Shutdown(context.Background())
	calls := 0
	h.reg.Register("codex", func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		return &steeringSession{
			fakeSession: &fakeSession{out: out, closeOutOnCancel: true},
			steer: func(context.Context, proto.PromptSteerPayload) error {
				calls++
				return nil
			},
		}, nil
	})
	ctx := context.Background()
	if err := h.router.Handle(ctx, mustEnv(t, proto.TypePromptRequest, "run-1", proto.PromptRequestPayload{AgentKind: "codex"})); err != nil {
		t.Fatal(err)
	}
	for i := range 257 {
		input := proto.PromptSteerPayload{InputID: fmt.Sprintf("input-%d", i), Text: "extra"}
		if err := h.router.Handle(ctx, mustEnv(t, proto.TypePromptSteer, "run-1", input)); err != nil {
			t.Fatal(err)
		}
		ack := lastSteeringAck(t, h.sender, "run-1", input.InputID)
		if i < 256 && !ack.Accepted || i == 256 && ack.ErrorCode != "input_limit" {
			t.Fatalf("input %d: %+v", i, ack)
		}
	}
	if err := h.router.Handle(ctx, mustEnv(t, proto.TypePromptSteer, "run-1", proto.PromptSteerPayload{InputID: "input-0", Text: "extra"})); err != nil {
		t.Fatal(err)
	}
	if ack := lastSteeringAck(t, h.sender, "run-1", "input-0"); !ack.Accepted || calls != 256 {
		t.Fatalf("receipt evicted or input redelivered: %+v, calls=%d", ack, calls)
	}
}
