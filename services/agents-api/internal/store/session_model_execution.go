package store

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func (s *Store) saveSessionModelExecution(ctx context.Context, q *sqlc.Queries, tenant string, session pgtype.UUID, provider *v1.ModelProviderInput) error {
	if provider == nil {
		return nil
	}
	raw, err := json.Marshal(provider)
	if err != nil {
		return err
	}
	encrypted, err := s.credentialCipher.SealModelExecution(raw, tenant, uuid.UUID(session.Bytes).String())
	if err != nil {
		return ErrCredentialStorageUnavailable
	}
	return q.SaveSessionModelExecution(ctx, sqlc.SaveSessionModelExecutionParams{SessionID: session, EncryptedConfig: encrypted})
}

func (s *Store) SessionModelExecution(ctx context.Context, tenant, session string) (*v1.ModelProviderInput, error) {
	tenantID, err := parseID(tenant)
	if err != nil {
		return nil, err
	}
	sessionID, err := parseID(session)
	if err != nil {
		return nil, err
	}
	ciphertext, err := s.queries.GetSessionModelExecution(ctx, sqlc.GetSessionModelExecutionParams{TenantID: tenantID, SessionID: sessionID})
	if err != nil {
		return nil, errors.New("session model execution configuration is unavailable")
	}
	raw, err := s.credentialCipher.OpenModelExecution(ciphertext, tenant, session)
	if err != nil {
		return nil, errors.New("session model execution decryption is unavailable")
	}
	var provider v1.ModelProviderInput
	if json.Unmarshal(raw, &provider) != nil {
		return nil, errors.New("invalid stored model execution configuration")
	}
	return &provider, provider.Validate()
}
