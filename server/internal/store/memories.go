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

// MemoryRead is the store-level view of a memories row. Plain string
// fields throughout; specmemory.Service validates scope/memory_type/
// source against the typed enums at the call boundary.
//
// WorkspaceID is "" when scope='user'; ConversationID is "" when the
// memory wasn't written inside an agent session turn.
type MemoryRead struct {
	ID             string
	Scope          string
	UserID         string
	WorkspaceID    string
	MemoryType     string
	Title          string
	Body           string
	Why            string
	Tags           []string
	Source         string
	AgentActor     string
	ConversationID string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// InsertMemoryInput collects the columns the caller controls at insert
// time. Caller has already validated scope/memory_type/source via the
// specmemory enums. WorkspaceID must match Scope per the
// memories_scope_workspace_id_match_check DB CHECK.
type InsertMemoryInput struct {
	ID             string
	Scope          string
	UserID         string
	WorkspaceID    string // required when Scope='workspace', "" when 'user'
	MemoryType     string
	Title          string
	Body           string
	Why            string
	Tags           []string
	Source         string
	AgentActor     string
	ConversationID string // "" for writes outside an agent turn
	Now            time.Time
}

// UpdateMemoryInput is the full-replace payload. Provenance fields are
// locked at insert; a "moved" memory should be deleted + re-inserted.
type UpdateMemoryInput struct {
	ID    string
	Title string
	Body  string
	Why   string
	Tags  []string
	Now   time.Time
}

// ListUserMemoriesInput drives the user-scope Memory tab + SessionStart
// snapshot. Filters use empty-string / empty-slice "skip" sentinels.
type ListUserMemoriesInput struct {
	UserID           string
	MemoryTypeFilter string   // "" = all types
	TagFilter        []string // nil/empty = all tags
	Limit            int32    // <= 0 -> defaultReadLimit
}

// ListWorkspaceMemoriesInput drives the workspace-scope Memory tab +
// SessionStart snapshot. Workspace memories are shared across users on
// the workspace — no user_id filter.
type ListWorkspaceMemoriesInput struct {
	WorkspaceID      string
	MemoryTypeFilter string
	TagFilter        []string
	Limit            int32
}

// ListUserMemoriesSinceInput is the per-turn incremental cursor for
// user-scope memories.
type ListUserMemoriesSinceInput struct {
	UserID string
	Since  time.Time
	Limit  int32
}

// ListWorkspaceMemoriesSinceInput is the per-turn incremental cursor for
// workspace-scope memories.
type ListWorkspaceMemoriesSinceInput struct {
	WorkspaceID string
	Since       time.Time
	Limit       int32
}

// InsertMemory persists a new memory row. The store only enforces
// structural invariants; specmemory validation happens upstream.
func (s *Store) InsertMemory(ctx context.Context, input InsertMemoryInput) (MemoryRead, error) {
	id, err := uuid(input.ID)
	if err != nil {
		return MemoryRead{}, fmt.Errorf("memory: id: %w", err)
	}
	scope := strings.TrimSpace(input.Scope)
	if scope == "" {
		return MemoryRead{}, errors.New("memory: scope is required")
	}
	userID, err := uuid(input.UserID)
	if err != nil {
		return MemoryRead{}, fmt.Errorf("memory: user_id: %w", err)
	}
	workspaceID, err := optionalUUID(input.WorkspaceID, "memory: workspace_id")
	if err != nil {
		return MemoryRead{}, err
	}
	memoryType := strings.TrimSpace(input.MemoryType)
	if memoryType == "" {
		return MemoryRead{}, errors.New("memory: memory_type is required")
	}
	if input.Body == "" {
		return MemoryRead{}, errors.New("memory: body is required")
	}
	source := strings.TrimSpace(input.Source)
	if source == "" {
		return MemoryRead{}, errors.New("memory: source is required")
	}
	conversationID, err := optionalUUID(input.ConversationID, "memory: conversation_id")
	if err != nil {
		return MemoryRead{}, err
	}
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	row, err := sqlc.New(s.db).InsertMemory(ctx, sqlc.InsertMemoryParams{
		ID:             id,
		Scope:          scope,
		UserID:         userID,
		WorkspaceID:    workspaceID,
		MemoryType:     memoryType,
		Title:          strings.TrimSpace(input.Title),
		Body:           input.Body,
		Why:            input.Why,
		Tags:           normalizeTags(input.Tags),
		Source:         source,
		AgentActor:     strings.TrimSpace(input.AgentActor),
		ConversationID: conversationID,
		Now:            timestamptz(now),
	})
	if err != nil {
		return MemoryRead{}, fmt.Errorf("memory: insert: %w", err)
	}
	return memoryFromInsertRow(row), nil
}

// GetMemory fetches a single non-deleted memory row. (zero, false, nil)
// when the row does not exist or has been soft-deleted.
func (s *Store) GetMemory(ctx context.Context, idRaw string) (MemoryRead, bool, error) {
	id, err := uuid(idRaw)
	if err != nil {
		return MemoryRead{}, false, fmt.Errorf("memory: id: %w", err)
	}
	row, err := sqlc.New(s.db).GetMemory(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MemoryRead{}, false, nil
		}
		return MemoryRead{}, false, fmt.Errorf("memory: get: %w", err)
	}
	return memoryFromGetRow(row), true, nil
}

