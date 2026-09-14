package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type ExecutionWork struct{ TenantID, SessionID, TurnID, Status string }

type EnvironmentInputWork struct{ TenantID, SessionID, ReservationID string }

func executionWorkCursor(after string, connectedDevices []string) (pgtype.UUID, []pgtype.UUID, error) {
	id := pgtype.UUID{Valid: true}
	var err error
	if after != "" {
		id, err = parseID(after)
		if err != nil {
			return id, nil, err
		}
	}
	devices := make([]pgtype.UUID, 0, len(connectedDevices))
	for _, value := range connectedDevices {
		device, err := parseID(value)
		if err != nil {
			return id, nil, err
		}
		devices = append(devices, device)
	}
	return id, devices, nil
}

func (s *Store) ListEnvironmentInputWork(ctx context.Context, after string, connectedDevices []string) ([]EnvironmentInputWork, error) {
	id, devices, err := executionWorkCursor(after, connectedDevices)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListEnvironmentInputWork(ctx, sqlc.ListEnvironmentInputWorkParams{AfterID: id, ConnectedDevices: devices})
	if err != nil {
		return nil, err
	}
	work := make([]EnvironmentInputWork, 0, len(rows))
	for _, row := range rows {
		work = append(work, EnvironmentInputWork{TenantID: uuid.UUID(row.TenantID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(), ReservationID: uuid.UUID(row.ID.Bytes).String()})
	}
	return work, nil
}

func (s *Store) ListExecutionWork(ctx context.Context, after string, statuses []string, connectedDevices []string) ([]ExecutionWork, error) {
	id, devices, err := executionWorkCursor(after, connectedDevices)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListExecutionWork(ctx, sqlc.ListExecutionWorkParams{AfterID: id, Statuses: statuses, ConnectedOnly: connectedDevices != nil, ConnectedDevices: devices})
	if err != nil {
		return nil, err
	}
	work := make([]ExecutionWork, 0, len(rows))
	for _, row := range rows {
		work = append(work, ExecutionWork{TenantID: uuid.UUID(row.TenantID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(), TurnID: uuid.UUID(row.ID.Bytes).String(), Status: row.Status})
	}
	return work, nil
}

func (s *Store) ListExecutionDevices(ctx context.Context, tenantID string) ([]ExecutionDevice, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListExecutionDevices(ctx, tenant)
	if err != nil {
		return nil, err
	}
	devices := make([]ExecutionDevice, 0, len(rows))
	for _, row := range rows {
		devices = append(devices, ExecutionDevice{ID: uuid.UUID(row.ID.Bytes).String(), Name: row.Name})
	}
	return devices, nil
}

func (s *Store) sessionActivity(ctx context.Context, session Session, err error) (Session, error) {
	if err != nil {
		return Session{}, err
	}
	id, _ := parseID(session.ID)
	tenant, _ := parseID(session.TenantID)
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		environment, err := q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: tenant, ID: id})
		if err == nil {
			value, err := environmentFromRow(environment.Environment, environment.TenantID, environment.Configuration, nil)
			if err != nil {
				return err
			}
			session.Environment = &value
			session.EnvironmentInputActivity, err = environmentInputActivity(ctx, q, id)
			if err != nil {
				return err
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		row, err := q.GetLatestSessionTurn(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		turn := turnFromRow(row)
		session.LastTurn = &turn
		session.RequiredActions, err = functionActions(ctx, q, row)
		if err != nil {
			return err
		}
		session.Usage, err = q.SessionTokenUsage(ctx, id)
		return err
	})
	return session, err
}
