package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
)

func (s *Store) UpdateSessionMetadata(ctx context.Context, tenantID, sessionID string, metadata map[string]string) (Session, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return Session{}, err
	}
	id, err := parseID(sessionID)
	if err != nil {
		return Session{}, err
	}
	encoded, err := encodeSessionMetadata(metadata)
	if err != nil {
		return Session{}, err
	}
	row, err := s.queries.UpdateSessionMetadata(ctx, sqlc.UpdateSessionMetadataParams{TenantID: tenant, ID: id, Metadata: encoded})
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("update session metadata: %w", err)
	}
	session, decodeErr := sessionFromRow(row)
	return s.sessionActivity(ctx, session, decodeErr)
}

func encodeSessionMetadata(metadata map[string]string) ([]byte, error) {
	if metadata == nil {
		metadata = map[string]string{}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("%w: metadata: %v", ErrInvalidInput, err)
	}
	if len(encoded) > 64*1024 {
		return nil, fmt.Errorf("%w: metadata exceeds 64 KiB", ErrInvalidInput)
	}
	return encoded, nil
}
