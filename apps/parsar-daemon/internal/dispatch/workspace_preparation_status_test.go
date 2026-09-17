package dispatch

import (
	"context"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type workspaceStatusSender chan proto.Envelope

func (s workspaceStatusSender) Send(_ context.Context, envelope proto.Envelope) error {
	s <- envelope
	return nil
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
