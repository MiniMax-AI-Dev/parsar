package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// UpsertOAuthUserInput is the projection an OAuth callback hands to the
// store after the upstream profile is fetched. The "upsert user + bind
// identity" pair runs in one tx so a half-failed callback can't leave an
// orphan auth_identities row pointing at a deleted user.
type UpsertOAuthUserInput struct {
	Provider string         // "feishu" / "github" / "google" / "oidc"
	Subject  string         // stable per-provider identifier
	Email    string         // required — users.email is the cross-provider dedupe key
	Name     string         // optional — falls back to email local-part
	Metadata map[string]any // stashed in auth_identities.metadata; never used as a query key
	Now      time.Time
}

// UpsertOAuthUserResult reports the resolved user plus whether the row
// was freshly created by this call.
type UpsertOAuthUserResult struct {
	UserID  string
	Email   string
	Name    string
	Created bool
}

// UpsertOAuthUser finds-or-creates the users row by email then binds
// (provider, subject) to it, transactionally. Idempotent: replays with
// the same (provider, subject) return the same user_id; metadata jsonb
// is refreshed on conflict.
func (s *Store) UpsertOAuthUser(ctx context.Context, in UpsertOAuthUserInput) (UpsertOAuthUserResult, error) {
	provider := strings.TrimSpace(in.Provider)
	subject := strings.TrimSpace(in.Subject)
	email := strings.TrimSpace(in.Email)
	name := strings.TrimSpace(in.Name)
	if provider == "" || subject == "" || email == "" {
		return UpsertOAuthUserResult{}, errors.New("store: UpsertOAuthUserInput requires provider, subject, and email")
	}
	if name == "" {
		if at := strings.Index(email, "@"); at > 0 {
			name = email[:at]
		}
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	metadata, err := json.Marshal(nonNilMap(in.Metadata))
	if err != nil {
		return UpsertOAuthUserResult{}, fmt.Errorf("store: marshal identity metadata: %w", err)
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return UpsertOAuthUserResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)

	userRow, err := q.UpsertUserByEmail(ctx, sqlc.UpsertUserByEmailParams{
		ID:    mustUUID(newID()),
		Email: email,
		Name:  name,
		Now:   timestamptz(now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UpsertOAuthUserResult{}, fmt.Errorf("store: UpsertUserByEmail returned no row for %s", email)
		}
		return UpsertOAuthUserResult{}, fmt.Errorf("store: upsert user: %w", err)
	}

	if err := q.UpsertAuthIdentity(ctx, sqlc.UpsertAuthIdentityParams{
		ID:       mustUUID(newID()),
		UserID:   mustUUID(userRow.ID),
		Provider: provider,
		Subject:  subject,
		Metadata: metadata,
		Now:      timestamptz(now),
	}); err != nil {
		return UpsertOAuthUserResult{}, fmt.Errorf("store: upsert auth_identity: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return UpsertOAuthUserResult{}, err
	}

	return UpsertOAuthUserResult{
		UserID:  userRow.ID,
		Email:   userRow.Email,
		Name:    userRow.Name,
		Created: userRow.Created,
	}, nil
}
