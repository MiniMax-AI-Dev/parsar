package store

// Bearer-only resolution for /api/v1/agent-runtime/* endpoints. The
// parsar CLI presents a long-lived runner credential without a URL-side
// runtime id; hash the plaintext, look the row up by config jsonb,
// return a narrow identity struct the middleware injects into ctx.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	sqlc "github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// RuntimeIdentity is the slim view the runner_credential middleware
// hands to /api/v1/agent-runtime/* handlers. Optional pointers come
// from runtime.config keys the sandbox provider wrote at acquire time;
// nil means "absent" (vs. empty string).
type RuntimeIdentity struct {
	RuntimeID      string
	WorkspaceID    string
	RuntimeType    string
	OwnerUserID    *string
	AgentID        *string
	ConnectorName  *string
	ConversationID *string
}

// ResolveRuntimeIdentity hashes the supplied plaintext bearer and
// returns the matching active runtime as an identity struct. Plaintext
// == "" and "no matching row" both collapse to (zero, false, nil) so
// the middleware emits a single generic 401.
//
// Constant-time comparison is unnecessary because the column already
// holds a SHA-256 hash — any timing leakage exposes positions in the
// hash, not in the plaintext token.
func (s *Store) ResolveRuntimeIdentity(ctx context.Context, plaintext string) (RuntimeIdentity, bool, error) {
	if plaintext == "" {
		return RuntimeIdentity{}, false, nil
	}
	hash := HashRuntimeCredential(plaintext)
	row, err := sqlc.New(s.db).GetRuntimeByCredentialHash(ctx, hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RuntimeIdentity{}, false, nil
		}
		return RuntimeIdentity{}, false, fmt.Errorf("runtime identity: query: %w", err)
	}
	cfg := unmarshalJSONOrEmpty(row.Config)
	return RuntimeIdentity{
		RuntimeID:      row.ID,
		WorkspaceID:    row.WorkspaceID,
		RuntimeType:    row.Type,
		OwnerUserID:    nullableUUIDString(row.OwnerUserID),
		AgentID:        configString(cfg, "agent_id"),
		ConnectorName:  configString(cfg, "connector"),
		ConversationID: configString(cfg, "conversation_id"),
	}, true, nil
}

// configString reads a string-valued key out of an unmarshaled jsonb
// config map. Returns nil when the key is absent, non-string, or empty.
func configString(cfg map[string]any, key string) *string {
	raw, ok := cfg[key]
	if !ok {
		return nil
	}
	s, ok := raw.(string)
	if !ok || s == "" {
		return nil
	}
	return &s
}
