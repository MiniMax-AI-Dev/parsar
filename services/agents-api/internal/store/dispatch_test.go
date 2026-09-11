package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtime"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type dispatchHarness struct {
	t          *testing.T
	s          *store.Store
	d          *execution.Dispatcher
	tenant     string
	session    store.Session
	device     store.ExecutionDevice
	conn       *websocket.Conn
	registry   *gateway.Registry
	url        string
	credential string
}

func newDispatchHarness(t *testing.T) *dispatchHarness {
	t.Helper()
	s, _ := store.NewTestStore(t)
	h := &dispatchHarness{t: t, s: s, tenant: uuid.NewString()}
	ctx := context.Background()
	var err error
	h.session, err = s.CreateSession(ctx, h.tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "session", Configuration: []byte(`{"agent":{"model":"test-model","instructions":"Keep this instruction."},"daemon":{"work_dir":"/tmp"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	secret := uuid.NewString()
	h.credential = secret
	h.device, err = s.CreateDevice(ctx, h.tenant, "isolated executor", device.HashCredential(secret))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BindSessionDevice(ctx, h.tenant, h.session.ID, h.device.ID); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	wsURL := "ws://" + server.Listener.Addr().String() + "/api/v1/agent-daemon/ws"
	server.Config.Handler, h.registry, err = runtime.NewGateway(s, wsURL)
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	h.url = server.URL
	t.Cleanup(func() { server.Close(); runtime.CloseConnections(h.registry) })
	u, _ := url.Parse(wsURL)
	u.RawQuery = url.Values{"device_id": {h.device.ID}, "token": {secret}, "version": {proto.Version}}.Encode()
	h.conn, _, err = websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatal("device connection failed")
	}
	t.Cleanup(func() { h.conn.Close() })
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: proto.AgentKindCapabilities{Streaming: true, Steering: true, Resume: true}}}})
	deadline := time.Now().Add(3 * time.Second)
	for {
		peer, e := h.registry.LookupDevice(h.device.ID)
		if e == nil {
			if _, _, known := peer.AgentKindStatus("codex"); known {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("heartbeat not registered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.d = &execution.Dispatcher{Store: s, Registry: h.registry}
	return h
}

func (h *dispatchHarness) message(key, text string) store.InputReceipt {
	h.t.Helper()
	body, _ := json.Marshal(map[string]string{"text": text})
	r, err := h.s.SubmitMessage(context.Background(), h.tenant, h.session.ID, key, body)
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

func (h *dispatchHarness) write(run, kind string, payload any) {
	h.t.Helper()
	env, err := proto.NewEnvelope(kind, run, payload)
	if err != nil {
		h.t.Fatal(err)
	}
	if err = h.conn.WriteJSON(env); err != nil {
		h.t.Fatal(err)
	}
}

func (h *dispatchHarness) read(kind string) proto.Envelope {
	h.t.Helper()
	_ = h.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var env proto.Envelope
		if err := h.conn.ReadJSON(&env); err != nil {
			h.t.Fatal(err)
		}
		if env.Type == kind {
			return env
		}
	}
}

type runResult struct {
	turn store.Turn
	err  error
}

func (h *dispatchHarness) run(ctx context.Context, turn string) <-chan runResult {
	out := make(chan runResult, 1)
	go func() { result, err := h.d.Run(ctx, h.tenant, h.session.ID, turn); out <- runResult{result, err} }()
	return out
}

func (h *dispatchHarness) finished(result <-chan runResult, status string) store.Turn {
	h.t.Helper()
	select {
	case got := <-result:
		if got.err != nil || got.turn.Status != status {
			h.t.Fatalf("execution result: status=%s err=%v outcome=%s", got.turn.Status, got.err, got.turn.Outcome)
		}
		return got.turn
	case <-time.After(10 * time.Second):
		h.t.Fatal("execution did not finish")
	}
	return store.Turn{}
}

func TestExecutionDispatchSteeringAndNativeContinuity(t *testing.T) {
	h := newDispatchHarness(t)
	ctx := context.Background()
	first := h.message("first", "Initial input")
	result := h.run(ctx, first.TurnID)
	request := h.read(proto.TypePromptRequest)
	var prompt proto.PromptRequestPayload
	_ = request.DecodePayload(&prompt)
	if prompt.Prompt != "Initial input" || prompt.ConversationID != h.session.ID || prompt.AgentOptions["model"] != "test-model" || prompt.AgentOptions["system_prompt"] != "Keep this instruction." {
		t.Fatalf("wrong resolved request: %+v", prompt)
	}
	if _, err := h.d.Run(ctx, uuid.NewString(), h.session.ID, first.TurnID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign execution: %v", err)
	}
	if _, err := h.d.Run(ctx, h.tenant, h.session.ID, first.TurnID); !errors.Is(err, store.ErrTurnConflict) {
		t.Fatalf("duplicate execution: %v", err)
	}
	second := h.message("second", "Follow-up input")
	steer := h.read(proto.TypePromptSteer)
	var input proto.PromptSteerPayload
	_ = steer.DecodePayload(&input)
	if input.Text != "Follow-up input" {
		t.Fatal(input.Text)
	}
	h.write(first.TurnID, proto.TypePromptSteerAck, proto.PromptSteerAckPayload{InputID: input.InputID, ErrorCode: "not_ready"})
	retry := h.read(proto.TypePromptSteer)
	if string(retry.Payload) != string(steer.Payload) {
		t.Fatal("steering retry changed identity")
	}
	h.write(first.TurnID, proto.TypePromptSteerAck, proto.PromptSteerAckPayload{InputID: input.InputID, Accepted: true})
	h.write(first.TurnID, proto.TypeDone, proto.DonePayload{Content: "Finished", Usage: proto.Usage{InputTokens: 7, OutputTokens: 3}, Metadata: map[string]any{proto.DoneMetaAgentSessionID: "native-thread-1"}})
	done := h.finished(result, store.TurnCompleted)
	var outcome execution.Result
	_ = json.Unmarshal(done.Outcome, &outcome)
	if outcome.AppliedThrough != second.Sequence || outcome.Done.Usage.InputTokens != 7 {
		t.Fatalf("missing result: %+v", outcome)
	}
	newStore, pool := store.NewTestStore(t)
	defer pool.Close()
	h.s = newStore
	h.d.Store = newStore
	bound, err := newStore.GetSessionDevice(ctx, h.tenant, h.session.ID)
	if err != nil || bound.NativeSessionID != "native-thread-1" {
		t.Fatalf("native binding lost: %+v %v", bound, err)
	}
	next := h.message("third", "Continue the session")
	result = h.run(ctx, next.TurnID)
	request = h.read(proto.TypePromptRequest)
	_ = request.DecodePayload(&prompt)
	if prompt.AgentSessionID != "native-thread-1" || prompt.AgentStateKey != "agents-api-"+h.session.ID {
		t.Fatal("native continuity lost")
	}
	h.write(next.TurnID, proto.TypeDone, proto.DonePayload{Content: "Continued"})
	h.finished(result, store.TurnCompleted)
}

func TestExecutionCancellationRequiresReceiptAndSurvivesContextEnd(t *testing.T) {
	for _, withDone := range []bool{false, true} {
		t.Run(map[bool]string{false: "receipt", true: "done-before-receipt"}[withDone], func(t *testing.T) {
			h := newDispatchHarness(t)
			first := h.message("first", "Run")
			result := h.run(context.Background(), first.TurnID)
			h.read(proto.TypePromptRequest)
			_, err := h.s.RequestCancel(context.Background(), h.tenant, h.session.ID, "cancel")
			if err != nil {
				t.Fatal(err)
			}
			env := h.read(proto.TypePromptCancel)
			var cancel proto.PromptCancelPayload
			_ = env.DecodePayload(&cancel)
			if cancel.DeliveryID == "" {
				t.Fatal("cancellation has no receipt identity")
			}
			current, err := h.s.GetTurn(context.Background(), h.tenant, h.session.ID, first.TurnID)
			if err != nil || current.Status != store.TurnInProgress {
				t.Fatal("cancel finished before receipt")
			}
			if withDone {
				h.write(first.TurnID, proto.TypeDone, proto.DonePayload{})
			}
			outcome := &proto.DonePayload{Metadata: map[string]any{proto.DoneMetaAgentSessionID: "cancelled-native"}, Usage: proto.Usage{InputTokens: 10}}
			h.write(first.TurnID, proto.TypeInteractionDecisionAck, proto.InteractionDecisionAckPayload{DeliveryID: cancel.DeliveryID, Applied: true, Outcome: outcome})
			h.finished(result, store.TurnCancelled)
			next := h.message("next", "Continue after cancellation")
			result = h.run(context.Background(), next.TurnID)
			env = h.read(proto.TypePromptRequest)
			var prompt proto.PromptRequestPayload
			_ = env.DecodePayload(&prompt)
			if prompt.AgentSessionID != "cancelled-native" {
				t.Fatal("cancellation lost native continuity")
			}
			h.write(next.TurnID, proto.TypeDone, proto.DonePayload{})
			h.finished(result, store.TurnCompleted)
		})
	}
	h := newDispatchHarness(t)
	first := h.message("first", "Run")
	ctx, cancel := context.WithCancel(context.Background())
	result := h.run(ctx, first.TurnID)
	h.read(proto.TypePromptRequest)
	cancel()
	done := h.finished(result, store.TurnFailed)
	var outcome execution.Result
	_ = json.Unmarshal(done.Outcome, &outcome)
	if outcome.ErrorCode != "execution_interrupted" {
		t.Fatal(outcome.ErrorCode)
	}
}

func TestExecutionFailureDoesNotBecomeSuccessOrReplay(t *testing.T) {
	for _, kind := range []string{"disconnect", "engine", "late-input", "unconfirmed-input"} {
		t.Run(kind, func(t *testing.T) {
			h := newDispatchHarness(t)
			first := h.message("first", "Run")
			result := h.run(context.Background(), first.TurnID)
			h.read(proto.TypePromptRequest)
			switch kind {
			case "disconnect":
				h.conn.Close()
			case "engine":
				h.write(first.TurnID, proto.TypeUsage, proto.UsagePayload{Usage: proto.Usage{InputTokens: 13, OutputTokens: 7}})
				h.write(first.TurnID, proto.TypeError, proto.ErrorPayload{Error: "Engine failed"})
				h.write(first.TurnID, proto.TypeDone, proto.DonePayload{})
			case "late-input":
				h.message("late", "Still unprocessed")
				h.write(first.TurnID, proto.TypeDone, proto.DonePayload{Content: "Only first input finished"})
			case "unconfirmed-input":
				h.message("second", "Steer")
				h.read(proto.TypePromptSteer)
				h.write(first.TurnID, proto.TypeDone, proto.DonePayload{})
			}
			done := h.finished(result, store.TurnFailed)
			if kind == "engine" {
				var outcome execution.Result
				_ = json.Unmarshal(done.Outcome, &outcome)
				if outcome.Done.Usage.InputTokens != 13 || outcome.Done.Usage.OutputTokens != 7 {
					t.Fatal("failed execution lost reported usage")
				}
			}
			if kind != "disconnect" {
				if _, err := h.d.Run(context.Background(), h.tenant, h.session.ID, first.TurnID); !errors.Is(err, store.ErrTurnConflict) {
					t.Fatalf("terminal replay: %v", err)
				}
			}
		})
	}
}

func TestExecutionOutcomeAndNativeBindingCommitTogether(t *testing.T) {
	h := newDispatchHarness(t)
	first := h.message("first", "Run")
	ctx := context.Background()
	_, err := h.s.TransitionTurn(ctx, h.tenant, h.session.ID, first.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress})
	if err != nil {
		t.Fatal(err)
	}
	late := h.message("second", "Late")
	if _, err := h.s.CompleteExecution(ctx, h.tenant, h.session.ID, first.TurnID, store.TurnCompleted, []byte(`{}`), "native-one", first.Sequence); !errors.Is(err, store.ErrUnappliedInputs) {
		t.Fatalf("unapplied completion: %v", err)
	}
	bound, _ := h.s.GetSessionDevice(ctx, h.tenant, h.session.ID)
	if bound.NativeSessionID != "" {
		t.Fatal("native ID committed without outcome")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, native := range []string{"native-one", "native-two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.s.CompleteExecution(ctx, h.tenant, h.session.ID, first.TurnID, store.TurnCompleted, []byte(`{}`), native, late.Sequence)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, store.ErrTurnConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("multiple terminal owners")
	}
}
