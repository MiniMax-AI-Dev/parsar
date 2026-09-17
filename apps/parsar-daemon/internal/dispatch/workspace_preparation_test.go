package dispatch_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestWorkspaceReadPreparationRejectsStartAndWaitsForClose(t *testing.T) {
	sender := &recSender{}
	entered, release := make(chan struct{}), make(chan struct{})
	p := &controlledPreparation{closed: make(chan struct{}), closeHook: func() { close(entered); <-release }}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	request := preparationRequest()
	request.Configuration.WorkspaceReadOnly = true
	prepare := mustEnv(t, proto.TypeExecutionPrepare, "read", request)
	if err := r.Handle(t.Context(), prepare); err != nil {
		t.Fatal(err)
	}
	ready := waitPreparationStatus(t, sender, "read", "ready", "")
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionStart, "read", proto.ExecutionStartPayload{Handle: ready.Handle, RunID: "run", Prompt: "work"})); err == nil || p.starts.Load() != 0 {
		t.Fatal("read owner admitted a Run")
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionRelease, "read", proto.ExecutionReleasePayload{Handle: ready.Handle})); err != nil {
		t.Fatal(err)
	}
	<-entered
	// An idempotent prepare retry must not expose a premature terminal status.
	if err := r.Handle(t.Context(), prepare); err != nil {
		t.Fatal(err)
	}
	for _, envelope := range sender.snapshot() {
		var status proto.PreparationStatusPayload
		if envelope.DecodePayload(&status) == nil && status.State == "released" {
			t.Fatal("release acknowledged before native close")
		}
	}
	close(release)
	waitPreparationStatus(t, sender, "read", "released", "")
	if owned, _ := r.PreparationOwnershipForTest(ready.Handle); owned {
		t.Fatal("settled release retained ownership")
	}
}

func TestWorkspaceReadPreparationRetainsFailedCleanup(t *testing.T) {
	sender := &recSender{}
	p := &retryablePreparation{close: func(call int32) error {
		if call == 1 {
			return errors.New("controlled cleanup failure")
		}
		return nil
	}}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	request := preparationRequest()
	request.Configuration.WorkspaceReadOnly = true
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "read", request)); err != nil {
		t.Fatal(err)
	}
	ready := waitPreparationStatus(t, sender, "read", "ready", "")
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionRelease, "read", proto.ExecutionReleasePayload{Handle: ready.Handle})); err != nil {
		t.Fatal(err)
	}
	failed := waitPreparationStatus(t, sender, "read", "failed", "")
	if failed.ErrorCode != "cleanup_unconfirmed" {
		t.Fatal("cleanup failure hidden")
	}
	if owned, _ := r.PreparationOwnershipForTest(ready.Handle); !owned {
		t.Fatal("uncertain cleanup discarded ownership")
	}
}
