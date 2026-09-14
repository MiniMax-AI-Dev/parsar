package execution

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type preparedStart struct {
	peer      *gateway.Session
	requestID string
	handle    string
	sub       *gateway.Subscription
}

func newPreparedStart(peer *gateway.Session) (*preparedStart, error) {
	id := uuid.NewString()
	sub, err := peer.SubscribePreparation(id)
	if err != nil {
		return nil, err
	}
	return &preparedStart{peer: peer, requestID: id, sub: sub}, nil
}

func (p *preparedStart) close() {
	if p.handle != "" {
		_ = send(context.Background(), p.peer, proto.TypeExecutionRelease, p.requestID, proto.ExecutionReleasePayload{Handle: p.handle})
	}
	p.peer.UnsubscribePreparation(p.requestID)
}

func (p *preparedStart) observation(env proto.Envelope) (proto.PreparationStatusPayload, error) {
	var status proto.PreparationStatusPayload
	if env.Type != proto.TypePreparationStatus || env.ID != p.requestID || env.DecodePayload(&status) != nil {
		return status, errors.New("invalid preparation control response")
	}
	if status.State == "rejected" {
		return status, errors.New("preparation control rejected")
	}
	if status.Handle == "" || status.Revision == 0 || (p.handle != "" && p.handle != status.Handle) {
		return status, errors.New("preparation identity changed")
	}
	p.handle = status.Handle
	switch status.State {
	case "preparing", "ready", "starting", "started":
		return status, nil
	default:
		return status, errors.New("preparation is no longer available")
	}
}

func (d *Dispatcher) awaitPreparation(ctx context.Context, tenant, session string, pending store.EnvironmentInputReservation, prepared *preparedStart) (store.EnvironmentInputReservation, error) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return pending, ctx.Err()
		case <-tick.C:
			current, err := d.Store.ExpireEnvironmentInput(ctx, tenant, session, pending.ID)
			if err != nil || current.State != store.EnvironmentInputPending {
				return current, err
			}
		case env, ok := <-prepared.sub.Events:
			if !ok {
				return pending, errors.New("preparation control stream closed")
			}
			status, err := prepared.observation(env)
			if err != nil {
				return pending, err
			}
			if status.State == "ready" && status.RunID == "" {
				return pending, nil
			}
			if status.State != "preparing" {
				return pending, errors.New("unexpected preparation state before admission")
			}
		}
	}
}

func (p *preparedStart) start(ctx context.Context, request proto.PromptRequestPayload) error {
	return send(ctx, p.peer, proto.TypeExecutionStart, p.requestID, proto.ExecutionStartPayload{Handle: p.handle, RunID: request.RunID, Prompt: request.Prompt})
}

func (p *preparedStart) started(env proto.Envelope, runID string) (bool, error) {
	status, err := p.observation(env)
	if err != nil {
		return false, err
	}
	if status.RunID != runID || (status.State != "starting" && status.State != "started") {
		return false, errors.New("unexpected preparation state after admission")
	}
	return status.State == "started", nil
}
