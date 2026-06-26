package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	sqlc "github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// Values must match the CHECK constraint on the sandboxes table.
const (
	SandboxPoolEntryStatusIdle           = "idle"
	SandboxPoolEntryStatusRenewing       = "renewing"
	SandboxPoolEntryStatusClaimed        = "claimed"
	SandboxPoolEntryStatusKilled         = "killed"
	SandboxPoolEntryStatusKilledOrphaned = "killed_orphaned"
)

type SandboxPoolEntryRead struct {
	SandboxID                 string
	TemplateID                string
	Status                    string
	CreatedAt                 time.Time
	LastRenewedAt             time.Time
	KilledAt                  *time.Time
	ExpiresAt                 time.Time
	TimeoutSeconds            int32
	AutoRenewThresholdSeconds int32
}

// SandboxPoolAutoRenewCandidate is the row shape returned by the auto-renew
// scan. Caller filters in Go: renew any row where ExpiresAt.Sub(now) ≤
// AutoRenewThresholdSeconds.
type SandboxPoolAutoRenewCandidate struct {
	SandboxID                 string
	TemplateID                string
	ExpiresAt                 time.Time
	TimeoutSeconds            int32
	AutoRenewThresholdSeconds int32
}

// CreateSandboxPoolEntry persists a freshly-spawned blank pool sandbox.
// ON CONFLICT DO NOTHING leaves a stranded row from a previous server lifetime
// alone — startup sweep reaps it.
func (s *Store) CreateSandboxPoolEntry(ctx context.Context, workspaceID, sandboxID, templateID string, expiresAt time.Time, timeoutSeconds int32) error {
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return fmt.Errorf("sandbox pool: workspace_id: %w", err)
	}
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return fmt.Errorf("sandbox pool: sandbox_id is required")
	}
	templateID = strings.TrimSpace(templateID)
	if templateID == "" {
		return fmt.Errorf("sandbox pool: template_id is required")
	}
	if timeoutSeconds <= 0 {
		return fmt.Errorf("sandbox pool: timeout_seconds must be > 0, got %d", timeoutSeconds)
	}
	if expiresAt.IsZero() {
		return fmt.Errorf("sandbox pool: expires_at is required")
	}
	now := time.Now().UTC()
	return sqlc.New(s.db).CreateSandboxPoolEntry(ctx, sqlc.CreateSandboxPoolEntryParams{
		WorkspaceID:    workspaceUUID,
		SandboxID:      sandboxID,
		TemplateID:     templateID,
		Now:            timestamptz(now),
		ExpiresAt:      timestamptz(expiresAt.UTC()),
		TimeoutSeconds: timeoutSeconds,
	})
}

// TouchSandboxPoolRenewed bumps last_renewed_at and rolls expires_at forward
// after a successful Renew. Idempotent against rows killed concurrently.
func (s *Store) TouchSandboxPoolRenewed(ctx context.Context, sandboxID string, expiresAt time.Time) error {
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return fmt.Errorf("sandbox pool: sandbox_id is required")
	}
	if expiresAt.IsZero() {
		return fmt.Errorf("sandbox pool: expires_at is required")
	}
	return sqlc.New(s.db).TouchSandboxPoolRenewed(ctx, sqlc.TouchSandboxPoolRenewedParams{
		SandboxID: sandboxID,
		Now:       timestamptz(time.Now().UTC()),
		ExpiresAt: timestamptz(expiresAt.UTC()),
	})
}

// SetSandboxPoolAutoRenewThreshold sets the remaining-lifetime auto-renew
// trigger in seconds. 0 disables auto-renew; negative rejected.
func (s *Store) SetSandboxPoolAutoRenewThreshold(ctx context.Context, sandboxID string, thresholdSeconds int32) error {
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return fmt.Errorf("sandbox pool: sandbox_id is required")
	}
	if thresholdSeconds < 0 {
		return fmt.Errorf("sandbox pool: threshold_seconds must be >= 0, got %d", thresholdSeconds)
	}
	return sqlc.New(s.db).SetSandboxPoolAutoRenewThreshold(ctx, sqlc.SetSandboxPoolAutoRenewThresholdParams{
		SandboxID:        sandboxID,
		ThresholdSeconds: thresholdSeconds,
	})
}

// MarkSandboxPoolEntryClaimed transitions an idle entry to claimed and bumps
// last_renewed_at. killed_at stays NULL — the sandbox is still alive inside
// a Persistent binding and must remain visible in admin until really killed.
func (s *Store) MarkSandboxPoolEntryClaimed(ctx context.Context, workspaceID, projectAgentID, cacheKey, sandboxID string) error {
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return fmt.Errorf("sandbox pool: workspace_id: %w", err)
	}
	projectAgentUUID, err := uuid(projectAgentID)
	if err != nil {
		return fmt.Errorf("sandbox pool: project_agent_id: %w", err)
	}
	cacheKey = strings.TrimSpace(cacheKey)
	if cacheKey == "" {
		return fmt.Errorf("sandbox pool: cache_key is required")
	}
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return fmt.Errorf("sandbox pool: sandbox_id is required")
	}
	return sqlc.New(s.db).MarkSandboxPoolEntryClaimed(ctx, sqlc.MarkSandboxPoolEntryClaimedParams{
		WorkspaceID:    workspaceUUID,
		ProjectAgentID: projectAgentUUID,
		CacheKey:       pgtype.Text{String: cacheKey, Valid: true},
		SandboxID:      sandboxID,
		Now:            timestamptz(time.Now().UTC()),
	})
}