// ListUserMemories returns active user-scope memories for the given
// user, ordered by updated_at desc.
func (s *Store) ListUserMemories(ctx context.Context, input ListUserMemoriesInput) ([]MemoryRead, error) {
	userID, err := uuid(input.UserID)
	if err != nil {
		return nil, fmt.Errorf("memory: user_id: %w", err)
	}
	limit := input.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}
	rows, err := sqlc.New(s.db).ListUserMemories(ctx, sqlc.ListUserMemoriesParams{
		UserID:     userID,
		MemoryType: strings.TrimSpace(input.MemoryTypeFilter),
		TagFilter:  normalizeTags(input.TagFilter),
		ItemLimit:  limit,
	})
	if err != nil {
		return nil, fmt.Errorf("memory: list user: %w", err)
	}
	out := make([]MemoryRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, memoryFromListUserRow(r))
	}
	return out, nil
}

// ListWorkspaceMemories returns active workspace-scope memories for the
// given workspace (shared across all users on the workspace), ordered by
// updated_at desc.
func (s *Store) ListWorkspaceMemories(ctx context.Context, input ListWorkspaceMemoriesInput) ([]MemoryRead, error) {
	workspaceID, err := uuid(input.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("memory: workspace_id: %w", err)
	}
	limit := input.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}
	rows, err := sqlc.New(s.db).ListWorkspaceMemories(ctx, sqlc.ListWorkspaceMemoriesParams{
		WorkspaceID: workspaceID,
		MemoryType:  strings.TrimSpace(input.MemoryTypeFilter),
		TagFilter:   normalizeTags(input.TagFilter),
		ItemLimit:   limit,
	})
	if err != nil {
		return nil, fmt.Errorf("memory: list workspace: %w", err)
	}
	out := make([]MemoryRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, memoryFromListWorkspaceRow(r))
	}
	return out, nil
}

// ListUserMemoriesSince returns user-scope memories updated strictly
// after the cursor — surfaces new + edited rows for per-turn injection.
func (s *Store) ListUserMemoriesSince(ctx context.Context, input ListUserMemoriesSinceInput) ([]MemoryRead, error) {
	userID, err := uuid(input.UserID)
	if err != nil {
		return nil, fmt.Errorf("memory: user_id: %w", err)
	}
	if input.Since.IsZero() {
		return nil, errors.New("memory: since cursor is required")
	}
	limit := input.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}
	rows, err := sqlc.New(s.db).ListUserMemoriesSince(ctx, sqlc.ListUserMemoriesSinceParams{
		UserID:    userID,
		Since:     timestamptz(input.Since.UTC()),
		ItemLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("memory: list user since: %w", err)
	}
	out := make([]MemoryRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, memoryFromListUserSinceRow(r))
	}
	return out, nil
}

