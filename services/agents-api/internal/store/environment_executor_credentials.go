package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrEnvironmentCredentialExists = errors.New("environment executor credential already exists; rotate explicitly")

// IssueEnvironmentExecutorCredential returns a new secret once; it never replaces an existing credential.
func (s *Store) IssueEnvironmentExecutorCredential(ctx context.Context, tenant, environment string) (string, error) {
	return s.writeEnvironmentExecutorCredential(ctx, tenant, environment, false)
}

func (s *Store) RotateEnvironmentExecutorCredential(ctx context.Context, tenant, environment string) (string, error) {
	return s.writeEnvironmentExecutorCredential(ctx, tenant, environment, true)
}

func (s *Store) writeEnvironmentExecutorCredential(ctx context.Context, tenant, environment string, rotate bool) (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	hash := sha256.Sum256([]byte(token))
	err := s.withEnvironmentCredential(ctx, tenant, environment, func(ctx context.Context, q *sqlc.Queries, id pgtype.UUID) error {
		var n int64
		var err error
		if rotate {
			n, err = q.RotateEnvironmentExecutorCredential(ctx, sqlc.RotateEnvironmentExecutorCredentialParams{EnvironmentID: id, TokenSha256: hex.EncodeToString(hash[:])})
		} else {
			n, err = q.IssueEnvironmentExecutorCredential(ctx, sqlc.IssueEnvironmentExecutorCredentialParams{EnvironmentID: id, TokenSha256: hex.EncodeToString(hash[:])})
		}
		if err != nil {
			return err
		}
		if n == 0 {
			if rotate {
				return ErrNotFound
			}
			return ErrEnvironmentCredentialExists
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) RevokeEnvironmentExecutorCredential(ctx context.Context, tenant, environment string) error {
	return s.withEnvironmentCredential(ctx, tenant, environment, func(ctx context.Context, q *sqlc.Queries, id pgtype.UUID) error {
		n, err := q.RevokeEnvironmentExecutorCredential(ctx, id)
		if err == nil && n == 0 {
			return ErrNotFound
		}
		return err
	})
}

func (s *Store) withEnvironmentCredential(ctx context.Context, tenant, environment string, apply func(context.Context, *sqlc.Queries, pgtype.UUID) error) error {
	owned, err := s.GetEnvironment(ctx, tenant, environment)
	if err != nil {
		return err
	}
	id, err := parseID(owned.ID)
	if err != nil {
		return err
	}
	// The Session lock orders provisioning against deletion without a worker lease.
	return s.withPublicSession(ctx, tenant, owned.SessionID, func(ctx context.Context, q *sqlc.Queries, _ pgtype.UUID) error {
		return apply(ctx, q, id)
	})
}

// AuthenticateEnvironmentExecutor derives the tenant from live Environment ownership and the current digest.
func (s *Store) AuthenticateEnvironmentExecutor(ctx context.Context, environment, digest string) (string, error) {
	id, err := parseID(environment)
	if err != nil {
		return "", ErrNotFound
	}
	hash, err := hex.DecodeString(digest)
	if err != nil || len(hash) != sha256.Size {
		return "", ErrNotFound
	}
	tenant, err := s.queries.AuthenticateEnvironmentExecutor(ctx, sqlc.AuthenticateEnvironmentExecutorParams{EnvironmentID: id, TokenSha256: hex.EncodeToString(hash)})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("authenticate environment executor: %w", err)
	}
	return uuid.UUID(tenant.Bytes).String(), nil
}
