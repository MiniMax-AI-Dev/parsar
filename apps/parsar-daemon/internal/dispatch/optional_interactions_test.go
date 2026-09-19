package dispatch_test

import (
	"context"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// Wrapping only Cancel proves that no responder stubs are required for a Session.
type lifecycleOnly struct{ cancel func(context.Context) error }

func (s lifecycleOnly) Cancel(ctx context.Context) error { return s.cancel(ctx) }

func TestOptionalInteractionResponders(t *testing.T) {
	for _, ask := range []bool{false, true} {
		t.Run(map[bool]string{false: "permission", true: "user choice"}[ask], func(t *testing.T) {
			h := newHarness(t)
			defer h.router.Shutdown(context.Background())
			var output chan<- proto.Envelope
			h.reg.Register("minimal", func(_ context.Context, _ proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
				output = out
				s := &fakeSession{out: out, closeOutOnCancel: true}
				return lifecycleOnly{cancel: s.Cancel}, nil
			})
			if err := h.router.Handle(t.Context(), mustEnv(t, proto.TypePromptRequest, "run", proto.PromptRequestPayload{AgentKind: "minimal"})); err != nil {
				t.Fatal(err)
			}
			event := mustEnv(t, proto.TypePermissionRequest, "run", proto.PermissionRequestPayload{RequestID: "interaction", Tool: "fixture"})
			decision := mustEnv(t, proto.TypePermissionDecision, "interaction", proto.PermissionDecisionPayload{DeliveryID: "decision", Approved: true})
			if ask {
				event = mustEnv(t, proto.TypePromptForUserChoice, "run", proto.PromptForUserChoicePayload{AskID: "interaction"})
				decision = mustEnv(t, proto.TypePromptForUserChoiceDecision, "interaction", proto.PromptForUserChoiceDecisionPayload{DeliveryID: "decision"})
			}
			// An inconsistent adapter emitted an interaction it cannot answer: reject it,
			// never acknowledge application or call a fabricated responder.
			output <- event
			waitFor(t, func() bool { return len(h.sender.snapshot()) > 0 }, "interaction indexed")
			if err := h.router.Handle(t.Context(), decision); err != nil {
				t.Fatal(err)
			}
			assertDecisionAck(t, h.sender, "decision", false, "unsupported")
		})
	}
}