// ListWorkspaceMemoriesSince returns workspace-scope memories updated
// strictly after the cursor.
func (s *Store) ListWorkspaceMemoriesSince(ctx context.Context, input ListWorkspaceMemoriesSinceInput) ([]MemoryRead, error) {
	workspaceID, err := uuid(input.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("memory: workspace_id: %w", err)
	}
	if input.Since.IsZero() {
		return nil, errors.New("memory: since cursor is required")
	}
	limit := input.Limit
	if limit <= 0 {
		limit = defaultReadLimit
	}
	rows, err := sqlc.New(s.db).ListWorkspaceMemoriesSince(ctx, sqlc.ListWorkspaceMemoriesSinceParams{
		WorkspaceID: workspaceID,
		Since:       timestamptz(input.Since.UTC()),
		ItemLimit:   limit,
	})
	if err != nil {
		return nil, fmt.Errorf("memory: list workspace since: %w", err)
	}
	out := make([]MemoryRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, memoryFromListWorkspaceSinceRow(r))
	}
	return out, nil
}

// UpdateMemory applies the full-replace edit. (zero, false, nil) when
// the row was already soft-deleted.
func (s *Store) UpdateMemory(ctx context.Context, input UpdateMemoryInput) (MemoryRead, bool, error) {
	id, err := uuid(input.ID)
	if err != nil {
		return MemoryRead{}, false, fmt.Errorf("memory: id: %w", err)
	}
	if input.Body == "" {
		return MemoryRead{}, false, errors.New("memory: body is required")
	}
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	row, err := sqlc.New(s.db).UpdateMemory(ctx, sqlc.UpdateMemoryParams{
		Title: strings.TrimSpace(input.Title),
		Body:  input.Body,
		Why:   input.Why,
		Tags:  normalizeTags(input.Tags),
		Now:   timestamptz(now),
		ID:    id,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MemoryRead{}, false, nil
		}
		return MemoryRead{}, false, fmt.Errorf("memory: update: %w", err)
	}
	return memoryFromUpdateRow(row), true, nil
}

