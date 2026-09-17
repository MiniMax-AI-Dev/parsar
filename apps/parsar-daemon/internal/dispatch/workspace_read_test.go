package dispatch_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type workspaceTestReader struct {
	read func(context.Context, string, int) (agent.WorkspaceReadResult, error)
}

func (r *workspaceTestReader) ReadWorkspaceFile(ctx context.Context, path string, limit int) (agent.WorkspaceReadResult, error) {
	return r.read(ctx, path, limit)
}

type readablePreparation struct {
	*controlledPreparation
	*workspaceTestReader
}
type readableSession struct {
	*fakeSession
	*workspaceTestReader
}

func waitWorkspaceRead(t *testing.T, sender *recSender, id string) proto.WorkspaceReadResultPayload {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, env := range sender.snapshot() {
			if env.Type == proto.TypeWorkspaceReadResult && env.ID == id {
				var result proto.WorkspaceReadResultPayload
				if env.DecodePayload(&result) != nil {
					t.Fatal("invalid read result")
				}
				return result
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("read result missing", id)
	return proto.WorkspaceReadResultPayload{}
}

func TestWorkspaceReadUsesPreparationThenTransferredRun(t *testing.T) {
	sender := &recSender{}
	var calls atomic.Int32
	reader := &workspaceTestReader{read: func(_ context.Context, path string, limit int) (agent.WorkspaceReadResult, error) {
		calls.Add(1)
		if path != "file" || limit != 3 {
			return agent.WorkspaceReadResult{}, errors.New("request changed")
		}
		return agent.WorkspaceReadResult{Data: []byte{0, 1, 255}, Truncated: true}, nil
	}}
	p := &readablePreparation{controlledPreparation: &controlledPreparation{closed: make(chan struct{})}, workspaceTestReader: reader}
	p.start = func(ctx context.Context, _ string, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		return &readableSession{&fakeSession{out: out, ctx: ctx, closeOutOnCancel: true}, reader}, nil
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "prepare", preparationRequest()))
	ready := waitPreparationStatus(t, sender, "prepare", "ready", "")
	request := proto.WorkspaceReadPayload{Handle: ready.Handle, EnvironmentID: "environment", Path: "file", MaxBytes: 3}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, "idle", request))
	if result := waitWorkspaceRead(t, sender, "idle"); result.Outcome != "completed" || !result.CloseAcknowledged || !result.Truncated {
		t.Fatal(result)
	}
	bad := request
	bad.EnvironmentID = "another-environment"
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, "foreign", bad))
	if result := waitWorkspaceRead(t, sender, "foreign"); result.ErrorCode != "resource_unavailable" {
		t.Fatal(result)
	}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionStart, "prepare", proto.ExecutionStartPayload{Handle: ready.Handle, RunID: "run", Prompt: "start"}))
	waitPreparationStatus(t, sender, "prepare", "started", "")
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, "old-handle", request))
	if result := waitWorkspaceRead(t, sender, "old-handle"); result.Outcome != "rejected" {
		t.Fatal(result)
	}
	request.Handle, request.RunID = "", "run"
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, "active", request))
	if result := waitWorkspaceRead(t, sender, "active"); result.Outcome != "completed" {
		t.Fatal(result)
	}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypePromptCancel, "run", proto.PromptCancelPayload{}))
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, "cancelled", request))
	if result := waitWorkspaceRead(t, sender, "cancelled"); result.Outcome != "rejected" {
		t.Fatal(result)
	}
	if calls.Load() != 2 {
		t.Fatal("rejected reads reached adapter", calls.Load())
	}
}

func TestWorkspaceReadWaitSurvivesObserverAndResourceRelease(t *testing.T) {
	sender := &recSender{}
	entered, settle := make(chan context.Context, 1), make(chan struct{})
	p := &readablePreparation{controlledPreparation: &controlledPreparation{closed: make(chan struct{})}, workspaceTestReader: &workspaceTestReader{read: func(ctx context.Context, _ string, _ int) (agent.WorkspaceReadResult, error) {
		entered <- ctx
		<-settle
		return agent.WorkspaceReadResult{}, agent.ErrWorkspaceReadUncertain
	}}}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "prepare", preparationRequest()))
	ready := waitPreparationStatus(t, sender, "prepare", "ready", "")
	request := proto.WorkspaceReadPayload{Handle: ready.Handle, EnvironmentID: "environment", Path: "file", MaxBytes: 3}
	observer, cancel := context.WithCancel(t.Context())
	_ = r.Handle(observer, mustEnv(t, proto.TypeWorkspaceRead, "read", request))
	operation := <-entered
	cancel()
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, "read", request)); err == nil {
		t.Fatal("duplicate pending operation accepted")
	}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionRelease, "prepare", proto.ExecutionReleasePayload{Handle: ready.Handle}))
	waitPreparationClosed(t, p.controlledPreparation)
	if operation.Err() != nil {
		t.Fatal("observer/release discarded accepted waiter")
	}
	short, stop := context.WithTimeout(t.Context(), 20*time.Millisecond)
	if err := r.Shutdown(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("shutdown lost pending read", err)
	}
	stop()
	close(settle)
	if result := waitWorkspaceRead(t, sender, "read"); result.Outcome != "unknown" || len(result.Data) != 0 || result.CloseAcknowledged {
		t.Fatal(result)
	}
	if err := r.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceReadCapacityIsConnectionBounded(t *testing.T) {
	sender := &recSender{}
	entered, settle := make(chan struct{}, 4), make(chan struct{})
	p := &readablePreparation{controlledPreparation: &controlledPreparation{closed: make(chan struct{})}, workspaceTestReader: &workspaceTestReader{read: func(context.Context, string, int) (agent.WorkspaceReadResult, error) {
		entered <- struct{}{}
		<-settle
		return agent.WorkspaceReadResult{}, nil
	}}}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "prepare", preparationRequest()))
	ready := waitPreparationStatus(t, sender, "prepare", "ready", "")
	request := proto.WorkspaceReadPayload{Handle: ready.Handle, EnvironmentID: "environment", Path: "file", MaxBytes: 3}
	for i := 0; i < 4; i++ {
		_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, fmt.Sprint(i), request))
		<-entered
	}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, "excess", request))
	if result := waitWorkspaceRead(t, sender, "excess"); result.ErrorCode != "read_capacity" {
		t.Fatal(result)
	}
	close(settle)
	for i := 0; i < 4; i++ {
		if result := waitWorkspaceRead(t, sender, fmt.Sprint(i)); result.Outcome != "completed" {
			t.Fatal(result)
		}
	}
}
