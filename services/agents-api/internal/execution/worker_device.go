package execution

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func (w *Worker) bind(ctx context.Context, item store.ExecutionWork) (bool, error) {
	ready, err := w.bindDevice(ctx, item.TenantID, item.SessionID)
	if !errors.Is(err, store.ErrDeviceBindingConflict) {
		return ready, err
	}
	_, err = w.dispatcher.Store.TransitionTurn(ctx, item.TenantID, item.SessionID, item.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnFailed, Outcome: json.RawMessage(`{"error_code":"execution_device_unavailable"}`)})
	if errors.Is(err, store.ErrTurnConflict) {
		err = nil
	}
	return false, err
}

func (w *Worker) bindDevice(ctx context.Context, tenantID, sessionID string) (bool, error) {
	session, err := w.dispatcher.Store.GetSession(ctx, tenantID, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(session.Configuration, &snapshot); err != nil {
		return false, err
	}
	return w.bindSessionDevice(ctx, session, func(id string) bool { return w.ready(id, session.Engine, snapshot) })
}

func (w *Worker) bindSessionDevice(ctx context.Context, session store.Session, ready func(string) bool) (bool, error) {
	bound, err := w.dispatcher.Store.GetSessionDevice(ctx, session.TenantID, session.ID)
	if err == nil {
		return ready(bound.ID), nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	devices, err := w.dispatcher.Store.ListExecutionDevices(ctx, session.TenantID)
	if err != nil {
		return false, err
	}
	for _, device := range devices {
		if !ready(device.ID) {
			continue
		}
		err := w.dispatcher.Store.BindSessionDevice(ctx, session.TenantID, session.ID, device.ID)
		return err == nil, err
	}
	return false, nil
}

func (w *Worker) ready(deviceID, engine string, snapshot Snapshot) bool {
	peer, err := w.dispatcher.Registry.LookupDevice(deviceID)
	if err != nil {
		return false
	}
	_, err = engineCapabilities(peer, engine, snapshot)
	return err == nil
}