// SoftDeleteMemory tombstones the row. Idempotent on already-deleted
// rows. Caller emits audit.
func (s *Store) SoftDeleteMemory(ctx context.Context, idRaw string, now time.Time) error {
	id, err := uuid(idRaw)
	if err != nil {
		return fmt.Errorf("memory: id: %w", err)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := sqlc.New(s.db).SoftDeleteMemory(ctx, sqlc.SoftDeleteMemoryParams{
		Now: timestamptz(now.UTC()),
		ID:  id,
	}); err != nil {
		return fmt.Errorf("memory: soft delete: %w", err)
	}
	return nil
}

func memoryFromInsertRow(r sqlc.InsertMemoryRow) MemoryRead {
	return MemoryRead{
		ID:             r.ID,
		Scope:          r.Scope,
		UserID:         r.UserID,
		WorkspaceID:    pgUUIDString(r.WorkspaceID),
		MemoryType:     r.MemoryType,
		Title:          r.Title,
		Body:           r.Body,
		Why:            r.Why,
		Tags:           r.Tags,
		Source:         r.Source,
		AgentActor:     r.AgentActor,
		ConversationID: pgUUIDString(r.ConversationID),
		CreatedAt:      pgTime(r.CreatedAt),
		UpdatedAt:      pgTime(r.UpdatedAt),
	}
}

func memoryFromGetRow(r sqlc.GetMemoryRow) MemoryRead {
	return MemoryRead{
		ID:             r.ID,
		Scope:          r.Scope,
		UserID:         r.UserID,
		WorkspaceID:    pgUUIDString(r.WorkspaceID),
		MemoryType:     r.MemoryType,
		Title:          r.Title,
		Body:           r.Body,
		Why:            r.Why,
		Tags:           r.Tags,
		Source:         r.Source,
		AgentActor:     r.AgentActor,
		ConversationID: pgUUIDString(r.ConversationID),
		CreatedAt:      pgTime(r.CreatedAt),
		UpdatedAt:      pgTime(r.UpdatedAt),
	}
}

func memoryFromListUserRow(r sqlc.ListUserMemoriesRow) MemoryRead {
	return MemoryRead{
		ID:             r.ID,
		Scope:          r.Scope,
		UserID:         r.UserID,
		WorkspaceID:    pgUUIDString(r.WorkspaceID),
		MemoryType:     r.MemoryType,
		Title:          r.Title,
		Body:           r.Body,
		Why:            r.Why,
		Tags:           r.Tags,
		Source:         r.Source,
		AgentActor:     r.AgentActor,
		ConversationID: pgUUIDString(r.ConversationID),
		CreatedAt:      pgTime(r.CreatedAt),
		UpdatedAt:      pgTime(r.UpdatedAt),
	}
}

func memoryFromListWorkspaceRow(r sqlc.ListWorkspaceMemoriesRow) MemoryRead {
	return MemoryRead{
		ID:             r.ID,
		Scope:          r.Scope,
		UserID:         r.UserID,
		WorkspaceID:    pgUUIDString(r.WorkspaceID),
		MemoryType:     r.MemoryType,
		Title:          r.Title,
		Body:           r.Body,
		Why:            r.Why,
		Tags:           r.Tags,
		Source:         r.Source,
		AgentActor:     r.AgentActor,
		ConversationID: pgUUIDString(r.ConversationID),
		CreatedAt:      pgTime(r.CreatedAt),
		UpdatedAt:      pgTime(r.UpdatedAt),
	}
}

func memoryFromListUserSinceRow(r sqlc.ListUserMemoriesSinceRow) MemoryRead {
	return MemoryRead{
		ID:             r.ID,
		Scope:          r.Scope,
		UserID:         r.UserID,
		WorkspaceID:    pgUUIDString(r.WorkspaceID),
		MemoryType:     r.MemoryType,
		Title:          r.Title,
		Body:           r.Body,
		Why:            r.Why,
		Tags:           r.Tags,
		Source:         r.Source,
		AgentActor:     r.AgentActor,
		ConversationID: pgUUIDString(r.ConversationID),
		CreatedAt:      pgTime(r.CreatedAt),
		UpdatedAt:      pgTime(r.UpdatedAt),
	}
}

func memoryFromListWorkspaceSinceRow(r sqlc.ListWorkspaceMemoriesSinceRow) MemoryRead {
	return MemoryRead{
		ID:             r.ID,
		Scope:          r.Scope,
		UserID:         r.UserID,
		WorkspaceID:    pgUUIDString(r.WorkspaceID),
		MemoryType:     r.MemoryType,
		Title:          r.Title,
		Body:           r.Body,
		Why:            r.Why,
		Tags:           r.Tags,
		Source:         r.Source,
		AgentActor:     r.AgentActor,
		ConversationID: pgUUIDString(r.ConversationID),
		CreatedAt:      pgTime(r.CreatedAt),
		UpdatedAt:      pgTime(r.UpdatedAt),
	}
}

func memoryFromUpdateRow(r sqlc.UpdateMemoryRow) MemoryRead {
	return MemoryRead{
		ID:             r.ID,
		Scope:          r.Scope,
		UserID:         r.UserID,
		WorkspaceID:    pgUUIDString(r.WorkspaceID),
		MemoryType:     r.MemoryType,
		Title:          r.Title,
		Body:           r.Body,
		Why:            r.Why,
		Tags:           r.Tags,
		Source:         r.Source,
		AgentActor:     r.AgentActor,
		ConversationID: pgUUIDString(r.ConversationID),
		CreatedAt:      pgTime(r.CreatedAt),
		UpdatedAt:      pgTime(r.UpdatedAt),
	}
}
