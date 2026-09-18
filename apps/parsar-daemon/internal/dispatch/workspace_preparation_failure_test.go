package dispatch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type workspaceFailureBoundary struct {
	slog.Handler
	once            sync.Once
	entered, resume chan struct{}
}

func (h *workspaceFailureBoundary) Handle(ctx context.Context, record slog.Record) error {
	h.once.Do(func() { close(h.entered); <-h.resume })
	return h.Handler.Handle(ctx, record)
}

type workspaceCloseFunc struct {
	agent.Prepared
	close func() error
}

func (p workspaceCloseFunc) Close() error { return p.close() }

func TestReadConstructorFailureCannotPublishReleaseDuringCleanupRetry(t *testing.T) {
	boundary := &workspaceFailureBoundary{Handler: slog.NewTextHandler(io.Discard, nil), entered: make(chan struct{}), resume: make(chan struct{})}
	sender := make(workspaceStatusSender, 16)
	r := &Router{sender: sender, shutdownCh: make(chan struct{}), log: slog.New(boundary)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	retryEntered, retryResume := make(chan struct{}), make(chan struct{})
	var resumeOnce sync.Once
	defer resumeOnce.Do(func() { close(retryResume) })
	calls := 0
	resource := workspaceCloseFunc{close: func() error {
		calls++
		if calls == 2 {
			close(retryEntered)
			<-retryResume
		}
		return errors.New("cleanup incomplete")
	}}
	p := &preparationState{workspaceReadOnly: true, owns: true, busy: true,
		ctx: ctx, cancel: cancel, timer: timer, deadline: time.Now().Add(time.Hour),
		status: proto.PreparationStatusPayload{Handle: "reader", Revision: 1, State: "preparing"}}
	r.shutdownWG.Add(1)
	prepared := make(chan struct{})
	go func() {
		r.prepareExecution(p, proto.PromptRequestPayload{}, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
			return resource, errors.New("construction failed")
		})
		close(prepared)
	}()
	<-boundary.entered
	r.releasePreparation(p, "released", "", true, true)
	<-retryEntered
	close(boundary.resume)
	<-prepared
	for len(sender) > 0 {
		var status proto.PreparationStatusPayload
		if (<-sender).DecodePayload(&status) == nil && status.State == "released" {
			t.Error("constructor published release while retry cleanup was blocked")
		}
	}
	resumeOnce.Do(func() { close(retryResume) })
	r.shutdownWG.Wait()
	if !p.owns || p.busy || p.status.ErrorCode != "cleanup_unconfirmed" || calls != 2 {
		t.Fatal("failed retry did not retain cleanup ownership")
	}
}
