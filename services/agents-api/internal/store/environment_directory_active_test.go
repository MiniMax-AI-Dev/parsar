package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestEnvironmentDirectoryActiveRunUsesExistingOwner(t *testing.T) {
	h, w, environment, released := directoryWorker(t, true)
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: proto.AgentKindCapabilities{Streaming: true, Steering: true, DurableTurns: true, DurableInputReceipts: true, WebSearchControl: true, TextVerbosity: true, ExecutionControls: true, SubagentControl: true, ToolObservations: true, Preparation: true, RemoteEnvironment: true, WorkspaceReadPreparation: true}}}})
	pending, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "execute", []store.Input{{Kind: "message", Payload: []byte(`{"text":"work"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	prepare := h.read(proto.TypeExecutionPrepare)
	handle := acknowledgePreparation(h, prepare.ID)
	h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	frame := h.read(proto.TypeExecutionStart)
	var start proto.ExecutionStartPayload
	if frame.DecodePayload(&start) != nil || start.RunID == "" {
		t.Fatal("execution did not start")
	}
	h.write(prepare.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
	result := startDirectoryRead(t.Context(), w, environment)
	read := h.read(proto.TypeWorkspaceRead)
	var request proto.WorkspaceReadPayload
	if read.DecodePayload(&request) != nil || request.RunID != start.RunID || request.Handle != "" || request.EnvironmentID != environment.ID {
		t.Fatal("active read selected another execution owner")
	}
	size := int64(3)
	h.write(read.ID, proto.TypeWorkspaceReadResult, proto.WorkspaceReadResultPayload{Outcome: "completed", CloseAcknowledged: true, Directory: &proto.WorkspaceDirectoryResult{Entries: []proto.WorkspaceDirectoryEntry{{Name: "active.txt", Kind: "file", SizeBytes: &size}}}})
	if got := awaitDirectoryResult(t, result); got.err != nil || len(got.value.Entries) != 1 {
		t.Fatal("active read", got.err)
	}
	if released.Load() != 0 {
		t.Fatal("active read released model execution")
	}
	h.write(start.RunID, proto.TypeDone, proto.DonePayload{Content: "finished"})
	run := awaitWorkerEnvironmentRun(t, t.Context(), h.s, h.tenant, pending)
	if run.Turn.Status != store.TurnCompleted {
		t.Fatal("active read changed Turn outcome")
	}
	awaitDaemonRemoteCondition(t, context.Background(), 3*time.Second, "execution credential release", func() bool { return released.Load() == 1 })
}
