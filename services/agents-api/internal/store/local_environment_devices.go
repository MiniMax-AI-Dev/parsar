package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// CreateEnvironmentDevice provisions one dedicated Runtime without widening an existing credential.
func (s *Store) CreateEnvironmentDevice(ctx context.Context, tenantID, environmentID, name, credentialHash string) (ExecutionDevice, error) {
	environment, err := s.GetEnvironment(ctx, tenantID, environmentID)
	if err != nil {
		return ExecutionDevice{}, err
	}
	var configuration struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(environment.Configuration, &configuration) != nil || configuration.Type != "openai_hosted" {
		return ExecutionDevice{}, ErrInvalidInput
	}
	lookup, err := deviceLookup(tenantID, environment.ID)
	if err != nil {
		return ExecutionDevice{}, err
	}
	params, err := newDeviceParams(lookup.TenantID, name, credentialHash)
	if err != nil {
		return ExecutionDevice{}, err
	}
	err = s.withPublicSession(ctx, tenantID, environment.SessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		_, err := q.GetSessionDevice(ctx, sqlc.GetSessionDeviceParams{TenantID: lookup.TenantID, ID: session})
		if err == nil {
			return ErrDeviceBindingConflict
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		id, err := q.CreateEnvironmentDevice(ctx, sqlc.CreateEnvironmentDeviceParams{
			ID: params.ID, TenantID: params.TenantID, Name: params.Name,
			CredentialHash: params.CredentialHash, EnvironmentID: lookup.ID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDeviceBindingConflict
		}
		if err != nil {
			return err
		}
		_, err = q.BindSessionDevice(ctx, sqlc.BindSessionDeviceParams{TenantID: lookup.TenantID, ID: session, ID_2: id})
		return err
	})
	if err != nil {
		return ExecutionDevice{}, err
	}
	return ExecutionDevice{ID: uuid.UUID(params.ID.Bytes).String(), Name: params.Name, EnvironmentID: environment.ID}, nil
}
