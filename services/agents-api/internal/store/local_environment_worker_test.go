package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func localWorker(t *testing.T, scoped, execute bool) (*dispatchHarness, *execution.Worker, store.Environment) {
	t.Helper()
	h := newDispatchHarnessForSession(t, []byte(`{"agent":{"model":"test-model"},"environment":{"type":"openai_hosted","network":{"access":"disabled"}}}`), scoped)
	environment, err := h.s.GetSessionEnvironment(t.Context(), h.tenant, h.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	caps := proto.AgentKindCapabilities{LocalEnvironment: true, Preparation: true, WorkspaceReadPreparation: true}
	if execute {
		caps.WorkspaceOutputExport = true
		caps.Streaming, caps.Steering, caps.DurableTurns, caps.DurableInputReceipts = true, true, true, true
		caps.WebSearchControl, caps.TextVerbosity, caps.ExecutionControls = true, true, true
		caps.SubagentControl, caps.ToolObservations = true, true
	} else {
		h.d.Options = func(context.Context, store.Session) (map[string]any, error) {
			t.Error("directory read requested model credentials")
			return nil, errors.New("model unavailable")
		}
	}
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: caps}}})
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "local capability", func() bool {
		peer, err := h.registry.LookupDevice(h.device.ID)
		if err != nil {
			return false
		}
		info, _, _ := peer.AgentKindStatus("codex")
		return info.Capabilities.LocalEnvironment
	})
	w, err := execution.StartWorker(t.Context(), h.d)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("local worker did not stop")
		}
	})
	return h, w, environment
}

func TestLocalEnvironmentWorkerDirectoryUsesExactAuthorityWithoutModel(t *testing.T) {
	h, w, environment := localWorker(t, true, false)
	foreign := environment
	foreign.TenantID = uuid.NewString()
	if _, err := w.ReadEnvironmentDirectory(t.Context(), foreign, "reports"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign read admitted", err)
	}
	result := startDirectoryRead(t.Context(), w, environment)
	frame := h.read(proto.TypeExecutionPrepare)
	var prepare proto.ExecutionPreparePayload
	if frame.DecodePayload(&prepare) != nil || !proto.ValidWorkspaceReadPreparation(prepare.Configuration) || prepare.Configuration.LocalEnvironment == nil || prepare.Configuration.LocalEnvironment.ID != environment.ID || prepare.Configuration.RemoteEnvironment != nil {
		t.Fatal("local read did not preserve its exact identity")
	}
	handle := acknowledgePreparation(h, frame.ID)
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	read := h.read(proto.TypeWorkspaceRead)
	var input proto.WorkspaceReadPayload
	if read.DecodePayload(&input) != nil || input.EnvironmentID != environment.ID || input.Handle != handle || input.RunID != "" {
		t.Fatal("local directory owner changed")
	}
	completeDirectoryRead(t, h, frame.ID, read.ID, false, false)
	if got := awaitDirectoryResult(t, result); got.err != nil || len(got.value.Entries) != 1 {
		t.Fatal("local directory failed", got.err)
	}
	session, err := h.s.GetSession(t.Context(), h.tenant, h.session.ID)
	if err != nil || session.LastTurn != nil || session.EnvironmentInputActivity != nil {
		t.Fatal("local read admitted execution", err)
	}
}

func TestLocalEnvironmentWorkerRejectsGeneralDeviceDespiteCapability(t *testing.T) {
	h, w, environment := localWorker(t, false, false)
	if _, err := w.ReadEnvironmentDirectory(t.Context(), environment, "reports"); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("general device used as local authority", err)
	}
	other, err := h.s.CreateSession(t.Context(), h.tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "unassigned", Configuration: h.session.Configuration})
	if err != nil {
		t.Fatal(err)
	}
	unassigned, err := h.s.GetSessionEnvironment(t.Context(), h.tenant, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.ReadEnvironmentDirectory(t.Context(), unassigned, "reports"); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("unassigned environment selected general device", err)
	}
	if _, err := h.s.GetSessionDevice(t.Context(), h.tenant, other.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("read persisted an unauthorized placement", err)
	}
}

func TestLocalEnvironmentWorkerSchedulesPreparationWithoutRemoteResolver(t *testing.T) {
	h, worker, environment := localWorker(t, true, true)
	reservation, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "local-input", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"first"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	// The scheduler's scan interval is five seconds.
	_ = h.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	var frame proto.Envelope
	for frame.Type != proto.TypeExecutionPrepare {
		if h.conn.ReadJSON(&frame) != nil {
			t.Fatal("local reservation was not scheduled")
		}
	}
	var prepare proto.ExecutionPreparePayload
	if frame.DecodePayload(&prepare) != nil || prepare.Configuration.LocalEnvironment == nil || prepare.Configuration.LocalEnvironment.ID != environment.ID || prepare.Configuration.RemoteEnvironment != nil || prepare.Configuration.WorkDir != "" {
		t.Fatal("local preparation lost identity")
	}
	before, err := h.s.GetSession(t.Context(), h.tenant, h.session.ID)
	if err != nil || before.LastTurn != nil {
		t.Fatal("preparation admitted execution before readiness", err)
	}
	handle := acknowledgePreparation(h, frame.ID)
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	startFrame := h.read(proto.TypeExecutionStart)
	var start proto.ExecutionStartPayload
	if startFrame.DecodePayload(&start) != nil || start.Handle != handle || start.RunID == "" || start.Prompt != "first" {
		t.Fatal("local Start changed reservation identity")
	}
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
	h.write(start.RunID, proto.TypeDone, proto.DonePayload{Content: "complete", Metadata: map[string]any{proto.DoneMetaAgentSessionID: "local-native-history"}})
	completeLocalArtifactExport(t, h, worker, environment)
	awaitDaemonRemoteCondition(t, t.Context(), 5*time.Second, "local completion", func() bool {
		turn, err := h.s.GetTurn(t.Context(), h.tenant, h.session.ID, start.RunID)
		return err == nil && turn.Status == store.TurnCompleted
	})
	settled, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, h.session.ID, reservation.ID)
	if err != nil || settled.State != store.EnvironmentInputAdmitted || len(settled.Receipts) != 1 {
		t.Fatal("local reservation did not settle", err)
	}
	bound, err := h.s.GetSessionDevice(t.Context(), h.tenant, h.session.ID)
	if err != nil || bound.EnvironmentID != environment.ID || bound.NativeSessionID != "local-native-history" {
		t.Fatal("local native identity was not retained", err)
	}
	artifacts, err := h.s.ListSessionArtifacts(t.Context(), h.tenant, h.session.ID, environment.ID, "", 20, false)
	if err != nil || len(artifacts.Artifacts) != 1 || artifacts.Artifacts[0].Path != "/workspace/outputs/result.bin" || artifacts.Artifacts[0].TurnID != start.RunID || artifacts.Artifacts[0].SizeBytes != 3 {
		t.Fatalf("completed turn did not publish output: %+v %v", artifacts, err)
	}
}
