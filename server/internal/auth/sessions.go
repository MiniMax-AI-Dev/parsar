package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// DefaultSessionTTL is the lifetime of a freshly-minted session. No
// sliding window — TouchSession only updates last_seen_at, not the
// expiry.
const DefaultSessionTTL = 30 * 24 * time.Hour

// SessionInfo is the projection the middleware needs after resolving
// the cookie.
type SessionInfo struct {
	ID         string
	UserID     string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

type SessionStore interface {
	Create(ctx context.Context, in CreateSessionInput) (string, error)
	Resolve(ctx context.Context, sessionID string, now time.Time) (SessionInfo, error)
	Revoke(ctx context.Context, sessionID string, now time.Time) error
}

type CreateSessionInput struct {
	UserID    string
	UserAgent string
	IP        string
	TTL       time.Duration // 0 = DefaultSessionTTL
}

// Querier is the subset of sqlc.Queries the store uses, factored out
// so production and tests share the same surface.
type Querier interface {
	CreateSession(ctx context.Context, arg sqlc.CreateSessionParams) error
	GetActiveSession(ctx context.Context, arg sqlc.GetActiveSessionParams) (sqlc.GetActiveSessionRow, error)
	TouchSession(ctx context.Context, arg sqlc.TouchSessionParams) error
	RevokeSession(ctx context.Context, arg sqlc.RevokeSessionParams) error
}

type PostgresSessionStore struct {
	q Querier
}

func NewPostgresSessionStore(q Querier) *PostgresSessionStore {
	return &PostgresSessionStore{q: q}
}

// Create mints a session token, persists the row, and returns the
// token. Callers MUST set the returned value as the HttpOnly cookie
// verbatim.
func (s *PostgresSessionStore) Create(ctx context.Context, in CreateSessionInput) (string, error) {
	if in.UserID == "" {
		return "", errors.New("auth: CreateSessionInput.UserID is required")
	}
	ttl := in.TTL
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	id, err := NewSessionID()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	uid, err := parseUUID(in.UserID)
	if err != nil {
		return "", fmt.Errorf("auth: invalid user_id: %w", err)
	}
	if err := s.q.CreateSession(ctx, sqlc.CreateSessionParams{
		ID:        id,
		UserID:    uid,
		Now:       timestamp(now),
		ExpiresAt: timestamp(now.Add(ttl)),
		UserAgent: in.UserAgent,
		Ip:        in.IP,
	}); err != nil {
		return "", fmt.Errorf("auth: persist session: %w", err)
	}
	return id, nil
}

// Resolve returns the session projection. ErrInvalidSession is
// returned for missing / expired / revoked rows so the caller can
// map directly to 401.
func (s *PostgresSessionStore) Resolve(ctx context.Context, sessionID string, now time.Time) (SessionInfo, error) {
	if sessionID == "" {
		return SessionInfo{}, ErrInvalidSession
	}
	row, err := s.q.GetActiveSession(ctx, sqlc.GetActiveSessionParams{
		ID:  sessionID,
		Now: timestamp(now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SessionInfo{}, ErrInvalidSession
		}
		return SessionInfo{}, fmt.Errorf("auth: lookup session: %w", err)
	}
	// Touch is best-effort: a failed last_seen_at update must not
	// fail the request.
	_ = s.q.TouchSession(ctx, sqlc.TouchSessionParams{
		ID:  sessionID,
		Now: timestamp(now),
	})
	return SessionInfo{
		ID:         row.ID,
		UserID:     row.UserID,
		CreatedAt:  row.CreatedAt.Time,
		LastSeenAt: row.LastSeenAt.Time,
		ExpiresAt:  row.ExpiresAt.Time,
	}, nil
}

// Revoke marks a session logged out. Idempotent.
func (s *PostgresSessionStore) Revoke(ctx context.Context, sessionID string, now time.Time) error {
	if sessionID == "" {
		return nil
	}
	if err := s.q.RevokeSession(ctx, sqlc.RevokeSessionParams{
		ID:  sessionID,
		Now: timestamp(now),
	}); err != nil {
		return fmt.Errorf("auth: revoke session: %w", err)
	}
	return nil
}

func timestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return u, err
	}
	return u, nil
}
