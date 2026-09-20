package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// EnvironmentTemplate is configuration ownership, independent of provider images.

type EnvironmentTemplate struct {
	Files         []InitialFileMetadata
	ID            string
	Name          *string
	NetworkAccess string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type EnvironmentTemplateInput struct {
	Files         []InitialFile
	SetFiles      bool
	Name          *string
	SetName       bool
	NetworkAccess string
	SetNetwork    bool
}

func (in EnvironmentTemplateInput) valid() bool {
	return (in.Name == nil || (utf8.ValidString(*in.Name) && utf8.RuneCountInString(*in.Name) >= 1 && utf8.RuneCountInString(*in.Name) <= 256)) &&
		(!in.SetNetwork || in.NetworkAccess == "enabled" || in.NetworkAccess == "disabled")
}

type templateMetadataRow sqlc.GetEnvironmentTemplateRow

func templateFromRow(row templateMetadataRow, err error) (EnvironmentTemplate, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return EnvironmentTemplate{}, ErrNotFound
	}
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	result := EnvironmentTemplate{ID: uuid.UUID(row.ID.Bytes).String(), NetworkAccess: row.NetworkAccess, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
	if row.Name.Valid {
		result.Name = &row.Name.String
	}
	if json.Unmarshal(row.Files, &result.Files) != nil {
		return EnvironmentTemplate{}, ErrInvalidInput
	}
	return result, nil
}

func (s *Store) CreateEnvironmentTemplate(ctx context.Context, tenantID string, in EnvironmentTemplateInput) (EnvironmentTemplate, error) {
	if !in.SetNetwork {
		in.NetworkAccess = "enabled"
		in.SetNetwork = true
	}
	if !in.valid() {
		return EnvironmentTemplate{}, ErrInvalidInput
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	var name pgtype.Text
	if in.Name != nil {
		name = pgtype.Text{String: *in.Name, Valid: true}
	}
	id := uuid.New()
	metadata, encrypted, err := s.sealTemplateFiles(uuid.UUID(tenant.Bytes).String(), id.String(), in.Files)
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	row, err := s.queries.CreateEnvironmentTemplate(ctx, sqlc.CreateEnvironmentTemplateParams{ID: pgtype.UUID{Bytes: id, Valid: true}, TenantID: tenant, Name: name, NetworkAccess: in.NetworkAccess, Files: metadata, FileContents: encrypted})
	return templateFromRow(templateMetadataRow(row), err)
}

func (s *Store) GetEnvironmentTemplate(ctx context.Context, tenantID, templateID string) (EnvironmentTemplate, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	id, err := parseID(templateID)
	if err != nil {
		return EnvironmentTemplate{}, ErrNotFound
	}
	row, err := s.queries.GetEnvironmentTemplate(ctx, sqlc.GetEnvironmentTemplateParams{TenantID: tenant, ID: id})
	return templateFromRow(templateMetadataRow(row), err)
}

// Each supplied field replaces atomically, preserving concurrent unrelated updates.
func (s *Store) UpdateEnvironmentTemplate(ctx context.Context, tenantID, templateID string, in EnvironmentTemplateInput) (EnvironmentTemplate, error) {
	if !in.valid() {
		return EnvironmentTemplate{}, ErrInvalidInput
	}
	tenant, err := parseID(tenantID)
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	id, err := parseID(templateID)
	if err != nil {
		return EnvironmentTemplate{}, ErrNotFound
	}
	if !in.SetName && !in.SetNetwork && !in.SetFiles {
		return s.GetEnvironmentTemplate(ctx, tenantID, templateID)
	}
	var name pgtype.Text
	if in.Name != nil {
		name = pgtype.Text{String: *in.Name, Valid: true}
	}
	metadata, encrypted, err := s.sealTemplateFiles(uuid.UUID(tenant.Bytes).String(), uuid.UUID(id.Bytes).String(), in.Files)
	if err != nil {
		return EnvironmentTemplate{}, err
	}
	row, err := s.queries.UpdateEnvironmentTemplate(ctx, sqlc.UpdateEnvironmentTemplateParams{TenantID: tenant, ID: id, Name: name, SetName: in.SetName, NetworkAccess: in.NetworkAccess, SetNetwork: in.SetNetwork, SetFiles: in.SetFiles, Files: metadata, FileContents: encrypted})
	return templateFromRow(templateMetadataRow(row), err)
}

func (s *Store) DeleteEnvironmentTemplate(ctx context.Context, tenantID, templateID string) (string, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return "", err
	}
	id, err := parseID(templateID)
	if err != nil {
		return "", ErrNotFound
	}
	result, err := s.queries.DeleteEnvironmentTemplate(ctx, sqlc.DeleteEnvironmentTemplateParams{TenantID: tenant, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return uuid.UUID(result.Bytes).String(), nil
}

type EnvironmentTemplatePage struct {
	Templates []EnvironmentTemplate
	HasMore   bool
}

func (s *Store) ListEnvironmentTemplates(ctx context.Context, tenantID, cursor string, limit int, ascending bool) (EnvironmentTemplatePage, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return EnvironmentTemplatePage{}, err
	}
	if limit < 1 || limit > 100 {
		return EnvironmentTemplatePage{}, ErrInvalidInput
	}
	params := sqlc.ListEnvironmentTemplatesParams{TenantID: tenant, PageLimit: int32(limit + 1), AfterID: pgtype.UUID{Valid: true}, Ascending: ascending}
	if cursor != "" {
		after, err := s.GetEnvironmentTemplate(ctx, tenantID, cursor)
		if err != nil {
			return EnvironmentTemplatePage{}, err
		}
		params.AfterCreated = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		params.AfterID, _ = parseID(after.ID)
	}
	rows, err := s.queries.ListEnvironmentTemplates(ctx, params)
	if err != nil {
		return EnvironmentTemplatePage{}, err
	}
	page := EnvironmentTemplatePage{Templates: make([]EnvironmentTemplate, 0, min(limit, len(rows))), HasMore: len(rows) > limit}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	for _, row := range rows {
		value, err := templateFromRow(templateMetadataRow(row), nil)
		if err != nil {
			return EnvironmentTemplatePage{}, err
		}
		page.Templates = append(page.Templates, value)
	}
	return page, nil
}
