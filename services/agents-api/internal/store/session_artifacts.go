package store

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type SessionArtifact struct {
	ID            string
	SessionID     string
	TurnID        string
	EnvironmentID string
	Path          string
	SizeBytes     int64
	CreatedAt     time.Time
}

type ArtifactPage struct {
	Artifacts  []SessionArtifact
	NextCursor string
}

func (s *Store) GetSessionArtifact(ctx context.Context, tenantID, sessionID, artifactID string) (SessionArtifact, error) {
	lookup, err := artifactLookup(tenantID, sessionID, artifactID)
	if err != nil {
		return SessionArtifact{}, err
	}
	row, err := s.queries.GetSessionArtifact(ctx, lookup)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionArtifact{}, ErrNotFound
	}
	return artifactFromRow(row), err
}

func (s *Store) ListSessionArtifacts(ctx context.Context, tenantID, sessionID, environmentID, cursor string, limit int, ascending bool) (ArtifactPage, error) {
	if limit < 1 || limit > 100 {
		return ArtifactPage{}, ErrInvalidInput
	}
	if _, err := s.GetSession(ctx, tenantID, sessionID); err != nil {
		return ArtifactPage{}, err
	}
	tenant, _ := parseID(tenantID)
	session, _ := parseID(sessionID)
	params := sqlc.ListSessionArtifactsParams{TenantID: tenant, SessionID: session, PageLimit: int32(limit + 1), Ascending: ascending, AfterID: pgtype.UUID{Valid: true}}
	if environmentID != "" {
		var err error
		params.EnvironmentID, err = parseID(environmentID)
		if err != nil {
			return ArtifactPage{}, err
		}
	}
	if cursor != "" {
		after, err := s.GetSessionArtifact(ctx, tenantID, sessionID, cursor)
		if err != nil {
			return ArtifactPage{}, err
		}
		params.AfterCreated = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		params.AfterID, _ = parseID(after.ID)
	}
	rows, err := s.queries.ListSessionArtifacts(ctx, params)
	if err != nil {
		return ArtifactPage{}, err
	}
	page := ArtifactPage{Artifacts: make([]SessionArtifact, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		page.NextCursor = uuid.UUID(rows[limit-1].ID.Bytes).String()
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Artifacts = append(page.Artifacts, artifactFromRow(row))
	}
	return page, nil
}

// ReadSessionArtifact keeps an admitted snapshot available across concurrent deletion.
func (s *Store) ReadSessionArtifact(ctx context.Context, tenantID, sessionID, artifactID string, consume func(SessionArtifact, io.Reader) error) error {
	lookup, err := artifactLookup(tenantID, sessionID, artifactID)
	if err != nil {
		return err
	}
	if consume == nil {
		return ErrInvalidInput
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	row, err := s.queries.WithTx(tx).GetSessionArtifact(ctx, lookup)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	objects := tx.LargeObjects()
	body, err := objects.Open(ctx, row.BodyOid.Uint32, pgx.LargeObjectModeRead)
	if err != nil {
		return err
	}
	if err := consume(artifactFromRow(row), body); err != nil {
		return err
	}
	if err := body.Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteSessionArtifact(ctx context.Context, tenantID, sessionID, artifactID string) error {
	lookup, err := artifactLookup(tenantID, sessionID, artifactID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	q := s.queries.WithTx(tx)
	// Use the same lock order as whole-Session deletion and Turn publication.
	locked, err := q.LockSession(ctx, sqlc.LockSessionParams{TenantID: lookup.TenantID, ID: lookup.SessionID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && locked.DeletedAt.Valid {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	oid, err := q.DeleteSessionArtifact(ctx, sqlc.DeleteSessionArtifactParams(lookup))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	objects := tx.LargeObjects()
	if err := objects.Unlink(ctx, oid.Uint32); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func artifactLookup(tenantID, sessionID, artifactID string) (sqlc.GetSessionArtifactParams, error) {
	ids, err := turnLookup(tenantID, sessionID, artifactID)
	return sqlc.GetSessionArtifactParams{TenantID: ids.TenantID, SessionID: ids.SessionID, ID: ids.ID}, err
}

func artifactFromRow(row sqlc.SessionArtifact) SessionArtifact {
	return SessionArtifact{ID: uuid.UUID(row.ID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(),
		TurnID: uuid.UUID(row.TurnID.Bytes).String(), EnvironmentID: uuid.UUID(row.EnvironmentID.Bytes).String(),
		Path: row.Path, SizeBytes: row.SizeBytes, CreatedAt: row.CreatedAt.Time}
}
