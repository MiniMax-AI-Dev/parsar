package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type CreateSecretInput struct {
	WorkspaceID string
	Name        string
	Kind        string
	Provider    string
	AuthType    string
	Payload     map[string]any
	Masked      string
	CreatedBy   string
	// CredentialKindCode is optional metadata that pins a capability_inline
	// secret to a single credential_kinds.code. Used by the agent-creation
	// shared-binding picker to filter secrets by the kind they hold.
	CredentialKindCode string
}

type SecretRead struct {
	ManagementWorkspaceID string         `json:"management_workspace_id,omitempty"`
	ID                    string         `json:"id"`
	Slug                  string         `json:"slug"`
	Name                  string         `json:"name"`
	Kind                  string         `json:"kind"`
	Provider              string         `json:"provider"`
	AuthType              string         `json:"auth_type"`
	KeyVersion            string         `json:"key_version"`
	Status                string         `json:"status"`
	Masked                string         `json:"masked"`
	Metadata              map[string]any `json:"metadata"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
}

type SecretPayload struct {
	SecretRead
	EncryptedPayload []byte
}

func (s *Store) CreateSecret(ctx context.Context, input CreateSecretInput, encryptedPayload []byte) (SecretRead, error) {
	now := time.Now().UTC()
	createdBy := nullableUUID(input.CreatedBy)
	metaPayload := map[string]any{"masked": strings.TrimSpace(input.Masked)}
	if code := strings.TrimSpace(input.CredentialKindCode); code != "" {
		metaPayload["credential_kind_code"] = code
	}
	if strings.TrimSpace(input.Kind) == "capability_inline" {
		metaPayload["workspace_id"] = strings.TrimSpace(input.WorkspaceID)
	}
	metadata, err := json.Marshal(metaPayload)
	if err != nil {
		return SecretRead{}, err
	}
	row, err := sqlc.New(s.db).CreateSecret(ctx, sqlc.CreateSecretParams{
		ID:                    mustUUID(newID()),
		Slug:                  generateAutoSlug("secret"),
		Name:                  strings.TrimSpace(input.Name),
		Kind:                  secretKind(input.Kind),
		Provider:              strings.TrimSpace(input.Provider),
		AuthType:              strings.TrimSpace(input.AuthType),
		EncryptedPayload:      encryptedPayload,
		KeyVersion:            "v1",
		Metadata:              metadata,
		CreatedBy:             createdBy,
		ManagementWorkspaceID: nullableUUID(input.WorkspaceID),
		Now:                   timestamptz(now),
	})
	if err != nil {
		return SecretRead{}, err
	}
	read := secretReadFromCreateRow(row)

	s.emitAuditEvent(audit.Event{
		OccurredAt: now,
		Source:     audit.SourceAdmin,
		EventType:  auditSecretCreated,
		ActorType:  audit.ActorTypeSystem,
		ActorID:    input.CreatedBy,
		TargetType: "secret",
		TargetID:   read.ID,
		Payload: map[string]any{
			"source":    auditSourceDevSecretWrite,
			"name":      read.Name,
			"slug":      read.Slug,
			"kind":      read.Kind,
			"provider":  read.Provider,
			"auth_type": read.AuthType,
		},
	})

	return read, nil
}

func (s *Store) DisableSecret(ctx context.Context, workspaceID string, secretID string) (SecretRead, error) {
	now := time.Now().UTC()
	if _, err := s.GetSecretPayload(ctx, workspaceID, secretID); err != nil {
		return SecretRead{}, err
	}
	secretUUID, err := uuid(secretID)
	if err != nil {
		return SecretRead{}, err
	}
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return SecretRead{}, err
	}
	row, err := sqlc.New(s.db).DisableSecret(ctx, sqlc.DisableSecretParams{ID: secretUUID, WorkspaceID: workspaceUUID, Now: timestamptz(now)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SecretRead{}, fmt.Errorf("%w: %s", ErrUnknownSecret, secretID)
		}
		return SecretRead{}, err
	}
	read := secretReadFromDisableRow(row)

	s.emitAuditEvent(audit.Event{
		OccurredAt: now,
		Source:     audit.SourceAdmin,
		EventType:  auditSecretDisabled,
		ActorType:  audit.ActorTypeSystem,
		TargetType: "secret",
		TargetID:   read.ID,
		Payload: map[string]any{
			"source": auditSourceDevSecretWrite,
			"name":   read.Name,
			"slug":   read.Slug,
			"status": read.Status,
		},
	})

	return read, nil
}

func secretReadFromCreateRow(row sqlc.CreateSecretRow) SecretRead {
	return secretRead(row.ID, row.Slug, row.Name, row.Kind, row.Provider, row.AuthType, row.KeyVersion, row.Status, row.Metadata, row.ManagementWorkspaceID, row.CreatedAt, row.UpdatedAt)
}

func secretReadFromListRow(row sqlc.ListSecretsRow) SecretRead {
	return secretRead(row.ID, row.Slug, row.Name, row.Kind, row.Provider, row.AuthType, row.KeyVersion, row.Status, row.Metadata, row.ManagementWorkspaceID, row.CreatedAt, row.UpdatedAt)
}

func secretReadFromWorkspaceListRow(row sqlc.ListSecretsForWorkspaceRow) SecretRead {
	return secretRead(row.ID, row.Slug, row.Name, row.Kind, row.Provider, row.AuthType, row.KeyVersion, row.Status, row.Metadata, row.ManagementWorkspaceID, row.CreatedAt, row.UpdatedAt)
}

func secretReadFromDisableRow(row sqlc.DisableSecretRow) SecretRead {
	return secretRead(row.ID, row.Slug, row.Name, row.Kind, row.Provider, row.AuthType, row.KeyVersion, row.Status, row.Metadata, row.ManagementWorkspaceID, row.CreatedAt, row.UpdatedAt)
}

func secretReadFromSecretRow(row sqlc.GetSecretPayloadRow) SecretRead {
	return secretRead(row.ID, row.Slug, row.Name, row.Kind, row.Provider, row.AuthType, row.KeyVersion, row.Status, row.Metadata, row.ManagementWorkspaceID, row.CreatedAt, row.UpdatedAt)
}

func secretReadFromUpdatePayloadRow(row sqlc.UpdateSecretPayloadRow) SecretRead {
	return secretRead(row.ID, row.Slug, row.Name, row.Kind, row.Provider, row.AuthType, row.KeyVersion, row.Status, row.Metadata, row.ManagementWorkspaceID, row.CreatedAt, row.UpdatedAt)
}

func secretRead(id, slug, name, kind, provider, authType, keyVersion, status string, metadataJSON []byte, managementWorkspaceID string, createdAt, updatedAt pgtype.Timestamptz) SecretRead {
	metadata := decodeJSONMap(metadataJSON)
	masked, _ := metadata["masked"].(string)
	return SecretRead{
		ID:                    id,
		Slug:                  slug,
		Name:                  name,
		Kind:                  kind,
		Provider:              provider,
		AuthType:              authType,
		KeyVersion:            keyVersion,
		Status:                status,
		Masked:                masked,
		Metadata:              metadata,
		ManagementWorkspaceID: managementWorkspaceID,
		CreatedAt:             pgTime(createdAt),
		UpdatedAt:             pgTime(updatedAt),
	}
}
