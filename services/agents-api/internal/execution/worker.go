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
	dispatcher *Dispatcher
	admission  *store.Store
	lease      *store.ExecutionLease
}

func StartWorker(ctx context.Context, dispatcher *Dispatcher) (*Worker, error) {
	lease, err := dispatcher.Store.AcquireExecutionLease(ctx)
	if err != nil {
		return nil, err
	}
	owned := *dispatcher
	owned.Store = lease.Store()
	worker := &Worker{dispatcher: &owned, admission: dispatcher.Store, lease: lease}
	if err := worker.reconcile(ctx); err != nil {
		_ = lease.Close(context.Background())
		return nil, err
	}
	return worker, nil
}

// CheckOwnership checks the same database lease used for execution writes.
func (w *Worker) CheckOwnership(ctx context.Context) error { return w.lease.Ping(ctx) }

func (w *Worker) SubmitInputs(ctx context.Context, tenant, session, key string, inputs []store.Input) ([]store.InputReceipt, error) {
	value, err := w.admission.GetSession(ctx, tenant, session)
	if err != nil {
		return nil, err
	}
	if !canAdmitInputs(value.Engine, value.Configuration) {
		return nil, store.ErrInvalidInput
	}
	if err := validateEngineInputs(value.Engine, inputs); err != nil {
		return nil, err
	}
	return w.admission.SubmitInputs(ctx, tenant, session, key, inputs)
}

// CreateSession validates execution support before atomically admitting initial work.
func (w *Worker) CreateSession(ctx context.Context, tenant string, input store.CreateSessionInput) (store.Session, error) {
	if !canAdmitInputs(input.Engine, input.Configuration) {
		return store.Session{}, store.ErrInvalidInput
	}
	return w.admission.CreateSession(ctx, tenant, input)
}

// CreateSessionStream applies the same execution admission before creating a stream.
func (w *Worker) CreateSessionStream(ctx context.Context, tenant string, input store.CreateSessionInput) (store.SessionCreation, error) {
	if !canAdmitInputs(input.Engine, input.Configuration) {
		return store.SessionCreation{}, store.ErrInvalidInput
	}
	return w.admission.CreateSessionStream(ctx, tenant, input)
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
	schedule := workerSchedule{}
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
			if _, err := w.dispatcher.Store.ExpireEnvironmentInputs(ctx); err != nil {
				return err
			}
			if len(active) == 4 {
				continue
			}
			devices := w.dispatcher.Registry.Devices()
			if len(devices) == 0 {
				continue
			}
			work, err := schedule.selectWork(ctx, w, devices, active)
			if err != nil {
				return err
			}
			for _, item := range work {
				running.Add(1)
				go func() {
					defer running.Done()
					var err error
					if item.reservationID != "" {
						err = w.runEnvironmentInput(ctx, item)
					} else {
						err = w.runClaim(ctx, item.ExecutionWork)
					}
					completed <- completion{id: item.SessionID, err: err}
				}()
			}
		}
	}
}

func (w *Worker) reconcile(ctx context.Context) error {
	cursor := ""
	for {
		work, err := w.dispatcher.Store.ListExecutionWork(ctx, cursor, []string{store.TurnInProgress, store.TurnWaiting}, nil)
		if err != nil {
			return err
		}
		if len(work) == 0 {
			return nil
		}
		for _, item := range work {
			_, err := w.dispatcher.Store.TransitionTurn(ctx, item.TenantID, item.SessionID, item.TurnID, store.TurnTransition{ExpectedStatus: item.Status, Status: store.TurnFailed, Outcome: json.RawMessage(`{"error_code":"execution_interrupted"}`)})
			if err != nil && !errors.Is(err, store.ErrTurnConflict) {
				return err
			}
			cursor = item.TurnID
		}
	}
}

func (w *Worker) bind(ctx context.Context, item store.ExecutionWork) (bool, error) {
	session, err := w.dispatcher.Store.GetSession(ctx, item.TenantID, item.SessionID)
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

	bound, err := w.dispatcher.Store.GetSessionDevice(ctx, item.TenantID, item.SessionID)
	if err == nil {
		return w.ready(bound.ID, session.Engine, snapshot), nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	devices, err := w.dispatcher.Store.ListExecutionDevices(ctx, item.TenantID)
	if err != nil {
		return false, err
	}
	for _, device := range devices {
		if !w.ready(device.ID, session.Engine, snapshot) {
			continue
		}
		err := w.dispatcher.Store.BindSessionDevice(ctx, item.TenantID, item.SessionID, device.ID)
		if errors.Is(err, store.ErrDeviceBindingConflict) {
			_, err = w.dispatcher.Store.TransitionTurn(ctx, item.TenantID, item.SessionID, item.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnFailed, Outcome: json.RawMessage(`{"error_code":"execution_device_unavailable"}`)})
			if errors.Is(err, store.ErrTurnConflict) {
				err = nil
			}
			return false, err
		}
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

func (w *Worker) runClaim(ctx context.Context, item store.ExecutionWork) error {
	_, err := w.dispatcher.Run(ctx, item.TenantID, item.SessionID, item.TurnID)
	if err == nil || errors.Is(err, store.ErrTurnConflict) {
		return nil
	}
	finish, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	turn, err := w.dispatcher.Store.GetTurn(finish, item.TenantID, item.SessionID, item.TurnID)
	if err != nil {
		return err
	}
	if turn.Status == store.TurnCompleted || turn.Status == store.TurnFailed || turn.Status == store.TurnCancelled {
		return nil
	}
	log.Ctx(ctx).Error("agents-api dispatch did not complete", "turn_id", item.TurnID)
	_, err = w.dispatcher.Store.TransitionTurn(finish, item.TenantID, item.SessionID, item.TurnID, store.TurnTransition{ExpectedStatus: turn.Status, Status: store.TurnFailed, Outcome: json.RawMessage(`{"error_code":"execution_unavailable"}`)})
	if errors.Is(err, store.ErrTurnConflict) {
		return nil
	}
	return err
}
