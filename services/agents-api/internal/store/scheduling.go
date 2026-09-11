package store

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ExecutionWork struct{ TenantID, SessionID, TurnID, Status string }

type ExecutionLease struct{ conn *pgxpool.Conn }

// AcquireExecutionLease enforces the gateway's single-service ownership per database.
func (s *Store) AcquireExecutionLease(ctx context.Context) (*ExecutionLease, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	acquired, err := sqlc.New(conn).TryExecutionLease(ctx)
	if err != nil || !acquired {
		_ = conn.Hijack().Close(context.Background())
		if err != nil {
			return nil, err
		}
		return nil, errors.New("another execution service owns this database")
	}
	return &ExecutionLease{conn: conn}, nil
}

func (l *ExecutionLease) Ping(ctx context.Context) error  { return l.conn.Ping(ctx) }
func (l *ExecutionLease) Close(ctx context.Context) error { return l.conn.Hijack().Close(ctx) }

func (s *Store) ListExecutionWork(ctx context.Context, after string, statuses []string, connectedDevices []string) ([]ExecutionWork, error) {
	id := pgtype.UUID{Valid: true}
	var err error
	if after != "" {
		id, err = parseID(after)
		if err != nil {
			return nil, err
		}
	}
	devices := make([]pgtype.UUID, 0, len(connectedDevices))
	for _, value := range connectedDevices {
		device, err := parseID(value)
		if err != nil {
			return nil, err
		}
		devices = append(devices, device)
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
	row, err := s.queries.GetLatestSessionTurn(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return session, nil
	}
	if err != nil {
		return Session{}, err
	}
	turn := turnFromRow(row)
	session.LastTurn = &turn
	session.Usage, err = s.queries.SessionTokenUsage(ctx, id)
	return session, err
}
