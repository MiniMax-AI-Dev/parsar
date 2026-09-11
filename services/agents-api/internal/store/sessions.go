// Package store persists execution state independently of the Parsar product.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

var (
	ErrInvalidInput        = errors.New("invalid session input")
	ErrNotFound            = errors.New("session not found")
	ErrIdempotencyConflict = errors.New("idempotency key was already used with different input")
	enginePattern          = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
)

// Session is a durable execution context, separate from product conversations
// and from live daemon connections. Engine session IDs will be bound at execution.
type Session struct {
	ID            string
	TenantID      string
	Engine        string
	Metadata      map[string]string
	CreatedAt     time.Time
	Configuration json.RawMessage
}

type CreateSessionInput struct {
	Engine         string
	Metadata       map[string]string
	IdempotencyKey string
	Configuration  json.RawMessage
}

type SessionPage struct {
	Sessions   []Session
	NextCursor string
}

type Store struct {
	queries *sqlc.Queries
	pool    *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{queries: sqlc.New(pool), pool: pool} }

func ValidEngine(engine string) bool { return enginePattern.MatchString(engine) }

// CreateSession uses a caller-scoped key to make retries safe, including
// concurrent submissions. Different input with the same key is a conflict.
func (s *Store) CreateSession(ctx context.Context, tenantID string, input CreateSessionInput) (Session, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return Session{}, err
	}
	input.Engine = strings.TrimSpace(input.Engine)
	if !ValidEngine(input.Engine) || strings.TrimSpace(input.IdempotencyKey) == "" || len(input.IdempotencyKey) > 128 {
		return Session{}, fmt.Errorf("%w: engine and idempotency key are required", ErrInvalidInput)
	}
	if input.Metadata == nil {
		input.Metadata = map[string]string{}
	}
	metadata, err := json.Marshal(input.Metadata)
	if err != nil {
		return Session{}, fmt.Errorf("%w: metadata: %v", ErrInvalidInput, err)
	}
	if len(metadata) > 64*1024 {
		return Session{}, fmt.Errorf("%w: metadata exceeds 64 KiB", ErrInvalidInput)
	}
	if len(input.Configuration) > 512*1024 {
		return Session{}, fmt.Errorf("%w: configuration exceeds 512 KiB", ErrInvalidInput)
	}
	configuration, err := canonicalJSONObject(input.Configuration)
	if err != nil {
		return Session{}, err
	}
	hashConfiguration := configuration
	// Empty configuration retains the idempotency hashes from the first schema.
	if string(configuration) == "{}" {
		hashConfiguration = nil
	}
	// JSON map keys are sorted by encoding/json, so key order does not affect retries.
	canonical, err := json.Marshal(struct {
		Engine        string
		Metadata      map[string]string
		Configuration json.RawMessage `json:",omitempty"`
	}{input.Engine, input.Metadata, hashConfiguration})
	if err != nil {
		return Session{}, fmt.Errorf("%w: input: %v", ErrInvalidInput, err)
	}
	hash := sha256.Sum256(canonical)
	row, err := s.queries.CreateSession(ctx, sqlc.CreateSessionParams{
		ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: tenant, Engine: input.Engine,
		Metadata: metadata, IdempotencyKey: input.IdempotencyKey, RequestHash: hex.EncodeToString(hash[:]),
		Configuration: configuration,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrIdempotencyConflict
	}
	if err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}
	return sessionFromRow(row)
}

// GetSession always scopes lookup to the authenticated caller's tenant.
func (s *Store) GetSession(ctx context.Context, tenantID, sessionID string) (Session, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return Session{}, err
	}
	id, err := parseID(sessionID)
	if err != nil {
		return Session{}, err
	}
	row, err := s.queries.GetSession(ctx, sqlc.GetSessionParams{TenantID: tenant, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("get session: %w", err)
	}
	return sessionFromRow(row)
}

// ListSessions orders by creation time and ID. The cursor is the last returned
// session ID and must belong to the same tenant; it grants no additional access.
func (s *Store) ListSessions(ctx context.Context, tenantID, cursor string, limit int, ascending bool) (SessionPage, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return SessionPage{}, err
	}
	if limit < 1 || limit > 100 {
		return SessionPage{}, fmt.Errorf("%w: page size must be 1..100", ErrInvalidInput)
	}
	params := sqlc.ListSessionsParams{TenantID: tenant, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: ascending}
	if cursor != "" {
		after, err := s.GetSession(ctx, tenantID, cursor)
		if err != nil {
			return SessionPage{}, err
		}
		params.AfterCreated = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		params.AfterID, _ = parseID(after.ID)
	}
	rows, err := s.queries.ListSessions(ctx, params)
	if err != nil {
		return SessionPage{}, fmt.Errorf("list sessions: %w", err)
	}
	page := SessionPage{Sessions: make([]Session, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		page.NextCursor = uuid.UUID(rows[limit-1].ID.Bytes).String()
		rows = rows[:limit]
	}
	for _, row := range rows {
		session, err := sessionFromRow(row)
		if err != nil {
			return SessionPage{}, err
		}
		page.Sessions = append(page.Sessions, session)
	}
	return page, nil
}

func parseID(value string) (pgtype.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return pgtype.UUID{}, fmt.Errorf("%w: nonzero UUID required", ErrInvalidInput)
	}
	return pgtype.UUID{Bytes: id, Valid: true}, nil
}

func sessionFromRow(row sqlc.Session) (Session, error) {
	session := Session{ID: uuid.UUID(row.ID.Bytes).String(), TenantID: uuid.UUID(row.TenantID.Bytes).String(), Engine: row.Engine, CreatedAt: row.CreatedAt.Time}
	configuration, err := canonicalJSONObject(row.Configuration)
	if err != nil {
		return Session{}, fmt.Errorf("decode session configuration: %w", err)
	}
	session.Configuration = configuration
	if err := json.Unmarshal(row.Metadata, &session.Metadata); err != nil {
		return Session{}, fmt.Errorf("decode session metadata: %w", err)
	}
	return session, nil
}
