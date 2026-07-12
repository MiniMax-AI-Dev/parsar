package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// SpecFragmentRead is the store-level view of a spec_fragments row.
// String IDs use "" for SQL NULL.
type SpecFragmentRead struct {
	ID          string
	WorkspaceID string
	Title       string
	Body        string
	Tags        []string
	Source      string
	CreatedBy   string // "" when the fragment was authored by an agent
	AgentActor  string // "" when authored by a human
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// InsertSpecFragmentInput collects insert-time columns. ID and Now are
// caller-supplied; Source stays untyped to decouple from specmemory enums.
type InsertSpecFragmentInput struct {
	ID          string
	WorkspaceID string
	Title       string
	Body        string
	Tags        []string
	Source      string
	CreatedBy   string // "" for agent writes (column will be NULL)
	AgentActor  string // "" for human writes
	Now         time.Time
}

// UpdateSpecFragmentInput is a full-replace payload. Provenance
// (Source/CreatedBy/AgentActor) is locked at insert time.
type UpdateSpecFragmentInput struct {
	ID    string
	Title string
	Body  string
	Tags  []string
	Now   time.Time
}

// ListWorkspaceSpecFragmentsInput: SourceFilter/TagFilter use empty
// string / empty slice as "skip" sentinels matched by the SQL.
type ListWorkspaceSpecFragmentsInput struct {
	WorkspaceID  string
	SourceFilter string   // "" = all sources
	TagFilter    []string // nil/empty = all tags
	Limit        int32    // <= 0 -> defaultReadLimit
}

// ListWorkspaceSpecFragmentsSinceInput: Since is the timestamp of the
// last fragment the caller has seen; the query returns strictly newer rows.
type ListWorkspaceSpecFragmentsSinceInput struct {
	WorkspaceID string
	Since       time.Time
	Limit       int32 // <= 0 -> defaultReadLimit
}

// InsertSpecFragment writes a fragment. Caller validates Source against
// specmemory.Source before calling.
func (s *Store) InsertSpecFragment(ctx context.Context, input InsertSpecFragmentInput) (SpecFragmentRead, error) {
	id, err := uuid(input.ID)
	if err != nil {
		return SpecFragmentRead{}, fmt.Errorf("spec fragment: id: %w", err)
	}
	workspaceID, err := uuid(input.WorkspaceID)
	if err != nil {
		return SpecFragmentRead{}, fmt.Errorf("spec fragment: workspace_id: %w", err)
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return SpecFragmentRead{}, errors.New("spec fragment: title is required")
	}
	if input.Body == "" {
		return SpecFragmentRead{}, errors.New("spec fragment: body is required")
	}
	source := strings.TrimSpace(input.Source)
	if source == "" {
		return SpecFragmentRead{}, errors.New("spec fragment: source is required")
	}
	createdBy, err := optionalUUID(input.CreatedBy, "spec fragment: created_by")
	if err != nil {
		return SpecFragmentRead{}, err
	}
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	row, err := sqlc.New(s.db).InsertSpecFragment(ctx, sqlc.InsertSpecFragmentParams{
		ID:          id,
		WorkspaceID: workspaceID,
		Title:       title,
		Body:        input.Body,
		Tags:        normalizeTags(input.Tags),
		Source:      source,
		CreatedBy:   createdBy,
		AgentActor:  strings.TrimSpace(input.AgentActor),
		Now:         timestamptz(now),
	})
	if err != nil {
		return SpecFragmentRead{}, fmt.Errorf("spec fragment: insert: %w", err)
	}
	return specFragmentFromInsertRow(row), nil
}

// GetSpecFragment fetches a single non-deleted fragment. Returns
// (zero, false, nil) when missing or soft-deleted.
func (s *Store) GetSpecFragment(ctx context.Context, idRaw string) (SpecFragmentRead, bool, error) {
	id, err := uuid(idRaw)
	if err != nil {
		return SpecFragmentRead{}, false, fmt.Errorf("spec fragment: id: %w", err)
	}
	row, err := sqlc.New(s.db).GetSpecFragment(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SpecFragmentRead{}, false, nil
		}
		return SpecFragmentRead{}, false, fmt.Errorf("spec fragment: get: %w", err)
	}
	return specFragmentFromGetRow(row), true, nil
}

// ListWorkspaceSpecFragments returns active fragments ordered by
// updated_at desc so the freshest dominate when Limit caps the set.
func (s *Store) ListWorkspaceSpecFragments(ctx context.Context, input ListWorkspaceSpecFragmentsInput) ([]SpecFragmentRead, error) {
	workspaceID, err := uuid(input.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("spec fragment: workspace_id: %w", err)
	}
	limit := input.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}
	rows, err := sqlc.New(s.db).ListWorkspaceSpecFragments(ctx, sqlc.ListWorkspaceSpecFragmentsParams{
		WorkspaceID: workspaceID,
		Source:      strings.TrimSpace(input.SourceFilter),
		TagFilter:   normalizeTags(input.TagFilter),
		ItemLimit:   limit,
	})
	if err != nil {
		return nil, fmt.Errorf("spec fragment: list: %w", err)
	}
	out := make([]SpecFragmentRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, specFragmentFromListRow(r))
	}
	return out, nil
}

