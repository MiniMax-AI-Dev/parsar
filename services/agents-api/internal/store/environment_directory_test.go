package store_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type directoryResult struct {
	value proto.WorkspaceDirectoryResult
	err   error
}

func directoryWorker(t *testing.T, execute ...bool) (*dispatchHarness, *execution.Worker, store.Environment, *atomic.Int32) {
	t.Helper()
	h := newDispatchHarness(t)
	var err error
	h.session, err = h.s.CreateSession(t.Context(), h.tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "read-only", Configuration: []byte(`{"agent":{"model":"unavailable-model"},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := h.s.GetSessionEnvironment(t.Context(), h.tenant, h.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: proto.AgentKindCapabilities{RemoteEnvironment: true, Preparation: true, WorkspaceReadPreparation: true}}}})
	peer, err := h.registry.LookupDevice(h.device.ID)
	if err != nil {
		t.Fatal(err)
	}
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "read preparation capability", func() bool {
		info, _, _ := peer.AgentKindStatus("codex")
		return info.Capabilities.WorkspaceReadPreparation
	})
	released := &atomic.Int32{}
	h.d.Options = func(context.Context, store.Session) (map[string]any, error) {
		if len(execute) > 0 && execute[0] {
			return nil, nil
		}
		t.Error("read resolved model credentials")
		return nil, errors.New("no credentials")
	}
	h.d.EnvironmentConnection = func(ctx context.Context, s store.Session, e store.Environment) (execution.EnvironmentConnection, error) {
		if ctx.Err() != nil || s.ID != h.session.ID || e.ID != environment.ID || e.TenantID != h.tenant {
			t.Error("wrong reader binding")
		}
		return execution.EnvironmentConnection{URL: "http://read-transport.test", Token: "synthetic-read-token", Release: func() { released.Add(1) }}, nil
	}
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
			t.Error("reader worker did not stop")
		}
	})
	return h, w, environment, released
}

func startDirectoryRead(ctx context.Context, w *execution.Worker, environment store.Environment) <-chan directoryResult {
	ch := make(chan directoryResult, 1)
	go func() {
		value, err := w.ReadEnvironmentDirectory(ctx, environment, "reports")
		ch <- directoryResult{value, err}
	}()
	return ch
}

func awaitDirectoryResult(t *testing.T, ch <-chan directoryResult) directoryResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("directory read did not return")
		return directoryResult{}
	}
}

func prepareDirectoryRead(t *testing.T, h *dispatchHarness, environment store.Environment) (string, string) {
	t.Helper()
	frame := h.read(proto.TypeExecutionPrepare)
	var request proto.ExecutionPreparePayload
	if frame.DecodePayload(&request) != nil || !proto.ValidWorkspaceReadPreparation(request.Configuration) || request.Configuration.RemoteEnvironment.ID != environment.ID || request.Configuration.AgentStateKey != "agents-api-"+h.session.ID {
		t.Fatal("read did not use the closed preparation profile")
	}
	handle := acknowledgePreparation(h, frame.ID)
	h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
	read := h.read(proto.TypeWorkspaceRead)
	var input proto.WorkspaceReadPayload
	if read.DecodePayload(&input) != nil || input.Handle != handle || input.RunID != "" || input.EnvironmentID != environment.ID || input.Path != "reports" || input.Operation != "directory" {
		t.Fatal("directory request changed binding or path")
	}
	return frame.ID, read.ID
}

func completeDirectoryRead(t *testing.T, h *dispatchHarness, request, read string, truncated, cleanupFailed bool) {
	t.Helper()
	size := int64(9)
	h.write(read, proto.TypeWorkspaceReadResult, proto.WorkspaceReadResultPayload{Outcome: "completed", CloseAcknowledged: true, Directory: &proto.WorkspaceDirectoryResult{Entries: []proto.WorkspaceDirectoryEntry{{Name: "report.txt", Kind: "file", SizeBytes: &size}}, Truncated: truncated}})
	release := h.read(proto.TypeExecutionRelease)
	var input proto.ExecutionReleasePayload
	if release.ID != request || release.DecodePayload(&input) != nil || input.Handle == "" {
		t.Fatal("reader did not release its preparation")
	}
	status := proto.PreparationStatusPayload{Handle: input.Handle, Revision: 3, State: "released"}
	if cleanupFailed {
		status.State, status.ErrorCode = "failed", "cleanup_unconfirmed"
	}
	h.write(request, proto.TypePreparationStatus, status)
}

func TestEnvironmentDirectoryWorkerReadsWithoutExecutionPrerequisites(t *testing.T) {
	h, w, environment, released := directoryWorker(t)
	foreign := environment
	foreign.TenantID = uuid.NewString()
	if _, err := w.ReadEnvironmentDirectory(t.Context(), foreign, "reports"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign reader admitted", err)
	}
	wrong := environment
	wrong.SessionID = uuid.NewString()
	if _, err := w.ReadEnvironmentDirectory(t.Context(), wrong, "reports"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("wrong Session admitted", err)
	}
	result := startDirectoryRead(t.Context(), w, environment)
	request, read := prepareDirectoryRead(t, h, environment)
	if _, err := w.ReadEnvironmentDirectory(t.Context(), environment, "reports"); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("second idle reader bypassed Session owner", err)
	}
	select {
	case <-result:
		t.Fatal("read returned before settlement")
	default:
	}
	completeDirectoryRead(t, h, request, read, false, false)
	got := awaitDirectoryResult(t, result)
	if got.err != nil || len(got.value.Entries) != 1 || released.Load() != 1 {
		t.Fatal("directory result", got.err, released.Load())
	}
	session, err := h.s.GetSession(t.Context(), h.tenant, h.session.ID)
	if err != nil || session.LastTurn != nil || session.EnvironmentInputActivity != nil {
		t.Fatal("directory read manufactured execution")
	}
	bound, err := h.s.GetSessionDevice(t.Context(), h.tenant, h.session.ID)
	if err != nil || bound.ID != h.device.ID || bound.NativeSessionID != "" {
		t.Fatal("directory read changed native history identity")
	}
}

func TestEnvironmentDirectoryWorkerRejectsIncompleteOrUnreleasedResults(t *testing.T) {
	for _, mode := range []string{"truncated", "cleanup_failed"} {
		t.Run(mode, func(t *testing.T) {
			h, w, environment, released := directoryWorker(t)
			result := startDirectoryRead(t.Context(), w, environment)
			request, read := prepareDirectoryRead(t, h, environment)
			completeDirectoryRead(t, h, request, read, mode == "truncated", mode == "cleanup_failed")
			got := awaitDirectoryResult(t, result)
			if !errors.Is(got.err, execution.ErrExecutionUnavailable) || len(got.value.Entries) != 0 || released.Load() != 1 {
				t.Fatal("incomplete reader exposed data or retained authority", got.err)
			}
		})
	}
}

func TestEnvironmentDirectorySequentialReadsReleaseSchedulingOwnership(t *testing.T) {
	h, w, environment, released := directoryWorker(t)
	const pages = 32
	done := make(chan error, 1)
	go func() {
		for range pages {
			value, err := w.ReadEnvironmentDirectory(t.Context(), environment, "reports")
			if err != nil {
				done <- err
				return
			}
			if len(value.Entries) != 1 {
				done <- errors.New("missing directory page")
				return
			}
		}
		done <- nil
	}()
	for range pages {
		request, read := prepareDirectoryRead(t, h, environment)
		completeDirectoryRead(t, h, request, read, false, false)
	}
	select {
	case err := <-done:
		if err != nil || released.Load() != pages {
			t.Fatal("sequential reads retained ownership", err, released.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sequential reads did not finish")
	}
}

func TestEnvironmentDirectoryObserverCancellationRetainsReadOwner(t *testing.T) {
	h, w, environment, released := directoryWorker(t)
	ctx, cancel := context.WithCancel(t.Context())
	result := startDirectoryRead(ctx, w, environment)
	request, read := prepareDirectoryRead(t, h, environment)
	cancel()
	if got := awaitDirectoryResult(t, result); !errors.Is(got.err, execution.ErrExecutionUnavailable) {
		t.Fatal("cancelled observer result", got.err)
	}
	if released.Load() != 0 {
		t.Fatal("observer cancelled native ownership")
	}
	if _, err := w.ReadEnvironmentDirectory(t.Context(), environment, "reports"); !errors.Is(err, execution.ErrExecutionUnavailable) {
		t.Fatal("cancelled observer freed Session owner")
	}
	completeDirectoryRead(t, h, request, read, false, false)
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "reader release", func() bool { return released.Load() == 1 })
}
