package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

// Environment retains execution ownership; its configuration is an internal snapshot, not a public response.
type Environment struct {
	ID            string
	SessionID     string
	TenantID      string
	Status        string
	CreatedAt     time.Time
	Configuration json.RawMessage
}

func createSessionEnvironment(ctx context.Context, q *sqlc.Queries, session sqlc.Session) error {
	var snapshot struct {
		Environment *struct {
			Type string `json:"type"`
		} `json:"environment"`
	}
	if err := json.Unmarshal(session.Configuration, &snapshot); err != nil {
		return fmt.Errorf("%w: invalid environment configuration", ErrInvalidInput)
	}
	if snapshot.Environment == nil || snapshot.Environment.Type == "none" {
		return nil
	}
	switch snapshot.Environment.Type {
	case "self_hosted", "openai_hosted":
		return q.CreateEnvironment(ctx, sqlc.CreateEnvironmentParams{
			ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, SessionID: session.ID,
		})
	default:
		return fmt.Errorf("%w: unsupported environment type", ErrInvalidInput)
	}
}

func (s *Store) GetEnvironment(ctx context.Context, tenantID, environmentID string) (Environment, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return Environment{}, err
	}
	id, err := parseID(environmentID)
	if err != nil {
		return Environment{}, err
	}
	row, err := s.queries.GetEnvironment(ctx, sqlc.GetEnvironmentParams{TenantID: tenant, ID: id})
	return environmentFromRow(row.Environment, row.TenantID, row.Configuration, err)
}

func (s *Store) GetSessionEnvironment(ctx context.Context, tenantID, sessionID string) (Environment, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return Environment{}, err
	}
	id, err := parseID(sessionID)
	if err != nil {
		return Environment{}, err
	}
	row, err := s.queries.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: tenant, ID: id})
	return environmentFromRow(row.Environment, row.TenantID, row.Configuration, err)
}

func environmentFromRow(row sqlc.Environment, tenant pgtype.UUID, configuration []byte, err error) (Environment, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return Environment{}, ErrNotFound
	}
	if err != nil {
		return Environment{}, fmt.Errorf("get environment: %w", err)
	}
	configuration, err = canonicalJSONObject(configuration)
	if err != nil {
		return Environment{}, fmt.Errorf("decode environment configuration: %w", err)
	}
	return Environment{
		ID: uuid.UUID(row.ID.Bytes).String(), SessionID: uuid.UUID(row.SessionID.Bytes).String(),
		TenantID: uuid.UUID(tenant.Bytes).String(), Status: row.Status,
		CreatedAt: row.CreatedAt.Time, Configuration: configuration,
	}, nil
}
