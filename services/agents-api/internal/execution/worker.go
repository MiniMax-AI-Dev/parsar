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
	dispatcher     *Dispatcher
	admission      *store.Store
	lease          *store.ExecutionLease
	directoryReads chan directoryReadRequest
	fileWrites     chan fileWriteRequest
	stopped        chan struct{}
	stopOnce       sync.Once
	runtimes       *runtimeLifecycle
}

func StartWorker(ctx context.Context, dispatcher *Dispatcher) (*Worker, error) {
	lease, err := dispatcher.Store.AcquireExecutionLease(ctx)
	if err != nil {
		return nil, err
	}
	owned := *dispatcher
	owned.Store = lease.Store()
	worker := &Worker{dispatcher: &owned, admission: dispatcher.Store, lease: lease, directoryReads: make(chan directoryReadRequest), fileWrites: make(chan fileWriteRequest), stopped: make(chan struct{})}
	worker.runtimes, err = newRuntimeLifecycle(owned.Store, owned.Registry, owned.ManagedRuntimes)
	if err != nil {
		_ = lease.Close(context.Background())
		return nil, err
	}
	if err := owned.Store.ReconcileEnvironmentConnections(ctx); err != nil {
		if worker.runtimes != nil {
			worker.runtimes.stop()
		}
		_ = lease.Close(context.Background())
		return nil, err
	}
	if err := worker.reconcile(ctx); err != nil {
		if worker.runtimes != nil {
			worker.runtimes.stop()
		}
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
	if preparedEnvironmentConfiguration(value.Configuration) {
		return w.submitEnvironmentInputs(ctx, value, key, inputs)
	}
	if !canAdmitInputs(value.Engine, value.Configuration) {
		return nil, store.ErrInvalidInput
	}
	if err := validateEngineInputs(value.Engine, inputs); err != nil {
		return nil, err
	}
	return w.admission.SubmitInputs(ctx, tenant, session, key, inputs)
}

// CreateSession validates execution support before reserving or admitting initial work.
func (w *Worker) CreateSession(ctx context.Context, tenant string, input store.CreateSessionInput) (store.Session, error) {
	if err := w.validateCreation(ctx, input); err != nil {
		return store.Session{}, err
	}
	return w.admission.CreateSession(ctx, tenant, input)
}

// CreateSessionStream applies the same execution admission before creating a stream.
func (w *Worker) CreateSessionStream(ctx context.Context, tenant string, input store.CreateSessionInput) (store.SessionCreation, error) {
	if err := w.validateCreation(ctx, input); err != nil {
		return store.SessionCreation{}, err
	}
	return w.admission.CreateSessionStream(ctx, tenant, input)
}

// Run retains queued work across restarts, but never replays an uncertain claim.
func (w *Worker) Run(ctx context.Context) error {
	defer w.stopOnce.Do(func() { close(w.stopped) })
	ctx, cancel := context.WithCancel(ctx)
	var running sync.WaitGroup
	defer func() {
		cancel()
		if w.runtimes != nil {
			w.runtimes.stop()
		}
		running.Wait()
		if w.runtimes != nil {
			// Drain an external provisioning caller before releasing the writer lease.
			w.runtimes.gate <- struct{}{}
			<-w.runtimes.gate
		}
		if w.dispatcher.CloseEnvironmentConnections != nil {
			w.dispatcher.CloseEnvironmentConnections()
		}
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
	lifecycleDone := make(chan error, 1)
	if w.runtimes != nil {
		running.Add(1)
		go func() {
			defer running.Done()
			lifecycleDone <- w.runManagedRuntimes(ctx)
		}()
	}
	type readCompletion struct {
		id      string
		request directoryReadRequest
		result  directoryReadResult
	}
	type writeCompletion struct {
		request fileWriteRequest
		result  fileWriteResult
	}
	writesCompleted := make(chan writeCompletion, 4)
	readsCompleted := make(chan readCompletion, 4)
	reads := 0
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	schedule := workerSchedule{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-lifecycleDone:
			return err
		case request := <-w.fileWrites:
			if request.ctx.Err() != nil || active[request.environment.SessionID] || len(active) == 4 {
				request.result <- fileWriteResult{err: ErrExecutionUnavailable}
				continue
			}
			active[request.environment.SessionID] = true
			running.Add(1)
			go func() {
				defer running.Done()
				writesCompleted <- writeCompletion{request: request, result: w.runFileWrite(ctx, request)}
			}()
		case write := <-writesCompleted:
			delete(active, write.request.environment.SessionID)
			write.request.result <- write.result
		case request := <-w.directoryReads:
			if request.ctx.Err() != nil || reads == 4 || (!active[request.environment.SessionID] && len(active) == 4) {
				request.reply(directoryReadResult{err: ErrExecutionUnavailable})
				continue
			}
			reserved := !active[request.environment.SessionID]
			if reserved {
				active[request.environment.SessionID] = true
			}
			reads++
			running.Add(1)
			go func() {
				defer running.Done()
				result := w.runDirectoryRead(ctx, request, reserved)
				id := ""
				if reserved {
					id = request.environment.SessionID
				}
				readsCompleted <- readCompletion{id: id, request: request, result: result}
			}()
		case read := <-readsCompleted:
			reads--
			if read.id != "" {
				delete(active, read.id)
			}
			read.request.reply(read.result)
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
