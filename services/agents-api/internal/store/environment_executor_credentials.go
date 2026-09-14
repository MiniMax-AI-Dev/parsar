package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrExecutorCredentialExists = errors.New("executor key ID already exists; rotate explicitly")

type IssuedExecutorCredential struct {
	KeyID         string `json:"key_id"`
	EnvironmentID string `json:"environment_id,omitempty"`
	Token         string `json:"executor_token"`
}

// IssueExecutorCredential returns a new connect-only secret once, without replacing an existing ID.
func (s *Store) IssueExecutorCredential(ctx context.Context, principal identity.Principal, keyID, environment string) (IssuedExecutorCredential, error) {
	tenant, id, err := executorCredentialIdentity(principal, keyID)
	if err != nil {
		return IssuedExecutorCredential{}, err
	}
	exists, err := s.queries.ExecutorProjectScopeExists(ctx, sqlc.ExecutorProjectScopeExistsParams{TenantID: tenant, OrganizationID: principal.OrganizationID, ProjectID: principal.ProjectID})
	if err != nil {
		return IssuedExecutorCredential{}, err
	}
	if !exists {
		return IssuedExecutorCredential{}, ErrNotFound
	}
	var restriction pgtype.UUID
	if environment != "" {
		restriction, err = parseID(environment)
		if err != nil {
			return IssuedExecutorCredential{}, err
		}
	}
	token, digest, err := newExecutorSecret()
	if err != nil {
		return IssuedExecutorCredential{}, err
	}
	var result IssuedExecutorCredential
	err = s.withExecutorCredentialTarget(ctx, principal, restriction, func(ctx context.Context, q *sqlc.Queries) error {
		row, err := q.IssueExecutorCredential(ctx, sqlc.IssueExecutorCredentialParams{
			KeyID: id, TenantID: tenant, SubjectKind: pgtype.Text{String: principal.SubjectKind, Valid: true}, SubjectID: pgtype.Text{String: principal.SubjectID, Valid: true},
			OrganizationID: principal.OrganizationID, ProjectID: principal.ProjectID, EnvironmentID: restriction, TokenSha256: digest,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrExecutorCredentialExists
		}
		if err != nil {
			return err
		}
		result = issuedExecutorCredential(row.KeyID, row.EnvironmentID, token)
		return nil
	})
	if err != nil {
		return IssuedExecutorCredential{}, err
	}
	return result, nil
}

func (s *Store) RotateExecutorCredential(ctx context.Context, principal identity.Principal, keyID string) (IssuedExecutorCredential, error) {
	tenant, id, err := executorCredentialIdentity(principal, keyID)
	if err != nil {
		return IssuedExecutorCredential{}, err
	}
	restriction, err := s.executorCredentialRestriction(ctx, principal, tenant, id)
	if err != nil {
		return IssuedExecutorCredential{}, err
	}
	token, digest, err := newExecutorSecret()
	if err != nil {
		return IssuedExecutorCredential{}, err
	}
	var result IssuedExecutorCredential
	err = s.withExecutorCredentialTarget(ctx, principal, restriction, func(ctx context.Context, q *sqlc.Queries) error {
		row, err := q.RotateExecutorCredential(ctx, sqlc.RotateExecutorCredentialParams{KeyID: id, TenantID: tenant, SubjectKind: pgtype.Text{String: principal.SubjectKind, Valid: true}, SubjectID: pgtype.Text{String: principal.SubjectID, Valid: true}, TokenSha256: digest})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		result = issuedExecutorCredential(row.KeyID, row.EnvironmentID, token)
		return nil
	})
	if err != nil {
		return IssuedExecutorCredential{}, err
	}
	return result, nil
}

func (s *Store) RevokeExecutorCredential(ctx context.Context, principal identity.Principal, keyID string) error {
	tenant, id, err := executorCredentialIdentity(principal, keyID)
	if err != nil {
		return err
	}
	if _, err := s.executorCredentialRestriction(ctx, principal, tenant, id); err != nil {
		return err
	}
	n, err := s.queries.RevokeExecutorCredential(ctx, sqlc.RevokeExecutorCredentialParams{KeyID: id, TenantID: tenant, SubjectKind: pgtype.Text{String: principal.SubjectKind, Valid: true}, SubjectID: pgtype.Text{String: principal.SubjectID, Valid: true}})
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) executorCredentialRestriction(ctx context.Context, principal identity.Principal, tenant, id pgtype.UUID) (pgtype.UUID, error) {
	restriction, err := s.queries.GetExecutorCredentialForPrincipal(ctx, sqlc.GetExecutorCredentialForPrincipalParams{
		KeyID: id, TenantID: tenant, SubjectKind: pgtype.Text{String: principal.SubjectKind, Valid: true}, SubjectID: pgtype.Text{String: principal.SubjectID, Valid: true},
		OrganizationID: principal.OrganizationID, ProjectID: principal.ProjectID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, ErrNotFound
	}
	return restriction, err
}

// AuthenticateEnvironmentExecutor checks the current key and recorded Session creator in one database snapshot.
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
