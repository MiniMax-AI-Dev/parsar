package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func executorCredentialIdentity(principal identity.Principal, keyID string) (pgtype.UUID, pgtype.UUID, error) {
	if err := principal.Validate(); err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	tenant, err := parseID(principal.TenantID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	id, err := parseID(keyID)
	return tenant, id, err
}

func newExecutorSecret() (string, string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	hash := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(hash[:]), nil
}

func issuedExecutorCredential(id, environment pgtype.UUID, token string) IssuedExecutorCredential {
	result := IssuedExecutorCredential{KeyID: uuid.UUID(id.Bytes).String(), Token: token}
	if environment.Valid {
		result.EnvironmentID = uuid.UUID(environment.Bytes).String()
	}
	return result
}

func (s *Store) withExecutorCredentialTarget(ctx context.Context, principal identity.Principal, environment pgtype.UUID, apply func(context.Context, *sqlc.Queries) error) error {
	if !environment.Valid {
		return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error { return apply(ctx, s.queries.WithTx(tx)) })
	}
	owned, err := s.GetEnvironment(ctx, principal.TenantID, uuid.UUID(environment.Bytes).String())
	if err != nil {
		return err
	}
	tenant, _ := parseID(principal.TenantID)
	// The Session lock orders exact-target issuance and rotation against deletion.
	return s.withPublicSession(ctx, principal.TenantID, owned.SessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		row, err := q.GetSession(ctx, sqlc.GetSessionParams{TenantID: tenant, ID: session})
		if err != nil {
			return err
		}
		if !row.CreatorKind.Valid || !row.CreatorID.Valid || row.CreatorKind.String != principal.SubjectKind || row.CreatorID.String != principal.SubjectID {
			return ErrNotFound
		}
		return apply(ctx, q)
	})
}
