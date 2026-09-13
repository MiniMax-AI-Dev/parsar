package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestWorkerLeaseLossLeavesUncertainWorkForSuccessor(t *testing.T) {
	h := newDispatchHarness(t)
	_, pool := store.NewTestStore(t)
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: proto.AgentKindCapabilities{Streaming: true, Steering: true, DurableTurns: true, WebSearchControl: true, TextVerbosity: true, ExecutionControls: true, SubagentControl: true, ToolObservations: true, EnvironmentNone: true}}}})
	h.session = publicSession(t, h, "active")
	queued := publicSession(t, h, "queued")
	worker, err := execution.StartWorker(t.Context(), h.d)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
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
	inputs := []store.Input{{Kind: "message", Payload: json.RawMessage(`{"input":[{"role":"user","content":[{"type":"input_text","text":"execute"}]}]}`)}}
	receipts, err := worker.SubmitInputs(t.Context(), h.tenant, h.session.ID, "active", inputs)
	if err != nil {
		t.Fatal(err)
	}
	request := h.read(proto.TypePromptRequest)
	if request.ID != receipts[0].TurnID {
		t.Fatal("unexpected dispatch", request.ID)
	}
	// This worker is the only holder of the specific execution advisory lock.
	// Select it within the dedicated test DB, never terminate pooled backends.
	var pid uint32
	err = pool.QueryRow(t.Context(), `SELECT pid FROM pg_locks WHERE locktype='advisory'
        AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
        AND classid=(706172736172::bigint >> 32)::oid
        AND objid=(706172736172::bigint & 4294967295)::oid AND objsubid=1 AND granted`).Scan(&pid)
	if err != nil {
		t.Fatal(err)
	}
	var killed bool
	if err = pool.QueryRow(t.Context(), "SELECT pg_terminate_backend($1, 1000)", pid).Scan(&killed); err != nil || !killed {
		t.Fatal(killed, err)
	}
	// Admission is independent of the lost execution writer. No new model request
	// is required to preserve this queued input for the successor.
	pending, err := worker.SubmitInputs(t.Context(), h.tenant, queued.ID, "queued", inputs)
	if err != nil {
		t.Fatal("pooled admission failed after lease loss", err)
	}
	select {
	case err = <-done:
		done <- err // Retain the completion for cleanup.
		if err == nil {
			t.Fatal("worker ignored lease loss")
		}
	case <-time.After(12 * time.Second):
		t.Fatal("worker ignored lease loss")
	}
	active, err := h.s.GetTurn(t.Context(), h.tenant, h.session.ID, request.ID)
	if err != nil || active.Status != store.TurnInProgress {
		t.Fatal("lost owner persisted fallback completion", active, err)
	}
	successor, err := execution.StartWorker(t.Context(), h.d)
	if err != nil {
		t.Fatal(err)
	}
	stopped, stop := context.WithCancel(t.Context())
	stop()
	if err = successor.Run(stopped); err != context.Canceled {
		t.Fatal(err)
	}
	active, err = h.s.GetTurn(t.Context(), h.tenant, h.session.ID, request.ID)
	if err != nil || active.Status != store.TurnFailed {
		t.Fatal("successor did not reconcile", active, err)
	}
	next, err := h.s.GetTurn(t.Context(), h.tenant, queued.ID, pending[0].TurnID)
	if err != nil || next.Status != store.TurnQueued {
		t.Fatal("successor lost queued work", next, err)
	}
}
