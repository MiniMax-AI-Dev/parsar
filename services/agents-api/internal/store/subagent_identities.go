package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// SubagentIdentity is an internal execution binding, not a public Subagent resource.
type SubagentIdentity struct {
	ID, SessionID, NativeID, ParentNativeID string
	NativeCreatedAt                         int64
	FirstTurnID                             string
	FirstEventOrdinal                       int32
	FirstObservedAt                         time.Time
}

func projectSubagentIdentity(ctx context.Context, q *sqlc.Queries, session, turn pgtype.UUID, ordinal int32, raw json.RawMessage) error {
	var identity proto.SubagentIdentityPayload
	if json.Unmarshal(raw, &identity) != nil || identity.NativeCreatedAt <= 0 || identity.NativeID == identity.ParentNativeID {
		return ErrInvalidInput
	}
	for _, value := range []string{identity.NativeID, identity.ParentNativeID, identity.ParentTurnID, identity.SourceItemID} {
		if value == "" || len(value) > 512 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
			return ErrInvalidInput
		}
	}
	_, err := q.PutSubagentIdentity(ctx, sqlc.PutSubagentIdentityParams{
		ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, SessionID: session,
		NativeID: identity.NativeID, ParentNativeID: identity.ParentNativeID,
		NativeCreatedAt: identity.NativeCreatedAt, FirstTurnID: turn, FirstEventOrdinal: ordinal,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrIdempotencyConflict
	}
	return err
}

// GetSubagentIdentity recovers a binding in its authorized, visible Session.
func (s *Store) GetSubagentIdentity(ctx context.Context, tenantID, sessionID, nativeID string) (SubagentIdentity, error) {
	p, err := deviceLookup(tenantID, sessionID)
	if err != nil {
		return SubagentIdentity{}, err
	}
	row, err := s.queries.GetSubagentIdentity(ctx, sqlc.GetSubagentIdentityParams{TenantID: p.TenantID, SessionID: p.ID, NativeID: nativeID})
	if errors.Is(err, pgx.ErrNoRows) {
		return SubagentIdentity{}, ErrNotFound
	}
	if err != nil {
		return SubagentIdentity{}, err
	}
	return SubagentIdentity{ID: uuid.UUID(row.ID.Bytes).String(), SessionID: sessionID,
		NativeID: row.NativeID, ParentNativeID: row.ParentNativeID, NativeCreatedAt: row.NativeCreatedAt,
		FirstTurnID: uuid.UUID(row.FirstTurnID.Bytes).String(), FirstEventOrdinal: row.FirstEventOrdinal,
		FirstObservedAt: row.FirstObservedAt.Time}, nil
}