// ListWorkspaceSpecFragmentsSince returns fragments updated strictly after the cursor.
func (s *Store) ListWorkspaceSpecFragmentsSince(ctx context.Context, input ListWorkspaceSpecFragmentsSinceInput) ([]SpecFragmentRead, error) {
	workspaceID, err := uuid(input.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("spec fragment: workspace_id: %w", err)
	}
	if input.Since.IsZero() {
		return nil, errors.New("spec fragment: since cursor is required")
	}
	limit := input.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}
	rows, err := sqlc.New(s.db).ListWorkspaceSpecFragmentsSince(ctx, sqlc.ListWorkspaceSpecFragmentsSinceParams{
		WorkspaceID: workspaceID,
		Since:       timestamptz(input.Since.UTC()),
		ItemLimit:   limit,
	})
	if err != nil {
		return nil, fmt.Errorf("spec fragment: list since: %w", err)
	}
	out := make([]SpecFragmentRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, specFragmentFromListSinceRow(r))
	}
	return out, nil
}

// UpdateSpecFragment performs a full-replace edit. Returns (zero, false, nil)
// when soft-deleted — the UPDATE's deleted_at guard prevents zombie edits.
func (s *Store) UpdateSpecFragment(ctx context.Context, input UpdateSpecFragmentInput) (SpecFragmentRead, bool, error) {
	id, err := uuid(input.ID)
	if err != nil {
		return SpecFragmentRead{}, false, fmt.Errorf("spec fragment: id: %w", err)
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return SpecFragmentRead{}, false, errors.New("spec fragment: title is required")
	}
	if input.Body == "" {
		return SpecFragmentRead{}, false, errors.New("spec fragment: body is required")
	}
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	row, err := sqlc.New(s.db).UpdateSpecFragment(ctx, sqlc.UpdateSpecFragmentParams{
		Title: title,
		Body:  input.Body,
		Tags:  normalizeTags(input.Tags),
		Now:   timestamptz(now),
		ID:    id,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SpecFragmentRead{}, false, nil
		}
		return SpecFragmentRead{}, false, fmt.Errorf("spec fragment: update: %w", err)
	}
	return specFragmentFromUpdateRow(row), true, nil
}

// SoftDeleteSpecFragment tombstones the row. Idempotent. Caller emits audit.
func (s *Store) SoftDeleteSpecFragment(ctx context.Context, idRaw string, now time.Time) error {
	id, err := uuid(idRaw)
	if err != nil {
		return fmt.Errorf("spec fragment: id: %w", err)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := sqlc.New(s.db).SoftDeleteSpecFragment(ctx, sqlc.SoftDeleteSpecFragmentParams{
		Now: timestamptz(now.UTC()),
		ID:  id,
	}); err != nil {
		return fmt.Errorf("spec fragment: soft delete: %w", err)
	}
	return nil
}

func specFragmentFromInsertRow(r sqlc.InsertSpecFragmentRow) SpecFragmentRead {
	return specFragmentFromRow(specFragmentRow(r))
}

func specFragmentFromGetRow(r sqlc.GetSpecFragmentRow) SpecFragmentRead {
	return specFragmentFromRow(specFragmentRow(r))
}

func specFragmentFromListRow(r sqlc.ListWorkspaceSpecFragmentsRow) SpecFragmentRead {
	return specFragmentFromRow(specFragmentRow(r))
}

func specFragmentFromListSinceRow(r sqlc.ListWorkspaceSpecFragmentsSinceRow) SpecFragmentRead {
	return specFragmentFromRow(specFragmentRow(r))
}

func specFragmentFromUpdateRow(r sqlc.UpdateSpecFragmentRow) SpecFragmentRead {
	return specFragmentFromRow(specFragmentRow(r))
}

type specFragmentRow sqlc.GetSpecFragmentRow

func specFragmentFromRow(r specFragmentRow) SpecFragmentRead {
	return SpecFragmentRead{
		ID:          r.ID,
		WorkspaceID: r.WorkspaceID,
		Title:       r.Title,
		Body:        r.Body,
		Tags:        r.Tags,
		Source:      r.Source,
		CreatedBy:   pgUUIDString(r.CreatedBy),
		AgentActor:  r.AgentActor,
		CreatedAt:   pgTime(r.CreatedAt),
		UpdatedAt:   pgTime(r.UpdatedAt),
	}
}
