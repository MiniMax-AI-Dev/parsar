package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func publicSession(t *testing.T, h *dispatchHarness, key string) store.Session {
	t.Helper()
	value, err := h.s.CreateSession(context.Background(), h.tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: key, Configuration: json.RawMessage(`{"agent":{"id":"agent_test","model":"test-model","instructions":"Keep this."},"environment":{"type":"none"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestExecutionWorkerAdmissionBindingAndRecovery(t *testing.T) {
	h := newDispatchHarness(t)
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: proto.AgentKindCapabilities{Streaming: true, Steering: true, DurableTurns: true, WebSearchControl: true, TextVerbosity: true, SubagentControl: true, EnvironmentNone: true}}}})
	h.session = publicSession(t, h, "public")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker, err := execution.StartWorker(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("worker did not stop")
		}
	})
	if second, err := execution.StartWorker(ctx, h.d); err == nil {
		cancel()
		go second.Run(ctx)
		t.Fatal("second service acquired database")
	}
	inputs := []store.Input{{Kind: "message", Payload: json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"First"}]}]}`)}, {Kind: "message", Payload: json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"Second"}]}]}`)}}
	receipts, err := worker.SubmitInputs(ctx, h.tenant, h.session.ID, "batch", inputs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.SubmitInputs(ctx, uuid.NewString(), h.session.ID, "foreign", inputs); err == nil {
		t.Fatal("foreign tenant admitted")
	}
	retry, err := worker.SubmitInputs(ctx, h.tenant, h.session.ID, "batch", inputs)
	if err != nil || !retry[0].Replayed || retry[0].TurnID != receipts[0].TurnID {
		t.Fatal(retry, err)
	}
	request := h.read(proto.TypePromptRequest)
	var prompt proto.PromptRequestPayload
	if err := request.DecodePayload(&prompt); err != nil {
		t.Fatal(err)
	}
	if prompt.Prompt != "First\n\nSecond" || !prompt.DisableExecutionEnvironment || !prompt.DisableSubagents || prompt.AgentOptions["web_search"] != "disabled" || prompt.AgentOptions["model_verbosity"] != "medium" {
		t.Fatal(prompt)
	}
	bound, err := h.s.GetSessionDevice(ctx, h.tenant, h.session.ID)
	if err != nil || bound.ID != h.device.ID {
		t.Fatal(bound, err)
	}
	active, err := h.s.GetSession(ctx, h.tenant, h.session.ID)
	if err != nil || active.LastTurn == nil || active.LastTurn.Status != store.TurnInProgress {
		t.Fatal(active, err)
	}
	h.write(request.ID, proto.TypeDone, proto.DonePayload{Content: "Answer", Metadata: map[string]any{proto.DoneMetaAgentSessionID: "worker-native"}})
	waitTurn(t, h, receipts[0].TurnID, store.TurnCompleted)
	items, err := h.s.ListItems(ctx, h.tenant, h.session.ID, "", 100, true)
	if err != nil || len(items.Items) != 3 {
		t.Fatal(items, err)
	}
	next, err := worker.SubmitInputs(ctx, h.tenant, h.session.ID, "next", inputs[:1])
	if err != nil {
		t.Fatal(err)
	}
	request = h.read(proto.TypePromptRequest)
	_ = request.DecodePayload(&prompt)
	if prompt.AgentSessionID != "worker-native" {
		t.Fatal(prompt)
	}
	cancel()
	waitTurn(t, h, next[0].TurnID, store.TurnFailed)
}

func waitTurn(t *testing.T, h *dispatchHarness, id, status string) {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		turn, err := h.s.GetTurn(context.Background(), h.tenant, h.session.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		if turn.Status == status {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("turn %s did not become %s", id, status)
}

func TestWorkerRestartReconcilesClaimedButPreservesQueuedWork(t *testing.T) {
	h := newDispatchHarness(t)
	h.session = publicSession(t, h, "interrupted")
	first := h.message("first", "Already sent")
	ctx := context.Background()
	if _, err := h.s.TransitionTurn(ctx, h.tenant, h.session.ID, first.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress}); err != nil {
		t.Fatal(err)
	}
	queued := publicSession(t, h, "queued")
	if _, err := h.s.SubmitMessage(ctx, h.tenant, queued.ID, "first", json.RawMessage(`{"text":"Not sent"}`)); err != nil {
		t.Fatal(err)
	}
	worker, err := execution.StartWorker(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	stopped, cancel := context.WithCancel(ctx)
	cancel()
	if err := worker.Run(stopped); err != context.Canceled {
		t.Fatal(err)
	}
	interrupted, err := h.s.GetSession(ctx, h.tenant, h.session.ID)
	if err != nil || interrupted.LastTurn.Status != store.TurnFailed {
		t.Fatal(interrupted, err)
	}
	pending, err := h.s.GetSession(ctx, h.tenant, queued.ID)
	if err != nil || pending.LastTurn.Status != store.TurnQueued {
		t.Fatal(pending, err)
	}
	if _, err := h.s.RequestCancel(ctx, h.tenant, queued.ID, "stop-before-dispatch"); err != nil {
		t.Fatal(err)
	}
	pending, err = h.s.GetSession(ctx, h.tenant, queued.ID)
	if err != nil || pending.LastTurn.Status != store.TurnCancelled {
		t.Fatal(pending, err)
	}
	restarted, err := execution.StartWorker(ctx, h.d)
	if err != nil {
		t.Fatal("lease not released", err)
	}
	if err := restarted.Run(stopped); err != context.Canceled {
		t.Fatal(err)
	}
}