// MarkSandboxPoolEntryKilled is the terminal kill transition. status must be
// 'killed' or 'killed_orphaned'. Idempotent against already-killed rows.
func (s *Store) MarkSandboxPoolEntryKilled(ctx context.Context, sandboxID, status string) error {
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return fmt.Errorf("sandbox pool: sandbox_id is required")
	}
	status = strings.TrimSpace(status)
	switch status {
	case SandboxPoolEntryStatusKilled, SandboxPoolEntryStatusKilledOrphaned:
	default:
		return fmt.Errorf("sandbox pool: status must be killed or killed_orphaned, got %q", status)
	}
	return sqlc.New(s.db).MarkSandboxPoolEntryKilled(ctx, sqlc.MarkSandboxPoolEntryKilledParams{
		SandboxID: sandboxID,
		Status:    status,
		Now:       timestamptz(time.Now().UTC()),
	})
}

// GetSandboxPoolEntry reads a single row by sandbox_id, including killed rows.
// Returns (entry, true, nil) on hit, (zero, false, nil) on miss.
func (s *Store) GetSandboxPoolEntry(ctx context.Context, workspaceID, sandboxID string) (SandboxPoolEntryRead, bool, error) {
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return SandboxPoolEntryRead{}, false, fmt.Errorf("sandbox pool: workspace_id: %w", err)
	}
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return SandboxPoolEntryRead{}, false, fmt.Errorf("sandbox pool: sandbox_id is required")
	}
	row, err := sqlc.New(s.db).GetSandboxPoolEntry(ctx, sqlc.GetSandboxPoolEntryParams{WorkspaceID: workspaceUUID, SandboxID: sandboxID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SandboxPoolEntryRead{}, false, nil
		}
		return SandboxPoolEntryRead{}, false, err
	}
	return sandboxPoolRowToRead(row.SandboxID, row.TemplateID, row.Status,
		row.CreatedAt, row.LastRenewedAt, row.KilledAt,
		row.ExpiresAt, row.TimeoutSeconds, row.AutoRenewThresholdSeconds), true, nil
}

// ListActiveSandboxPoolEntries returns active rows newest-first, paginated.
// Limit defaults to 50 when ≤ 0; offset defaults to 0 when < 0.
func (s *Store) ListActiveSandboxPoolEntries(ctx context.Context, workspaceID string, limit, offset int32) ([]SandboxPoolEntryRead, error) {
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("sandbox pool: workspace_id: %w", err)
	}
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := sqlc.New(s.db).ListActiveSandboxPoolEntries(ctx, sqlc.ListActiveSandboxPoolEntriesParams{
		WorkspaceID: workspaceUUID,
		LimitN:      limit,
		OffsetN:     offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]SandboxPoolEntryRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, sandboxPoolRowToRead(r.SandboxID, r.TemplateID, r.Status,
			r.CreatedAt, r.LastRenewedAt, r.KilledAt,
			r.ExpiresAt, r.TimeoutSeconds, r.AutoRenewThresholdSeconds))
	}
	return out, nil
}

// CountActiveSandboxPoolEntries returns the total used by the admin paginator.
func (s *Store) CountActiveSandboxPoolEntries(ctx context.Context, workspaceID string) (int64, error) {
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return 0, fmt.Errorf("sandbox pool: workspace_id: %w", err)
	}
	return sqlc.New(s.db).CountActiveSandboxPoolEntries(ctx, workspaceUUID)
}

// ListSandboxPoolEntriesDueForAutoRenew returns every active row with
// auto-renew enabled (threshold > 0), ordered by soonest expiry. Caller
// filters in Go because sqlc's parser mangles `now + N * interval '1 second'`
// named-param names; pool size is bounded so returning all candidates is cheap.
func (s *Store) ListSandboxPoolEntriesDueForAutoRenew(ctx context.Context, workspaceID string) ([]SandboxPoolAutoRenewCandidate, error) {
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("sandbox pool: workspace_id: %w", err)
	}
	rows, err := sqlc.New(s.db).ListSandboxPoolEntriesDueForAutoRenew(ctx, workspaceUUID)
	if err != nil {
		return nil, err
	}
	out := make([]SandboxPoolAutoRenewCandidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, SandboxPoolAutoRenewCandidate{
			SandboxID:                 r.SandboxID,
			TemplateID:                r.TemplateID,
			ExpiresAt:                 r.ExpiresAt.Time,
			TimeoutSeconds:            r.TimeoutSeconds,
			AutoRenewThresholdSeconds: r.AutoRenewThresholdSeconds,
		})
	}
	return out, nil
}

// SweepOrphanedSandboxPoolEntries marks every active pool entry from a prior
// server lifetime as killed_orphaned. The e2b-side timeout reaps the actual
// sandboxes. Returns the row count for startup logging.
func (s *Store) SweepOrphanedSandboxPoolEntries(ctx context.Context) (int64, error) {
	return sqlc.New(s.db).SweepOrphanedSandboxPoolEntries(ctx, timestamptz(time.Now().UTC()))
}

func sandboxPoolRowToRead(
	sandboxID, templateID, status string,
	createdAt, lastRenewedAt, killedAt, expiresAt pgtype.Timestamptz,
	timeoutSeconds, autoRenewThresholdSeconds int32,
) SandboxPoolEntryRead {
	entry := SandboxPoolEntryRead{
		SandboxID:                 sandboxID,
		TemplateID:                templateID,
		Status:                    status,
		CreatedAt:                 createdAt.Time,
		LastRenewedAt:             lastRenewedAt.Time,
		ExpiresAt:                 expiresAt.Time,
		TimeoutSeconds:            timeoutSeconds,
		AutoRenewThresholdSeconds: autoRenewThresholdSeconds,
	}
	if killedAt.Valid {
		t := killedAt.Time
		entry.KilledAt = &t
	}
	return entry
}
