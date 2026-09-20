package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

var ErrCoreExecutionClaimed = errors.New("execution is already being observed")

type CoreSessionBinding struct {
	WorkspaceID      string
	ProviderSnapshot []byte `json:"-"`
	ID               string
	SessionID        string
	Request          json.RawMessage
}

type CoreRunBinding struct {
	SessionID      string
	Input          json.RawMessage
	PreviousTurnID string
	BaselineSet    bool
	TurnID         string
	Submitted      bool
	Attempted      bool
	Settled        bool
}

// ClaimCoreRun holds a conversation/Agent lease on a dedicated connection.
func (s *Store) ClaimCoreRun(ctx context.Context, runID string) (context.Context, func(), error) {
	id, err := uuid(runID)
	if err != nil {
		return nil, nil, err
	}
	var config *pgx.ConnConfig
	switch db := s.db.(type) {
	case *pgxpool.Pool:
		config = db.Config().ConnConfig.Copy()
	case *pgx.Conn:
		config = db.Config().Copy()
	default:
		return nil, nil, errors.New("Core execution requires PostgreSQL connection configuration")
	}
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, nil, err
	}
	closeConn := func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		closeConn()
		return nil, nil, err
	}
	q := sqlc.New(tx)
	claimed, err := q.LockProductCoreRun(ctx, id)
	if err != nil {
		closeConn()
		return nil, nil, err
	}
	if !claimed {
		closeConn()
		return nil, nil, ErrCoreExecutionClaimed
	}
	predecessor, err := q.ProductCoreRunHasPredecessor(ctx, id)
	if err != nil {
		closeConn()
		return nil, nil, err
	}
	if predecessor {
		closeConn()
		return nil, nil, ErrCoreExecutionClaimed
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-tick.C:
			}
			checkCtx, checkCancel := context.WithTimeout(leaseCtx, 2*time.Second)
			_, err := tx.Exec(checkCtx, "SELECT 1")
			checkCancel()
			if err != nil {
				cancel()
				return
			}
		}
	}()
	return leaseCtx, func() { cancel(); <-done; closeConn() }, nil
}

func (s *Store) EnsureCoreSession(ctx context.Context, runID string, request json.RawMessage) (CoreSessionBinding, error) {
	id, err := uuid(runID)
	if err != nil {
		return CoreSessionBinding{}, err
	}
	r, err := sqlc.New(s.db).EnsureProductCoreSession(ctx, sqlc.EnsureProductCoreSessionParams{
		ID: mustUUID(newID()), RunID: id, Request: request,
	})
	return CoreSessionBinding{ID: pgUUIDString(r.ID), SessionID: r.CoreSessionID, Request: r.Request}, err
}

func (s *Store) BindCoreSession(ctx context.Context, bindingID, sessionID string) error {
	_, err := sqlc.New(s.db).BindProductCoreSession(ctx, sqlc.BindProductCoreSessionParams{ID: mustUUID(bindingID), CoreSessionID: sessionID})
	return err
}

func (s *Store) EnsureCoreRun(ctx context.Context, runID, bindingID string, input json.RawMessage) error {
	_, err := sqlc.New(s.db).EnsureProductCoreRun(ctx, sqlc.EnsureProductCoreRunParams{RunID: mustUUID(runID), SessionBindingID: mustUUID(bindingID), Input: input})
	return err
}

func (s *Store) GetCoreRun(ctx context.Context, runID string) (CoreRunBinding, error) {
	r, err := sqlc.New(s.db).GetProductCoreRunBinding(ctx, mustUUID(runID))
	return CoreRunBinding{SessionID: r.CoreSessionID, Input: r.Input, PreviousTurnID: r.PreviousTurnID.String, BaselineSet: r.PreviousTurnID.Valid, TurnID: r.CoreTurnID, Submitted: r.Submitted, Attempted: r.Attempted, Settled: r.Settled}, err
}

func (s *Store) SetCoreRunBaseline(ctx context.Context, runID, previousTurnID string) error {
	_, err := sqlc.New(s.db).SetProductCoreRunBaseline(ctx, sqlc.SetProductCoreRunBaselineParams{RunID: mustUUID(runID), PreviousTurnID: previousTurnID})
	return err
}

func (s *Store) MarkCoreRunSubmitted(ctx context.Context, runID string) error {
	return sqlc.New(s.db).MarkProductCoreRunSubmitted(ctx, mustUUID(runID))
}

func (s *Store) MarkCoreRunAttempted(ctx context.Context, runID string) error {
	return sqlc.New(s.db).MarkProductCoreRunAttempted(ctx, mustUUID(runID))
}

func (s *Store) BindCoreTurn(ctx context.Context, runID, turnID string) error {
	_, err := sqlc.New(s.db).BindProductCoreTurn(ctx, sqlc.BindProductCoreTurnParams{RunID: mustUUID(runID), CoreTurnID: turnID})
	return err
}

func (s *Store) SettleCoreRun(ctx context.Context, runID string) error {
	return sqlc.New(s.db).SettleProductCoreRun(ctx, mustUUID(runID))
}

func (s *Store) ListRecoverableCoreRuns(ctx context.Context) ([]StreamingDispatchInput, error) {
	rows, err := sqlc.New(s.db).ListRecoverableProductCoreRuns(ctx)
	if err != nil {
		return nil, err
	}
	runs := make([]StreamingDispatchInput, 0, len(rows))
	for _, r := range rows {
		runs = append(runs, StreamingDispatchInput{RunID: r.ID, ConversationID: r.ConversationID, ConnectorType: "agents_api"})
	}
	return runs, nil
}

// GetCoreSession retrieves already-frozen execution without consulting mutable Agent settings.
func (s *Store) GetCoreSession(ctx context.Context, runID string) (CoreSessionBinding, error) {
	r, err := sqlc.New(s.db).GetCatalogCoreSession(ctx, mustUUID(runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return CoreSessionBinding{}, ErrUnknownAgentRun
	}
	return CoreSessionBinding{ID: r.ID, WorkspaceID: r.WorkspaceID, SessionID: r.CoreSessionID, Request: r.Request, ProviderSnapshot: r.ProviderSnapshot}, err
}

// GetCoreExecutionStatus is an internal lifecycle read, never a product visibility check.
func (s *Store) GetCoreExecutionStatus(ctx context.Context, runID string) (string, error) {
	id, err := uuid(runID)
	if err != nil {
		return "", err
	}
	return sqlc.New(s.db).GetProductCoreExecutionStatus(ctx, id)
}

// ListCoreExecutionEvents restores a frozen execution even after its conversation is deleted.
func (s *Store) ListCoreExecutionEvents(ctx context.Context, runID string, after int64) ([]AgentRunEventRead, error) {
	if _, err := s.GetCoreExecutionStatus(ctx, runID); err != nil {
		return nil, err
	}
	rows, err := sqlc.New(s.db).ListAgentRunEventsByRun(ctx, sqlc.ListAgentRunEventsByRunParams{AgentRunID: mustUUID(runID), AfterSequence: after})
	if err != nil {
		return nil, err
	}
	out := make([]AgentRunEventRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, agentRunEventFromRow(row.ID, row.WorkspaceID, row.AgentRunID, row.Sequence, row.EventKind, row.Payload, row.OccurredAt, row.CreatedAt))
	}
	return out, nil
}
