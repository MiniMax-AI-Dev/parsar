package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type localWriteResult struct {
	size int64
	err  error
}

func startLocalWrite(ctx context.Context, w *execution.Worker, e store.Environment) <-chan localWriteResult {
	done := make(chan localWriteResult, 1)
	go func() {
		size, err := w.WriteEnvironmentFile(ctx, e, "input", []byte("abc"))
		done <- localWriteResult{size, err}
	}()
	return done
}

func awaitLocalWrite(t *testing.T, done <-chan localWriteResult) localWriteResult {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("write did not return")
		return localWriteResult{}
	}
}

func TestLocalEnvironmentFileWriteOwnsMutationBeforeDispatch(t *testing.T) {
	h, w, environment := localWorker(t, true, false)
	foreign := environment
	foreign.TenantID = uuid.NewString()
	if _, err := w.WriteEnvironmentFile(t.Context(), foreign, "input", nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign upload", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := startLocalWrite(ctx, w, environment)
	begin := h.read(proto.TypeWorkspaceWrite)
	var request proto.WorkspaceWritePayload
	if begin.DecodePayload(&request) != nil || request.Step != "begin" || request.EnvironmentID != environment.ID || request.SessionID != h.session.ID || request.Path != "input" || request.SizeBytes != 3 {
		t.Fatal("upload identity changed")
	}
	intent, err := h.s.GetEnvironmentFileWrite(t.Context(), h.tenant, environment.ID, begin.ID)
	if err != nil || intent.State != "pending" || intent.Identity.DeviceID != h.device.ID {
		t.Fatal("dispatch preceded durable ownership", intent, err)
	}
	if _, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "concurrent", []store.Input{{Kind: "message", Payload: []byte(`{"text":"work"}`)}}); !errors.Is(err, store.ErrTurnConflict) {
		t.Fatal("upload admitted concurrent execution", err)
	}
	cancel()
	if got := awaitLocalWrite(t, done); !errors.Is(got.err, execution.ErrExecutionUnavailable) {
		t.Fatal("detached observer", got.err)
	}
	h.write(begin.ID, proto.TypeWorkspaceWriteResult, proto.WorkspaceWriteResultPayload{Outcome: "ready"})
	chunk := h.read(proto.TypeWorkspaceWrite)
	if chunk.ID != begin.ID || chunk.DecodePayload(&request) != nil || request.Step != "chunk" || string(request.Data) != "abc" {
		t.Fatal("body changed")
	}
	h.write(begin.ID, proto.TypeWorkspaceWriteResult, proto.WorkspaceWriteResultPayload{Outcome: "received", Offset: 3})
	commit := h.read(proto.TypeWorkspaceWrite)
	if commit.ID != begin.ID || commit.DecodePayload(&request) != nil || request.Step != "commit" {
		t.Fatal("commit changed")
	}
	h.write(begin.ID, proto.TypeWorkspaceWriteResult, proto.WorkspaceWriteResultPayload{Outcome: "completed", SizeBytes: 3})
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "detached durable commit", func() bool {
		got, e := h.s.GetEnvironmentFileWrite(t.Context(), h.tenant, environment.ID, begin.ID)
		return e == nil && got.State == "committed"
	})
	session, err := h.s.GetSession(t.Context(), h.tenant, h.session.ID)
	if err != nil || session.LastTurn != nil {
		t.Fatal("upload created model execution", err)
	}
}

func TestLocalEnvironmentFileWriteLostReceiptRemainsPending(t *testing.T) {
	h, w, environment := localWorker(t, true, false)
	done := startLocalWrite(t.Context(), w, environment)
	begin := h.read(proto.TypeWorkspaceWrite)
	if err := h.conn.Close(); err != nil {
		t.Fatal(err)
	}
	if result := awaitLocalWrite(t, done); !errors.Is(result.err, execution.ErrExecutionUnavailable) {
		t.Fatal(result.err)
	}
	intent, err := h.s.GetEnvironmentFileWrite(t.Context(), h.tenant, environment.ID, begin.ID)
	if err != nil || intent.State != "pending" {
		t.Fatal("disconnect guessed rejection", intent, err)
	}
	if _, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, h.session.ID, "after-loss", []store.Input{{Kind: "message", Payload: []byte(`{"text":"work"}`)}}); !errors.Is(err, store.ErrTurnConflict) {
		t.Fatal("unknown upload admitted execution", err)
	}
}

func TestLocalEnvironmentFileWriteKnownRejectionReleasesMutation(t *testing.T) {
	h, w, environment := localWorker(t, true, false)
	for range 2 {
		done := startLocalWrite(t.Context(), w, environment)
		begin := h.read(proto.TypeWorkspaceWrite)
		h.write(begin.ID, proto.TypeWorkspaceWriteResult, proto.WorkspaceWriteResultPayload{Outcome: "rejected", ErrorCode: "resource_unavailable"})
		if result := awaitLocalWrite(t, done); !errors.Is(result.err, execution.ErrExecutionUnavailable) {
			t.Fatal(result.err)
		}
		intent, err := h.s.GetEnvironmentFileWrite(t.Context(), h.tenant, environment.ID, begin.ID)
		if err != nil || intent.State != "rejected" {
			t.Fatal("rejection did not settle", intent, err)
		}
	}
}

func TestLocalEnvironmentFileWriteRejectsUnscopedDevice(t *testing.T) {
	_, w, environment := localWorker(t, false, false)
	if _, err := w.WriteEnvironmentFile(t.Context(), environment, "input", nil); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("unscoped writer selected", err)
	}
}
