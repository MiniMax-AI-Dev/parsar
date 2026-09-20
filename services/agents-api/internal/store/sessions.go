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

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
)

var (
	ErrInvalidInput           = errors.New("invalid session input")
	ErrEnvironmentUnavailable = errors.New("environment is no longer available")
	ErrNotFound               = errors.New("session not found")
	ErrIdempotencyConflict    = errors.New("idempotency key was already used with different input")
	enginePattern             = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
)

// Session is a durable execution context, separate from product conversations
// and from live daemon connections. Engine session IDs will be bound at execution.
type Session struct {
	ID                       string
	TenantID                 string
	Creator                  *identity.Subject
	Engine                   string
	Metadata                 map[string]string
	CreatedAt                time.Time
	Configuration            json.RawMessage
	LastTurn                 *Turn
	Usage                    json.RawMessage
	RequiredActions          []v1.FunctionCallAction
	Environment              *Environment
	EnvironmentInputActivity *EnvironmentInputActivity
}

type CreateSessionInput struct {
	InitialFiles    []InitialFile
	Creator         identity.Subject
	CreationRequest json.RawMessage
	Engine          string
	Metadata        map[string]string
	IdempotencyKey  string
	Configuration   json.RawMessage
	InitialInputs   []Input
}

type SessionPage struct {
	Sessions   []Session
	NextCursor string
}

type Store struct {
	queries          *sqlc.Queries
	pool             *pgxpool.Pool
	executionLease   *ExecutionLease
	credentialCipher *credentialcrypto.Cipher
}

func New(pool *pgxpool.Pool) *Store { return &Store{queries: sqlc.New(pool), pool: pool} }

func ValidEngine(engine string) bool { return enginePattern.MatchString(engine) }

// CreateSession uses a project-scoped key to make retries safe, including
// concurrent submissions. Different input or creator with the same key conflicts.
func (s *Store) CreateSession(ctx context.Context, tenantID string, input CreateSessionInput) (Session, error) {
	result, err := s.createSession(ctx, tenantID, input)
	return s.sessionActivity(ctx, result.Session, err)
}

func (s *Store) createSession(ctx context.Context, tenantID string, input CreateSessionInput) (SessionCreation, error) {
	if err := input.Creator.Validate(); err != nil {
		return SessionCreation{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return SessionCreation{}, err
	}
	input.Engine = strings.TrimSpace(input.Engine)
	if !ValidEngine(input.Engine) || strings.TrimSpace(input.IdempotencyKey) == "" || len(input.IdempotencyKey) > 128 {
		return SessionCreation{}, fmt.Errorf("%w: engine and idempotency key are required", ErrInvalidInput)
	}
	if input.Metadata == nil {
		input.Metadata = map[string]string{}
	}
	metadata, err := encodeMetadata(input.Metadata)
	if err != nil {
		return SessionCreation{}, err
	}
	if len(input.Configuration) > 512*1024 {
		return SessionCreation{}, fmt.Errorf("%w: configuration exceeds 512 KiB", ErrInvalidInput)
	}
	configuration, err := canonicalJSONObject(input.Configuration)
	if err != nil {
		return SessionCreation{}, err
	}
	var batch []Input
	var encodedInput json.RawMessage
	if len(input.InitialInputs) > 0 {
		batch, encodedInput, err = validateInitialInputs(input.InitialInputs)
		if err != nil {
			return SessionCreation{}, err
		}
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
		InitialInputs json.RawMessage `json:",omitempty"`
		InitialFiles  []InitialFile   `json:",omitempty"`
	}{input.Engine, input.Metadata, hashConfiguration, encodedInput, input.InitialFiles})
	if err != nil {
		return SessionCreation{}, fmt.Errorf("%w: input: %v", ErrInvalidInput, err)
	}
	creationHash, err := creationRequestHash(input.CreationRequest)
	if err != nil {
		return SessionCreation{}, err
	}
	hash := sha256.Sum256(canonical)
	params := sqlc.CreateSessionParams{
		ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: tenant, Engine: input.Engine,
		Metadata: metadata, IdempotencyKey: input.IdempotencyKey, RequestHash: hex.EncodeToString(hash[:]),
		Configuration: configuration, CreationRequestHash: creationHash,
		CreatorKind: pgtype.Text{String: input.Creator.Kind, Valid: true}, CreatorID: pgtype.Text{String: input.Creator.ID, Valid: true},
	}
	row, environment, err := s.createSessionResources(ctx, tenantID, params, batch, encodedInput, input.InitialFiles)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionCreation{}, ErrIdempotencyConflict
	}
	if err != nil {
		return SessionCreation{}, fmt.Errorf("create session: %w", err)
	}
	session, err := sessionFromRow(row)
	session.Environment = environment
	return SessionCreation{Session: session, Created: row.ID == params.ID, Cursor: row.EventSequence}, err
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
	session, decodeErr := sessionFromRow(row)
	return s.sessionActivity(ctx, session, decodeErr)
}

// ListSessions orders by creation time and ID. The cursor is the last returned
// session ID and must belong to the same tenant; it grants no additional access.
func (s *Store) ListSessions(ctx context.Context, tenantID, cursor string, limit int, ascending bool, agentID *string) (SessionPage, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return SessionPage{}, err
	}
	if limit < 1 || limit > 100 {
		return SessionPage{}, fmt.Errorf("%w: page size must be 1..100", ErrInvalidInput)
	}
	params := sqlc.ListSessionsParams{TenantID: tenant, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: ascending}
	if agentID != nil {
		params.AgentID = pgtype.Text{String: *agentID, Valid: true}
	}
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
		session, err = s.sessionActivity(ctx, session, err)
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
	session := Session{ID: uuid.UUID(row.ID.Bytes).String(), TenantID: uuid.UUID(row.TenantID.Bytes).String(), Engine: row.Engine, CreatedAt: row.CreatedAt.Time, RequiredActions: []v1.FunctionCallAction{}}
	creator, err := sessionCreator(row.CreatorKind, row.CreatorID)
	if err != nil {
		return Session{}, err
	}
	session.Creator = creator
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
