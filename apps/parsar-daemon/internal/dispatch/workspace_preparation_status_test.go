package dispatch

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

type workspaceStatusSender chan proto.Envelope

func (s workspaceStatusSender) Send(_ context.Context, envelope proto.Envelope) error {
	s <- envelope
	return nil
}

type offlineWorkspaceStatusSender struct{}

func (offlineWorkspaceStatusSender) Send(context.Context, proto.Envelope) error {
	return errors.New("observer disconnected")
}

type unsettledWorkspacePreparation struct {
	agent.Prepared
	calls   atomic.Int32
	settled atomic.Bool
}

func (p *unsettledWorkspacePreparation) Close() error {
	// Bound a regression so a recursive retry cannot hang the test.
	if p.calls.Add(1) >= 100 || p.settled.Load() {
		return nil
	}
	return errors.New("cleanup incomplete")
}

func TestReadPreparationOfflineStatusDoesNotRetryCleanup(t *testing.T) {
	prepared := &unsettledWorkspacePreparation{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	r := &Router{sender: offlineWorkspaceStatusSender{}, shutdownCh: make(chan struct{}), log: obslog.Bg()}
	p := &preparationState{workspaceReadOnly: true, owns: true, prepared: prepared,
		ctx: ctx, cancel: cancel, timer: timer,
		status: proto.PreparationStatusPayload{Handle: "reader", Revision: 1, State: "ready"}}
	for attempt := int32(1); attempt <= 2; attempt++ {
		r.releasePreparation(p, "released", "", true)
		r.shutdownWG.Wait()
		if got := prepared.calls.Load(); got != attempt {
			t.Fatalf("explicit release %d caused %d cleanup attempts", attempt, got)
		}
		if !p.owns || p.busy || p.prepared != prepared || p.status.ErrorCode != "cleanup_unconfirmed" {
			t.Fatal("unconfirmed cleanup lost its resource ownership")
		}
	}
	prepared.settled.Store(true)
	r.releasePreparation(p, "released", "", true)
	r.shutdownWG.Wait()
	if prepared.calls.Load() != 3 || p.owns || p.prepared != nil || p.status.State != "released" {
		t.Fatal("explicit retry did not settle resource ownership")
	}
}

func TestReadPreparationRetryCannotPublishStaleRelease(t *testing.T) {
	sender := make(workspaceStatusSender, 4)
	r := &Router{sender: sender, shutdownCh: make(chan struct{})}
	p := &preparationState{workspaceReadOnly: true, owns: true, busy: true,
		status: proto.PreparationStatusPayload{Handle: "reader", Revision: 2, State: "released"}}
	// A prepare retry captures this snapshot while Close is still running.
	snapshot := p.status
	// Close fails before the retry reaches publication.
	p.busy = false
	p.status = proto.PreparationStatusPayload{Handle: "reader", Revision: 3, State: "failed", ErrorCode: "cleanup_unconfirmed"}
	r.publishPreparation(p, snapshot)
	r.shutdownWG.Wait()
	if len(sender) != 0 {
		t.Fatal("stale release success escaped after failed Close")
	}
	r.publishPreparation(p, p.status)
	r.shutdownWG.Wait()
	var failed proto.PreparationStatusPayload
	if len(sender) != 1 || (<-sender).DecodePayload(&failed) != nil || failed.State != "failed" {
		t.Fatal("confirmed cleanup failure was suppressed")
	}
}
