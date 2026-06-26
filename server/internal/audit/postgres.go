package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// PostgresSink writes audit events into audit_records via the
// sqlc-generated InsertAuditRecord query. Open-source default Sink;
// internal deployments can register a different one.
type PostgresSink struct {
	queries *sqlc.Queries
}

func NewPostgresSink(queries *sqlc.Queries) *PostgresSink {
	if queries == nil {
		panic("audit.NewPostgresSink: queries is nil")
	}
	return &PostgresSink{queries: queries}
}

func (s *PostgresSink) Write(ctx context.Context, ev Event) error {
	payload, err := encodePayload(ev.Payload)
	if err != nil {
		return fmt.Errorf("audit: encode payload: %w", err)
	}
	return s.queries.InsertAuditRecord(ctx, sqlc.InsertAuditRecordParams{
		OccurredAt:  pgtype.Timestamptz{Time: ev.OccurredAt, Valid: true},
		Source:      ev.Source,
		EventType:   ev.EventType,
		ActorType:   ev.ActorType,
		ActorID:     pgUUID(ev.ActorID),
		TargetType:  ev.TargetType,
		TargetID:    pgUUID(ev.TargetID),
		WorkspaceID: pgUUID(ev.WorkspaceID),
		ProjectID:   pgUUID(ev.ProjectID),
		Payload:     payload,
	})
}

func encodePayload(payload map[string]any) ([]byte, error) {
	if len(payload) == 0 {
		return []byte("{}"), nil
	}
	return json.Marshal(payload)
}

// pgUUID translates an empty string to SQL NULL. Invalid UUID syntax
// degrades to NULL rather than panicking — audit emit must never fail
// business code.
func pgUUID(value string) pgtype.UUID {
	if value == "" {
		return pgtype.UUID{}
	}
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		return pgtype.UUID{}
	}
	return id
}
