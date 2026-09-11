package dispatch_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type functionSession struct {
	*fakeSession
	mu    sync.Mutex
	calls int
}

func (s *functionSession) SubmitFunctionResult(_ context.Context, p proto.FunctionResultPayload) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.CallID != "call" || s.calls != 0 {
		return agent.ErrUnknownFunctionCall
	}
	s.calls++
	return nil
}
func TestFunctionReceiptsScopeRetriesAndConflicts(t *testing.T) {
	reg := agent.NewRegistry()
	sender := &recSender{}
	sessions := map[string]*functionSession{}
	reg.RegisterKind(proto.SupportedAgentKind{Kind: "function-test", Available: true, Capabilities: proto.AgentKindCapabilities{FunctionTools: true}}, func(ctx context.Context, p proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		s := &functionSession{fakeSession: &fakeSession{out: out, ctx: ctx, closeOutOnCancel: true}}
		sessions[p.RunID] = s
		return s, nil
	})
	router, err := dispatch.New(dispatch.Config{Registry: reg, Sender: sender})
	if err != nil {
		t.Fatal(err)
	}
	defer router.Shutdown(context.Background())
	for _, id := range []string{"one", "two"} {
		env, _ := proto.NewEnvelope(proto.TypePromptRequest, id, proto.PromptRequestPayload{AgentKind: "function-test", Prompt: "lookup", FunctionTools: []proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{}`)}}})
		if err := router.Handle(t.Context(), env); err != nil {
			t.Fatal(err)
		}
	}
	submit := func(run, call, text, delivery string) proto.InteractionDecisionAckPayload {
		t.Helper()
		env, _ := proto.NewEnvelope(proto.TypeFunctionResult, run, proto.FunctionResultPayload{CallID: call, Success: true, Text: text, DeliveryID: delivery})
		if err := router.Handle(t.Context(), env); err != nil {
			t.Fatal(err)
		}
		frames := sender.snapshot()
		last := frames[len(frames)-1]
		var ack proto.InteractionDecisionAckPayload
		if err := last.DecodePayload(&ack); err != nil || last.Type != proto.TypeInteractionDecisionAck || last.ID != run || ack.DeliveryID != delivery {
			t.Fatal(last, err)
		}
		return ack
	}
	if a := submit("missing", "call", "answer", "a"); a.Applied || a.ErrorCode != "not_pending" {
		t.Fatal(a)
	}
	if a := submit("one", "missing", "answer", "b"); a.Applied || a.ErrorCode != "not_pending" {
		t.Fatal(a)
	}
	// A lost receipt may be retried without writing the native result twice.
	sender.mu.Lock()
	sender.failNow = true
	sender.mu.Unlock()
	first, _ := proto.NewEnvelope(proto.TypeFunctionResult, "one", proto.FunctionResultPayload{CallID: "call", Success: true, Text: "answer", DeliveryID: "lost"})
	if err := router.Handle(t.Context(), first); err == nil {
		t.Fatal("receipt send failure was hidden")
	}
	if a := submit("one", "call", "answer", "retry"); !a.Applied {
		t.Fatal(a)
	}
	if a := submit("one", "call", "changed", "conflict"); a.Applied || a.ErrorCode != "decision_conflict" {
		t.Fatal(a)
	}
	if a := submit("two", "call", "second answer", "other-run"); !a.Applied {
		t.Fatal(a)
	}
	for _, s := range sessions {
		s.mu.Lock()
		count := s.calls
		s.mu.Unlock()
		if count != 1 {
			t.Fatal(count)
		}
	}
}

func TestFunctionToolsRequireAdvertisedSupport(t *testing.T) {
	reg := agent.NewRegistry()
	called := false
	reg.Register("unsupported", func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
		called = true
		return nil, nil
	})
	sender := &recSender{}
	router, _ := dispatch.New(dispatch.Config{Registry: reg, Sender: sender})
	defer router.Shutdown(context.Background())
	env, _ := proto.NewEnvelope(proto.TypePromptRequest, "run", proto.PromptRequestPayload{AgentKind: "unsupported", FunctionTools: []proto.FunctionTool{{Name: "lookup", Parameters: json.RawMessage(`{}`)}}})
	if err := router.Handle(t.Context(), env); err == nil || called {
		t.Fatal("unsupported engine silently ignored tools", err)
	}
}
