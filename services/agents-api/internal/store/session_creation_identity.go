package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func creationRequestHash(raw json.RawMessage) (pgtype.Text, error) {
	if len(raw) == 0 {
		return pgtype.Text{}, nil
	}
	if len(raw) > 1024*1024 {
		return pgtype.Text{}, ErrInvalidInput
	}
	canonical, err := canonicalJSONObject(raw)
	if err != nil {
		return pgtype.Text{}, err
	}
	hash := sha256.Sum256(canonical)
	return pgtype.Text{String: hex.EncodeToString(hash[:]), Valid: true}, nil
}

// FindSessionCreation recovers recorded caller intent without resolving a mutable source.
func (s *Store) FindSessionCreation(ctx context.Context, tenantID, key string, request json.RawMessage) (SessionCreation, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return SessionCreation{}, err
	}
	if strings.TrimSpace(key) == "" || len(key) > 128 {
		return SessionCreation{}, ErrInvalidInput
	}
	hash, err := creationRequestHash(request)
	if err != nil {
		return SessionCreation{}, err
	}
	if !hash.Valid {
		return SessionCreation{}, ErrInvalidInput
	}
	row, err := s.queries.FindSessionCreation(ctx, sqlc.FindSessionCreationParams{TenantID: tenant, IdempotencyKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionCreation{}, ErrNotFound
	}
	if err != nil {
		return SessionCreation{}, fmt.Errorf("find session creation: %w", err)
	}
	if row.CreationRequestHash.String != hash.String {
		return SessionCreation{}, ErrIdempotencyConflict
	}
	session, err := sessionFromRow(row)
	// The row and cursor share one committed snapshot; later events remain observable.
	return SessionCreation{Session: session, Cursor: row.EventSequence}, err
}
