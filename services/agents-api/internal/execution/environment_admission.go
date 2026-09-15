package execution

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

var (
	ErrEnvironmentInputExpired   = errors.New("environment input expired before admission")
	ErrEnvironmentInputCancelled = errors.New("environment input cancelled before admission")
	ErrExecutionUnavailable      = errors.New("execution ownership is unavailable")
)

func selfHostedConfiguration(configuration json.RawMessage) bool {
	var snapshot Snapshot
	return json.Unmarshal(configuration, &snapshot) == nil && snapshot.Environment != nil && snapshot.Environment.Type == "self_hosted"
}

func (w *Worker) validateCreation(ctx context.Context, input store.CreateSessionInput) error {
	if selfHostedConfiguration(input.Configuration) {
		if w.dispatcher.EnvironmentConnection == nil {
			return store.ErrInvalidInput
		}
		if err := ValidateSessionConfiguration(input.Engine, input.Configuration); err != nil {
			return err
		}
		if len(input.InitialInputs) > 0 {
			return w.checkAdmissionOwnership(ctx)
		}
		return nil
	}
	if !canAdmitInputs(input.Engine, input.Configuration) {
		return store.ErrInvalidInput
	}
	return nil
}

func (w *Worker) submitEnvironmentInputs(ctx context.Context, session store.Session, key string, inputs []store.Input) ([]store.InputReceipt, error) {
	if w.dispatcher.EnvironmentConnection == nil || ValidateSessionConfiguration(session.Engine, session.Configuration) != nil {
		return nil, store.ErrInvalidInput
	}
	if err := w.checkAdmissionOwnership(ctx); err != nil {
		return nil, err
	}
	reserve, cancel := context.WithTimeout(ctx, 5*time.Second)
	reservation, err := w.admission.ReserveEnvironmentInput(reserve, session.TenantID, session.ID, key, inputs)
	cancel()
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		switch reservation.State {
		case store.EnvironmentInputAdmitted:
			return reservation.Receipts, nil
		case store.EnvironmentInputExpired:
			return nil, ErrEnvironmentInputExpired
		case store.EnvironmentInputCancelled:
			return nil, ErrEnvironmentInputCancelled
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
		if err := w.checkAdmissionOwnership(ctx); err != nil {
			return nil, err
		}
		reservation, err = w.environmentInputOutcome(ctx, session, reservation)
		if err != nil {
			return nil, err
		}
	}
}

func (w *Worker) environmentInputOutcome(ctx context.Context, session store.Session, reservation store.EnvironmentInputReservation) (store.EnvironmentInputReservation, error) {
	read, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// The database rechecks its clock under the Session lock before settlement.
	if !time.Now().Before(reservation.Deadline) {
		return w.admission.ExpireEnvironmentInput(read, session.TenantID, session.ID, reservation.ID)
	}
	return w.admission.GetEnvironmentInputReservation(read, session.TenantID, session.ID, reservation.ID)
}

func (w *Worker) checkAdmissionOwnership(ctx context.Context) error {
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := w.CheckOwnership(check); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrExecutionUnavailable
	}
	return nil
}
