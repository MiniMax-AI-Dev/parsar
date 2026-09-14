package execution

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type workerSchedule struct {
	turnCursor, environmentCursor string
	nextEnvironmentScan           time.Time
	environmentFirst              bool
}

type scheduledWork struct {
	store.ExecutionWork
	reservationID string
}

func (s *workerSchedule) selectWork(ctx context.Context, w *Worker, devices []string, active map[string]bool) ([]scheduledWork, error) {
	turns, err := w.dispatcher.Store.ListExecutionWork(ctx, s.turnCursor, []string{store.TurnQueued}, devices)
	if err != nil {
		return nil, err
	}
	if len(turns) == 0 {
		s.turnCursor = ""
	}
	var environments []store.EnvironmentInputWork
	if w.dispatcher.EnvironmentConnection != nil && !time.Now().Before(s.nextEnvironmentScan) {
		environments, err = w.dispatcher.Store.ListEnvironmentInputWork(ctx, s.environmentCursor, devices)
		if err != nil {
			return nil, err
		}
		s.nextEnvironmentScan = time.Now().Add(5 * time.Second)
		if len(environments) == 0 {
			s.environmentCursor = ""
		}
	}
	var selected []scheduledWork
	for len(turns)+len(environments) > 0 && len(active) < 4 {
		var item scheduledWork
		if len(environments) > 0 && (s.environmentFirst || len(turns) == 0) {
			value := environments[0]
			environments = environments[1:]
			s.environmentCursor = value.ReservationID
			item = scheduledWork{ExecutionWork: store.ExecutionWork{TenantID: value.TenantID, SessionID: value.SessionID}, reservationID: value.ReservationID}
			s.environmentFirst = false
		} else {
			item.ExecutionWork = turns[0]
			turns = turns[1:]
			s.turnCursor = item.TurnID
			s.environmentFirst = true
		}
		if active[item.SessionID] {
			continue
		}
		if item.reservationID == "" {
			ready, err := w.bind(ctx, item.ExecutionWork)
			if err != nil {
				return nil, err
			}
			if !ready {
				continue
			}
		}
		active[item.SessionID] = true
		selected = append(selected, item)
	}
	return selected, nil
}

func (w *Worker) runEnvironmentInput(ctx context.Context, item scheduledWork) error {
	run, err := w.dispatcher.RunEnvironmentInput(ctx, item.TenantID, item.SessionID, item.reservationID)
	if err == nil {
		return nil
	}
	if run.Reservation.State == store.EnvironmentInputAdmitted {
		return err
	}
	if run.Reservation.State != store.EnvironmentInputPending && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if ctx.Err() == nil && !errors.Is(err, store.ErrNotFound) {
		log.Ctx(ctx).Warn("agents-api environment preparation did not complete", "reservation_id", item.reservationID)
	}
	return nil
}
