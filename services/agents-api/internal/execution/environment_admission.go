package execution

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

var (
	ErrEnvironmentInputExpired   = errors.New("environment input expired before admission")
	ErrEnvironmentInputCancelled = errors.New("environment input cancelled before admission")
	ErrExecutionUnavailable      = errors.New("execution ownership is unavailable")
)

func preparedEnvironmentConfiguration(configuration json.RawMessage) bool {
	var snapshot Snapshot
	return json.Unmarshal(configuration, &snapshot) == nil && snapshot.Environment != nil &&
		(snapshot.Environment.Type == "self_hosted" || snapshot.Environment.Type == "openai_hosted")
}

func (w *Worker) validateEnvironmentAdmission(engine string, configuration json.RawMessage) error {
	var snapshot Snapshot
	if json.Unmarshal(configuration, &snapshot) != nil || snapshot.Environment == nil {
		return store.ErrInvalidInput
	}
	switch snapshot.Environment.Type {
	case "self_hosted":
		if w.dispatcher.EnvironmentConnection == nil {
			return store.ErrInvalidInput
		}
	case "openai_hosted":
		if w.runtimes == nil {
			return ErrExecutionUnavailable
		}
	default:
		return store.ErrInvalidInput
	}
	return w.dispatcher.ValidateSessionConfiguration(engine, configuration)
}

func (w *Worker) validateCreation(ctx context.Context, input store.CreateSessionInput) error {
	if preparedEnvironmentConfiguration(input.Configuration) {
		if err := w.validateEnvironmentAdmission(input.Engine, input.Configuration); err != nil {
			return err
		}
		var snapshot Snapshot
		if err := json.Unmarshal(input.Configuration, &snapshot); err != nil {
			return store.ErrInvalidInput
		}
		if snapshot.Environment.Type == "openai_hosted" && w.runtimes.config.DefaultProvider == "" {
			return ErrExecutionUnavailable
		}
		if len(input.InitialInputs) == 0 && snapshot.Environment.Type == "self_hosted" {
			return nil
		}
		return w.checkAdmissionOwnership(ctx)
	}
	if !w.dispatcher.canAdmitInputs(input.Engine, input.Configuration) {
		return store.ErrInvalidInput
	}
	return nil
}

func (w *Worker) submitEnvironmentInputs(ctx context.Context, session store.Session, key string, inputs []store.Input) ([]store.InputReceipt, error) {
	if err := w.validateEnvironmentAdmission(session.Engine, session.Configuration); err != nil {
		return nil, err
	}
	if err := w.checkAdmissionOwnership(ctx); err != nil {
		return nil, err
	}
	kind := ""
	if len(inputs) > 0 {
		kind = inputs[0].Kind
	}
	if (kind == "cancel" || kind == "tool_result") && !slices.ContainsFunc(inputs, func(input store.Input) bool { return input.Kind != kind }) {
		// Neither kind creates a Turn. The Session lock preserves target and retry identity.
		return w.admission.SubmitInputs(ctx, session.TenantID, session.ID, key, inputs)
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
		case store.EnvironmentInputFailed:
			return nil, store.ErrEnvironmentUnavailable
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
