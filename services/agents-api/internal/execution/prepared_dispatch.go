package execution

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type EnvironmentConnection struct {
	URL, Token string
	Release    func()
}

type EnvironmentRun struct {
	Reservation store.EnvironmentInputReservation
	Turn        store.Turn
}

// RunEnvironmentInput owns a private preparation through its first admitted Run.
func (d *Dispatcher) RunEnvironmentInput(ctx context.Context, tenantID, sessionID, reservationID string) (run EnvironmentRun, err error) {
	if err = d.Store.CheckExecutionOwnership(ctx); err != nil {
		return run, err
	}
	run.Reservation, err = d.Store.ExpireEnvironmentInput(ctx, tenantID, sessionID, reservationID)
	if err != nil || run.Reservation.State != store.EnvironmentInputPending {
		return run, err
	}
	session, err := d.Store.GetSession(ctx, tenantID, sessionID)
	if err != nil {
		return run, err
	}
	environment, err := d.Store.GetSessionEnvironment(ctx, tenantID, sessionID)
	if err != nil {
		return run, err
	}
	var snapshot Snapshot
	if json.Unmarshal(session.Configuration, &snapshot) != nil || snapshot.Daemon != nil || strings.TrimSpace(snapshot.Agent.Model) == "" {
		return run, store.ErrInvalidInput
	}
	bound, err := d.Store.GetSessionDevice(ctx, tenantID, sessionID)
	if err != nil {
		return run, err
	}
	peer, err := d.Registry.LookupDevice(bound.ID)
	if err != nil {
		return run, err
	}
	caps, err := engineCapabilities(peer, session.Engine, snapshot)
	if err != nil {
		return run, err
	}
	owner, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := d.executionRequest(owner, session, snapshot, caps, bound.NativeSessionID)
	if err != nil {
		return run, err
	}
	var messages []string
	for _, input := range run.Reservation.Inputs {
		if input.Kind != "message" {
			return run, store.ErrInvalidInput
		}
		text, err := messageText(input.Payload)
		if err != nil {
			return run, err
		}
		messages = append(messages, text)
	}
	release, err := d.configurePreparedEnvironment(owner, session, environment, bound, &req)
	if release != nil {
		defer release()
	}
	if err != nil {
		return run, err
	}
	prepared, err := newPreparedStart(peer)
	if err != nil {
		return run, err
	}
	defer prepared.close()
	if err = send(owner, peer, proto.TypeExecutionPrepare, prepared.requestID, proto.ExecutionPreparePayload{Configuration: req}); err != nil {
		return run, err
	}
	run.Reservation, err = d.awaitPreparation(owner, tenantID, sessionID, run.Reservation, prepared)
	if err != nil || run.Reservation.State != store.EnvironmentInputPending {
		return run, err
	}
	run.Reservation, err = d.Store.PromoteEnvironmentInput(owner, tenantID, sessionID, reservationID)
	if err != nil || run.Reservation.State != store.EnvironmentInputAdmitted {
		return run, err
	}
	if run.Reservation.Receipts[0].Replayed {
		return run, nil
	}
	req.RunID = run.Reservation.Receipts[0].TurnID
	req.Prompt = strings.Join(messages, "\n\n")
	through := run.Reservation.Receipts[len(run.Reservation.Receipts)-1].Sequence
	result, status := d.deliver(owner, tenantID, sessionID, peer, req, through, prepared)
	result, status = d.captureCompletedArtifacts(owner, peer, session, environment, bound, req.RunID, result, status)
	run.Turn, err = d.finishRun(tenantID, sessionID, req.RunID, snapshot.Agent.Model, result, status)
	return run, err
}
