package store

import "context"

// SessionCreation starts observation at the Session upsert. Session contains the
// resource fields before initial work, not a later activity snapshot. Only a new
// creation emits that snapshot; retries observe future changes from Cursor.
type SessionCreation struct {
	Session Session
	Created bool
	Cursor  int64
}

// CreateSessionStream shares admission and retry identity with ordinary creation.
// The upsert returns its event cursor while holding the Session write lock, before
// initial inputs commit. No post-commit cursor lookup may skip those inputs.
func (s *Store) CreateSessionStream(ctx context.Context, tenant string, input CreateSessionInput) (SessionCreation, error) {
	return s.createSession(ctx, tenant, input)
}
