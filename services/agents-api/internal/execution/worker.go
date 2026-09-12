package execution

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// Worker owns queued work; the database lease excludes a second execution service.
type Worker struct {
	Dispatcher *Dispatcher
	lease      *store.ExecutionLease
}

func StartWorker(ctx context.Context, dispatcher *Dispatcher) (*Worker, error) {
	lease, err := dispatcher.Store.AcquireExecutionLease(ctx)
	if err != nil {
		return nil, err
	}
	worker := &Worker{Dispatcher: dispatcher, lease: lease}
	if err := worker.reconcile(ctx); err != nil {
		_ = lease.Close(context.Background())
		return nil, err
	}
	return worker, nil
}

func (w *Worker) SubmitInputs(ctx context.Context, tenant, session, key string, inputs []store.Input) ([]store.InputReceipt, error) {
	value, err := w.Dispatcher.Store.GetSession(ctx, tenant, session)
	if err != nil {
		return nil, err
	}
	var snapshot Snapshot
	if value.Engine != "codex" || json.Unmarshal(value.Configuration, &snapshot) != nil || snapshot.Environment == nil || snapshot.Environment.Type != "none" || snapshot.Daemon != nil {
		return nil, store.ErrInvalidInput
	}
	return w.Dispatcher.Store.SubmitInputs(ctx, tenant, session, key, inputs)
}

// Run retains queued work across restarts, but never replays an uncertain claim.
func (w *Worker) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	var running sync.WaitGroup
	defer func() {
		cancel()
		running.Wait()
		closeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = w.lease.Close(closeCtx)
	}()
	active := make(map[string]bool)
	type completion struct {
		id  string
		err error
	}
	completed := make(chan completion, 4)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	cursor := ""
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-completed:
			delete(active, result.id)
			if result.err != nil {
				return result.err
			}
		case <-ticker.C:
			check, stop := context.WithTimeout(ctx, 5*time.Second)
			err := w.lease.Ping(check)
			stop()
			if err != nil {
				return err
			}
			if len(active) == 4 {
				continue
			}
			devices := w.Dispatcher.Registry.Devices()
			if len(devices) == 0 {
				continue
			}
			work, err := w.Dispatcher.Store.ListExecutionWork(ctx, cursor, []string{store.TurnQueued}, devices)
			if err != nil {
				return err
			}
			if len(work) == 0 {
				cursor = ""
				continue
			}
			for _, item := range work {
				if len(active) == 4 {
					break
				}
				cursor = item.TurnID
				if active[item.TurnID] {
					continue
				}
				ready, err := w.bind(ctx, item)
				if err != nil {
					return err
				}
				if !ready {
					continue
				}
				active[item.TurnID] = true
				running.Add(1)
				go func() {
					defer running.Done()
					err := w.runClaim(ctx, item)
					completed <- completion{id: item.TurnID, err: err}
				}()
			}
		}
	}
}

func (w *Worker) reconcile(ctx context.Context) error {
	cursor := ""
	for {
		work, err := w.Dispatcher.Store.ListExecutionWork(ctx, cursor, []string{store.TurnInProgress, store.TurnWaiting}, nil)
		if err != nil {
			return err
		}
		if len(work) == 0 {
			return nil
		}
		for _, item := range work {
			_, err := w.Dispatcher.Store.TransitionTurn(ctx, item.TenantID, item.SessionID, item.TurnID, store.TurnTransition{ExpectedStatus: item.Status, Status: store.TurnFailed, Outcome: json.RawMessage(`{"error_code":"execution_interrupted"}`)})
			if err != nil && !errors.Is(err, store.ErrTurnConflict) {
				return err
			}
			cursor = item.TurnID
		}
	}
}

func (w *Worker) bind(ctx context.Context, item store.ExecutionWork) (bool, error) {
	bound, err := w.Dispatcher.Store.GetSessionDevice(ctx, item.TenantID, item.SessionID)
	if err == nil {
		return w.ready(bound.ID), nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	devices, err := w.Dispatcher.Store.ListExecutionDevices(ctx, item.TenantID)
	if err != nil {
		return false, err
	}
	for _, device := range devices {
		if !w.ready(device.ID) {
			continue
		}
		err := w.Dispatcher.Store.BindSessionDevice(ctx, item.TenantID, item.SessionID, device.ID)
		if errors.Is(err, store.ErrDeviceBindingConflict) {
			_, err = w.Dispatcher.Store.TransitionTurn(ctx, item.TenantID, item.SessionID, item.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnFailed, Outcome: json.RawMessage(`{"error_code":"execution_device_unavailable"}`)})
			if errors.Is(err, store.ErrTurnConflict) {
				err = nil
			}
			return false, err
		}
		return err == nil, err
	}
	return false, nil
}

func (w *Worker) ready(deviceID string) bool {
	peer, err := w.Dispatcher.Registry.LookupDevice(deviceID)
	if err != nil {
		return false
	}
	info, found, known := peer.AgentKindStatus("codex")
	return found && known && info.Available && info.Capabilities.Streaming && info.Capabilities.Steering && info.Capabilities.DurableTurns && info.Capabilities.EnvironmentNone && info.Capabilities.WebSearchControl
}

func (w *Worker) runClaim(ctx context.Context, item store.ExecutionWork) error {
	_, err := w.Dispatcher.Run(ctx, item.TenantID, item.SessionID, item.TurnID)
	if err == nil || errors.Is(err, store.ErrTurnConflict) {
		return nil
	}
	log.Ctx(ctx).Error("agents-api dispatch did not complete", "turn_id", item.TurnID)
	finish, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	turn, err := w.Dispatcher.Store.GetTurn(finish, item.TenantID, item.SessionID, item.TurnID)
	if err != nil {
		return err
	}
	if turn.Status == store.TurnCompleted || turn.Status == store.TurnFailed || turn.Status == store.TurnCancelled {
		return nil
	}
	_, err = w.Dispatcher.Store.TransitionTurn(finish, item.TenantID, item.SessionID, item.TurnID, store.TurnTransition{ExpectedStatus: turn.Status, Status: store.TurnFailed, Outcome: json.RawMessage(`{"error_code":"execution_unavailable"}`)})
	if errors.Is(err, store.ErrTurnConflict) {
		return nil
	}
	return err
}
