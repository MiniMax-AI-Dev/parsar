package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrCredentialStorageUnavailable = errors.New("credential encryption is not configured")

// Credential contains only public metadata. Secret ciphertext is never selected
// by resource reads; decryption belongs to scoped execution lookup only.
type Credential struct {
	ID, VaultID, Name, AuthType, MCPServerURL string
	CreatedAt, UpdatedAt                      time.Time
}

type CreateStaticCredentialInput struct {
	Name, MCPServerURL, Token string
}

// NewWithCredentialCipher configures immutable credential encryption before the
// Store is published. A nil cipher leaves non-secret resource operations available.
func NewWithCredentialCipher(pool *pgxpool.Pool, cipher *credentialcrypto.Cipher) *Store {
	s := New(pool)
	s.credentialCipher = cipher
	return s
}

func (s *Store) CreateStaticCredential(ctx context.Context, tenantID, vaultID string, input CreateStaticCredentialInput) (Credential, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return Credential{}, err
	}
	vault, err := parseID(vaultID)
	if err != nil {
		return Credential{}, err
	}
	if !validVaultName(input.Name) || input.MCPServerURL == "" {
		return Credential{}, ErrInvalidInput
	}
	if s.credentialCipher == nil {
		return Credential{}, ErrCredentialStorageUnavailable
	}
	id := uuid.New()
	binding := credentialcrypto.Binding{TenantID: uuid.UUID(tenant.Bytes).String(), VaultID: uuid.UUID(vault.Bytes).String(), CredentialID: id.String(), AuthType: "static_bearer", Destination: input.MCPServerURL}
	ciphertext, err := s.credentialCipher.Seal([]byte(input.Token), binding)
	if err != nil {
		return Credential{}, errors.New("credential encryption failed")
	}
	row, err := s.queries.CreateStaticCredential(ctx, sqlc.CreateStaticCredentialParams{
		ID: pgtype.UUID{Bytes: id, Valid: true}, TenantID: tenant, VaultID: vault,
		Name: input.Name, McpServerUrl: input.MCPServerURL, TokenCiphertext: ciphertext,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, fmt.Errorf("create credential: %w", err)
	}
	return credentialFromRow(sqlc.GetCredentialRow(row)), nil
}

func (s *Store) GetCredential(ctx context.Context, tenantID, vaultID, credentialID string) (Credential, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return Credential{}, err
	}
	vault, err := parseID(vaultID)
	if err != nil {
		return Credential{}, err
	}
	id, err := parseID(credentialID)
	if err != nil {
		return Credential{}, err
	}
	row, err := s.queries.GetCredential(ctx, sqlc.GetCredentialParams{TenantID: tenant, VaultID: vault, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Credential{}, ErrNotFound
	}
	if err != nil {
		return Credential{}, fmt.Errorf("get credential: %w", err)
	}
	return credentialFromRow(row), nil
}

func credentialFromRow(row sqlc.GetCredentialRow) Credential {
	return Credential{
		ID: uuid.UUID(row.ID.Bytes).String(), VaultID: uuid.UUID(row.VaultID.Bytes).String(),
		Name: row.Name, AuthType: row.AuthType, MCPServerURL: row.McpServerUrl,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}
}
