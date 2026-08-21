package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sqlc "github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	guuid "github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Values must match the CHECK constraint in migration 000008.
const (
	SandboxBindingStatusSpawning       = "spawning"
	SandboxBindingStatusActive         = "running"
	SandboxBindingStatusRenewing       = "renewing"
	SandboxBindingStatusKilling        = "killing"
	SandboxBindingStatusKilled         = "killed"
	SandboxBindingStatusKilledOrphaned = "killed_orphaned"
	SandboxBindingStatusKilledError    = "killed_error"
)

type SandboxBindingRead struct {
	ID                        string
	WorkspaceID               string
	AgentID                   *string
	Name                      *string
	CacheKey                  string
	SandboxID                 string
	TemplateID                string
	Status                    string
	CreatedAt                 time.Time
	LastActiveAt              time.Time
	KilledAt                  *time.Time
	TimeoutSeconds            int32
	AutoRenewThresholdSeconds int32
	ExpiresAt                 *time.Time
	Metadata                  map[string]any
}

type SandboxAutoRenewClaim struct {
	BindingID      string
	SandboxID      string
	AgentID        string
	TimeoutSeconds int32
}

// ConfigureSandboxBindingLease persists the provider lease requested for a
// newly-created bound sandbox. A zero threshold disables automatic renewal.
func (s *Store) ConfigureSandboxBindingLease(ctx context.Context, bindingID string, timeoutSeconds, thresholdSeconds int32, expiresAt time.Time) error {
	id, err := uuid(bindingID)
	if err != nil {
		return fmt.Errorf("sandbox binding lease: id: %w", err)
	}
	if timeoutSeconds <= 0 {
		return fmt.Errorf("sandbox binding lease: timeout_seconds must be > 0")
	}
	if thresholdSeconds < 0 {
		return fmt.Errorf("sandbox binding lease: threshold_seconds must be >= 0")
	}
	_, err = s.db.Exec(ctx, `
update sandboxes
set timeout_seconds = $2,
    auto_renew_threshold_seconds = $3,
    expires_at = $4,
    last_renewed_at = $5
where id = $1
  and allocation_status = 'bound'
  and killed_at is null`, id, timeoutSeconds, thresholdSeconds, timestamptz(expiresAt.UTC()), timestamptz(time.Now().UTC()))
	return err
}

// ClaimSandboxBindingsDueForAutoRenew atomically moves due bound sandboxes to
// renewing. FOR UPDATE SKIP LOCKED plus the state transition prevents two
// server pods from renewing the same provider sandbox in one scan. A claim
// abandoned by a crashed worker becomes eligible again after two minutes.
func (s *Store) ClaimSandboxBindingsDueForAutoRenew(ctx context.Context, now time.Time, limit int32) ([]SandboxAutoRenewClaim, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
with due as (
  select id
  from sandboxes
  where allocation_status = 'bound'
    and killed_at is null
    and metadata->>'sandbox_kind' = 'agent_daemon'
    and expires_at is not null
    and auto_renew_threshold_seconds > 0
	    and (
	      (lifecycle_status = 'running'
	       and expires_at <= $1::timestamptz + auto_renew_threshold_seconds * interval '1 second')
	      or
	      (lifecycle_status = 'renewing'
	       and coalesce(last_renewed_at, created_at) < $1::timestamptz - interval '2 minutes')
    )
  order by expires_at asc
  for update skip locked
  limit $2
)
update sandboxes s
set lifecycle_status = 'renewing',
    last_renewed_at = $1
from due
where s.id = due.id
returning s.id::text, s.sandbox_id, s.agent_id::text, s.timeout_seconds`, timestamptz(now.UTC()), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	claims := make([]SandboxAutoRenewClaim, 0)
	for rows.Next() {
		var claim SandboxAutoRenewClaim
		if err := rows.Scan(&claim.BindingID, &claim.SandboxID, &claim.AgentID, &claim.TimeoutSeconds); err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	return claims, rows.Err()
}

func (s *Store) CompleteSandboxBindingRenew(ctx context.Context, bindingID string, expiresAt time.Time) error {
	id, err := uuid(bindingID)
	if err != nil {
		return fmt.Errorf("complete sandbox renew: id: %w", err)
	}
	_, err = s.db.Exec(ctx, `
update sandboxes
set lifecycle_status = 'running',
    expires_at = $2,
    last_renewed_at = $3
where id = $1
  and lifecycle_status in ('running', 'renewing')
  and killed_at is null`, id, timestamptz(expiresAt.UTC()), timestamptz(time.Now().UTC()))
	return err
}

func (s *Store) FailSandboxBindingRenew(ctx context.Context, bindingID string) error {
	id, err := uuid(bindingID)
	if err != nil {
		return fmt.Errorf("fail sandbox renew: id: %w", err)
	}
	_, err = s.db.Exec(ctx, `
update sandboxes
set lifecycle_status = 'running',
    auto_renew_threshold_seconds = 0
where id = $1
  and lifecycle_status = 'renewing'
  and killed_at is null`, id)
	return err
}

// GetActiveSandboxBindingByAgentID is the cross-pod lifecycle lookup used by
// manual renew and automatic recovery. Agent IDs are globally unique.
func (s *Store) GetActiveSandboxBindingByAgentID(ctx context.Context, agentID string) (SandboxBindingRead, bool, error) {
	agentUUID, err := uuid(agentID)
	if err != nil {
		return SandboxBindingRead{}, false, fmt.Errorf("sandbox binding: agent_id: %w", err)
	}
	row := s.db.QueryRow(ctx, `
select id::text, workspace_id::text, agent_id, name, cache_key, sandbox_id,
       template_id, lifecycle_status, created_at, last_active_at, killed_at,
       timeout_seconds, auto_renew_threshold_seconds, expires_at, metadata
from sandboxes
where agent_id = $1
  and allocation_status = 'bound'
  and killed_at is null
limit 1`, agentUUID)
	var out SandboxBindingRead
	var agent pgtype.UUID
	var name, cacheKey pgtype.Text
	var killedAt, expiresAt pgtype.Timestamptz
	var metadata []byte
	if err := row.Scan(&out.ID, &out.WorkspaceID, &agent, &name, &cacheKey,
		&out.SandboxID, &out.TemplateID, &out.Status, &out.CreatedAt,
		&out.LastActiveAt, &killedAt, &out.TimeoutSeconds,
		&out.AutoRenewThresholdSeconds, &expiresAt, &metadata); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SandboxBindingRead{}, false, nil
		}
		return SandboxBindingRead{}, false, err
	}
	out.AgentID = nullableUUIDString(agent)
	out.Name = nullableText(name)
	out.CacheKey = cacheKey.String
	out.KilledAt = nullableTime(killedAt)
	out.ExpiresAt = nullableTime(expiresAt)
	out.Metadata = unmarshalJSONOrEmpty(metadata)
	return out, true, nil
}

type CreateSandboxBindingInput struct {
	WorkspaceID string
	AgentID     string
	CacheKey    string
	SandboxID   string
	TemplateID  string
	Status      string
	Metadata    map[string]any
}

// CreateSandboxBinding inserts a new active binding row. Caller must ensure no
// other active binding exists for the same (workspace, agent) —
// otherwise the partial unique index trips and the pg conflict error is
// returned verbatim.
func (s *Store) CreateSandboxBinding(ctx context.Context, input CreateSandboxBindingInput) (SandboxBindingRead, error) {
	workspaceUUID, err := uuid(input.WorkspaceID)
	if err != nil {
		return SandboxBindingRead{}, fmt.Errorf("sandbox binding: workspace_id: %w", err)
	}
	agentUUID, err := uuid(input.AgentID)
	if err != nil {
		return SandboxBindingRead{}, fmt.Errorf("sandbox binding: agent_id: %w", err)
	}
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = SandboxBindingStatusActive
	}
	metadata := input.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return SandboxBindingRead{}, fmt.Errorf("sandbox binding: marshal metadata: %w", err)
	}
	row, err := sqlc.New(s.db).CreateSandboxBinding(ctx, sqlc.CreateSandboxBindingParams{
		ID:          mustUUID(newID()),
		WorkspaceID: workspaceUUID,
		AgentID:     agentUUID,
		CacheKey:    pgtype.Text{String: strings.TrimSpace(input.CacheKey), Valid: true},
		SandboxID:   strings.TrimSpace(input.SandboxID),
		TemplateID:  strings.TrimSpace(input.TemplateID),
		Status:      status,
		Now:         timestamptz(time.Now().UTC()),
		Metadata:    metadataJSON,
	})
	if err != nil {
		return SandboxBindingRead{}, err
	}
	return sandboxBindingReadFromCreateRow(row), nil
}

type ReserveSandboxBindingSlotInput struct {
	WorkspaceID string
	AgentID     string
	CacheKey    string
	TemplateID  string
	Metadata    map[string]any
}

// PendingSandboxIDPrefix marks a sandboxes.sandbox_id that is a
// reservation placeholder rather than a real provider-issued id.
// ReserveSandboxBindingSlot has to write *something* unique to claim the
// slot before the provider has created the sandbox, so it writes
// "pending-<id>"; FinalizeSandboxBindingSpawning overwrites it.
//
// Callers that forward sandbox_id to a provider API must skip placeholder
// values — the provider will reject them as malformed.
const PendingSandboxIDPrefix = "pending-"

// IsPendingSandboxID reports whether id is a ReserveSandboxBindingSlot
// placeholder (see PendingSandboxIDPrefix) rather than a real sandbox id.
func IsPendingSandboxID(id string) bool {
	return strings.HasPrefix(strings.TrimSpace(id), PendingSandboxIDPrefix)
}

// ReserveSandboxBindingSlot is the cluster-wide cold-start coordinator: exactly
// one caller wins the (workspace, agent) slot via the partial unique
// index and drives cold-start; losers must call WaitForSandboxBindingActive.
// Returns (row, true, nil) on win, (existing row, false, nil) on conflict,
// (zero, false, err) on unrelated DB errors. sandbox_id is a placeholder
// (PendingSandboxIDPrefix + id) until FinalizeSandboxBindingSpawning lands.
func (s *Store) ReserveSandboxBindingSlot(ctx context.Context, input ReserveSandboxBindingSlotInput) (SandboxBindingRead, bool, error) {
	workspaceUUID, err := uuid(input.WorkspaceID)
	if err != nil {
		return SandboxBindingRead{}, false, fmt.Errorf("sandbox binding: workspace_id: %w", err)
	}
	agentUUID, err := uuid(input.AgentID)
	if err != nil {
		return SandboxBindingRead{}, false, fmt.Errorf("sandbox binding: agent_id: %w", err)
	}
	metadata := input.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return SandboxBindingRead{}, false, fmt.Errorf("sandbox binding: marshal metadata: %w", err)
	}
	placeholder := PendingSandboxIDPrefix + newID()
	row, err := sqlc.New(s.db).ReserveSandboxBindingSlot(ctx, sqlc.ReserveSandboxBindingSlotParams{
		ID:                   mustUUID(newID()),
		WorkspaceID:          workspaceUUID,
		AgentID:              agentUUID,
		CacheKey:             pgtype.Text{String: strings.TrimSpace(input.CacheKey), Valid: true},
		PlaceholderSandboxID: placeholder,
		TemplateID:           strings.TrimSpace(input.TemplateID),
		Now:                  timestamptz(time.Now().UTC()),
		Metadata:             metadataJSON,
	})
	if err == nil {
		return sandboxBindingReadFromReserveRow(row), true, nil
	}
	if !isUniqueViolation(err) {
		return SandboxBindingRead{}, false, err
	}
	existing, ok, lookupErr := s.getActiveSandboxBindingForAgentAnyStatus(ctx, input.WorkspaceID, input.AgentID)
	if lookupErr != nil {
		return SandboxBindingRead{}, false, fmt.Errorf("sandbox binding: lookup after conflict: %w", lookupErr)
	}
	if !ok {
		// Row killed between our INSERT and the lookup — caller can retry.
		return SandboxBindingRead{}, false, errors.New("sandbox binding: slot conflict but lookup found no live row; retry")
	}
	return existing, false, nil
}

type FinalizeSandboxBindingSpawningInput struct {
	BindingID string
	SandboxID string
	Metadata  map[string]any
}

// FinalizeSandboxBindingSpawning flips a reserved row from spawning → running
// and writes the real sandbox_id + final metadata. Idempotent: matches no rows
// once already finalised or killed, returning nil.
// SetSpawningSandboxBindingSandboxID records the real provider sandbox id
// on a still-spawning reservation, as soon as the sandbox is created and
// before the rest of cold start runs. Until this lands the row carries a
// pending- placeholder, so a crash between Create and Finalize would orphan
// a real, billing sandbox that reclaim cannot identify or kill. Writing the
// id early closes that window: reclaimAbandonedSpawn can then kill it.
//
// Scoped to lifecycle_status='spawning' so it never disturbs a row that has
// already been finalized, killed, or reclaimed by another pod.
func (s *Store) SetSpawningSandboxBindingSandboxID(ctx context.Context, bindingID, sandboxID string) error {
	id, err := uuid(bindingID)
	if err != nil {
		return fmt.Errorf("set spawning sandbox id: id: %w", err)
	}
	_, err = s.db.Exec(ctx, `
update sandboxes
set sandbox_id = $2
where id = $1
  and lifecycle_status = 'spawning'
  and killed_at is null`, id, strings.TrimSpace(sandboxID))
	return err
}

func (s *Store) FinalizeSandboxBindingSpawning(ctx context.Context, input FinalizeSandboxBindingSpawningInput) error {
	id, err := uuid(input.BindingID)
	if err != nil {
		return fmt.Errorf("sandbox binding: id: %w", err)
	}
	metadata := input.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("sandbox binding: marshal metadata: %w", err)
	}
	return sqlc.New(s.db).FinalizeSandboxBindingSpawning(ctx, sqlc.FinalizeSandboxBindingSpawningParams{
		SandboxID: strings.TrimSpace(input.SandboxID),
		Metadata:  metadataJSON,
		Now:       timestamptz(time.Now().UTC()),
		ID:        id,
	})
}

// WaitForSandboxBindingActive is the loser-side of ReserveSandboxBindingSlot.
// Polls until the holding row leaves `spawning`:
//   - running     → (row, nil)
//   - killed_*    → (zero, ErrSandboxBindingFailed); no auto-retry, to avoid
//     stampedes when the underlying failure is systemic.
//   - row vanished → ErrSandboxBindingVanished; caller retries Reserve.
//
// pollInterval ≤ 0 is normalised to 250ms.
func (s *Store) WaitForSandboxBindingActive(
	ctx context.Context,
	workspaceID, agentID string,
	pollInterval time.Duration,
) (SandboxBindingRead, error) {
	if pollInterval <= 0 {
		pollInterval = 250 * time.Millisecond
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		row, ok, err := s.getActiveSandboxBindingForAgentAnyStatus(ctx, workspaceID, agentID)
		if err != nil {
			return SandboxBindingRead{}, fmt.Errorf("sandbox binding: wait lookup: %w", err)
		}
		if !ok {
			return SandboxBindingRead{}, ErrSandboxBindingVanished
		}
		switch row.Status {
		case SandboxBindingStatusActive, SandboxBindingStatusRenewing:
			// Provider lease renewal does not interrupt the running daemon;
			// dispatch may keep using the sandbox while the renew call is in flight.
			return row, nil
		case SandboxBindingStatusSpawning, SandboxBindingStatusKilling:
		default:
			return SandboxBindingRead{}, fmt.Errorf("%w: status=%s", ErrSandboxBindingFailed, row.Status)
		}
		select {
		case <-ctx.Done():
			return SandboxBindingRead{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// getActiveSandboxBindingForAgentAnyStatus sees in-flight `spawning` rows that
// the admin-facing GetActiveSandboxBindingForAgent filters out.
func (s *Store) getActiveSandboxBindingForAgentAnyStatus(ctx context.Context, workspaceID, agentID string) (SandboxBindingRead, bool, error) {
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return SandboxBindingRead{}, false, fmt.Errorf("sandbox binding: workspace_id: %w", err)
	}
	agentUUID, err := uuid(agentID)
	if err != nil {
		return SandboxBindingRead{}, false, fmt.Errorf("sandbox binding: agent_id: %w", err)
	}
	row, err := sqlc.New(s.db).GetActiveSandboxBindingByAgentForWait(ctx, sqlc.GetActiveSandboxBindingByAgentForWaitParams{
		WorkspaceID: workspaceUUID,
		AgentID:     agentUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SandboxBindingRead{}, false, nil
		}
		return SandboxBindingRead{}, false, err
	}
	return sandboxBindingReadFromWaitRow(row), true, nil
}

// ErrSandboxBindingFailed marks a wait that resolved to a terminal killed_* state.
var ErrSandboxBindingFailed = errors.New("sandbox binding: holder failed cold-start")

// ErrSandboxBindingVanished marks the race where the holder soft-killed the
// reservation between our INSERT-conflict and the follow-up SELECT.
var ErrSandboxBindingVanished = errors.New("sandbox binding: reservation vanished mid-wait")

// GetActiveSandboxBindingForAgent returns the live binding for the
// (workspace, agent) tuple, or (zero, false, nil) when none exists.
func (s *Store) GetActiveSandboxBindingForAgent(ctx context.Context, workspaceID, agentID string) (SandboxBindingRead, bool, error) {
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return SandboxBindingRead{}, false, fmt.Errorf("sandbox binding: workspace_id: %w", err)
	}
	agentUUID, err := uuid(agentID)
	if err != nil {
		return SandboxBindingRead{}, false, fmt.Errorf("sandbox binding: agent_id: %w", err)
	}
	row, err := sqlc.New(s.db).GetActiveSandboxBindingForAgent(ctx, sqlc.GetActiveSandboxBindingForAgentParams{
		WorkspaceID: workspaceUUID,
		AgentID:     agentUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SandboxBindingRead{}, false, nil
		}
		return SandboxBindingRead{}, false, err
	}
	return sandboxBindingReadFromGetActiveRow(row), true, nil
}

// ListActiveSandboxBindings returns all live bindings in the workspace,
// ordered newest-active first. Limit defaults to defaultReadLimit when ≤ 0.
func (s *Store) ListActiveSandboxBindings(ctx context.Context, workspaceID string, limit int32) ([]SandboxBindingRead, error) {
	if limit <= 0 {
		limit = defaultReadLimit
	}
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("sandbox binding: workspace_id: %w", err)
	}
	rows, err := sqlc.New(s.db).ListActiveSandboxBindingsForWorkspace(ctx, sqlc.ListActiveSandboxBindingsForWorkspaceParams{
		WorkspaceID: workspaceUUID,
		LimitN:      limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]SandboxBindingRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, sandboxBindingReadFromListRow(r))
	}
	return out, nil
}

// TouchSandboxBinding bumps last_active_at by binding UUID. Best-effort —
// callers MUST NOT fail the prompt on error. Silent no-op when no row matches.
func (s *Store) TouchSandboxBinding(ctx context.Context, bindingID string) error {
	id, err := uuid(bindingID)
	if err != nil {
		return fmt.Errorf("sandbox binding: id: %w", err)
	}
	return sqlc.New(s.db).TouchSandboxBinding(ctx, sqlc.TouchSandboxBindingParams{
		Now: timestamptz(time.Now().UTC()),
		ID:  id,
	})
}

// TouchSandboxBindingByCacheKey is the cache-key variant used by Provider's
// OnCacheHit hook, which knows the abstract cache key but not the binding
// UUID. Best-effort, same semantics as TouchSandboxBinding.
func (s *Store) TouchSandboxBindingByCacheKey(ctx context.Context, cacheKey string) error {
	cacheKey = strings.TrimSpace(cacheKey)
	if cacheKey == "" {
		return errors.New("sandbox binding: cache_key empty")
	}
	return sqlc.New(s.db).TouchSandboxBindingByCacheKey(ctx, sqlc.TouchSandboxBindingByCacheKeyParams{
		Now:      timestamptz(time.Now().UTC()),
		CacheKey: pgtype.Text{String: cacheKey, Valid: true},
	})
}

// ListIdleSandboxBindings returns active bindings with last_active_at older
// than idleBefore, oldest-idle first. Server-global (no workspace filter).
// limit ≤ 0 is normalised to 1 so a bug can't turn this into a full-table scan.
func (s *Store) ListIdleSandboxBindings(ctx context.Context, idleBefore time.Time, limit int32) ([]SandboxBindingRead, error) {
	if limit <= 0 {
		limit = 1
	}
	rows, err := sqlc.New(s.db).ListIdleSandboxBindings(ctx, sqlc.ListIdleSandboxBindingsParams{
		IdleBefore: timestamptz(idleBefore.UTC()),
		LimitN:     limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]SandboxBindingRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, sandboxBindingReadFromIdleRow(r))
	}
	return out, nil
}

// MarkSandboxBindingKilled transitions a binding to a terminal status.
// Idempotent — the WHERE clause requires killed_at IS NULL.
func (s *Store) MarkSandboxBindingKilled(ctx context.Context, bindingID, status string) error {
	id, err := uuid(bindingID)
	if err != nil {
		return fmt.Errorf("sandbox binding: id: %w", err)
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = SandboxBindingStatusKilled
	}
	return sqlc.New(s.db).MarkSandboxBindingKilled(ctx, sqlc.MarkSandboxBindingKilledParams{
		Status: status,
		Now:    timestamptz(time.Now().UTC()),
		ID:     id,
	})
}

// ReclaimAbandonedSandboxBinding atomically retires a spawning reservation
// only when it is still spawning and was created before the caller's cutoff.
// The guarded UPDATE closes the read-then-write race with a concurrent
// FinalizeSandboxBindingSpawning: exactly one state transition can win.
func (s *Store) ReclaimAbandonedSandboxBinding(ctx context.Context, bindingID string, createdBefore time.Time) (bool, error) {
	id, err := uuid(bindingID)
	if err != nil {
		return false, fmt.Errorf("reclaim abandoned sandbox binding: id: %w", err)
	}
	now := time.Now().UTC()
	result, err := s.db.Exec(ctx, `
update sandboxes
set lifecycle_status  = 'killed_error',
    allocation_status = 'released',
    killed_at         = $3,
    last_active_at    = $3
where id = $1
  and lifecycle_status = 'spawning'
  and created_at <= $2
  and killed_at is null`, id, timestamptz(createdBefore.UTC()), timestamptz(now))
	if err != nil {
		return false, err
	}
	return result.RowsAffected() == 1, nil
}

// SweepOrphanedSandboxBindings marks every non-terminal binding at startup as
// killed_orphaned — the in-memory cache that held their envd tokens is gone.
// Returns the row count for startup logging.
func (s *Store) SweepOrphanedSandboxBindings(ctx context.Context) (int64, error) {
	return sqlc.New(s.db).SweepOrphanedSandboxBindings(ctx, timestamptz(time.Now().UTC()))
}

type sandboxBindingRow sqlc.CreateSandboxBindingRow

func sandboxBindingReadFromRow(r sandboxBindingRow) SandboxBindingRead {
	return SandboxBindingRead{
		ID:           r.ID,
		WorkspaceID:  r.WorkspaceID,
		AgentID:      nullableUUIDString(r.AgentID),
		Name:         nullableText(r.Name),
		CacheKey:     nullableTextValue(r.CacheKey),
		SandboxID:    r.SandboxID,
		TemplateID:   r.TemplateID,
		Status:       r.Status,
		CreatedAt:    r.CreatedAt.Time,
		LastActiveAt: r.LastActiveAt.Time,
		KilledAt:     nullableTime(r.KilledAt),
		Metadata:     unmarshalJSONOrEmpty(r.Metadata),
	}
}

func sandboxBindingReadFromCreateRow(r sqlc.CreateSandboxBindingRow) SandboxBindingRead {
	return sandboxBindingReadFromRow(sandboxBindingRow(r))
}

func sandboxBindingReadFromReserveRow(r sqlc.ReserveSandboxBindingSlotRow) SandboxBindingRead {
	return sandboxBindingReadFromRow(sandboxBindingRow(r))
}

func sandboxBindingReadFromWaitRow(r sqlc.GetActiveSandboxBindingByAgentForWaitRow) SandboxBindingRead {
	return sandboxBindingReadFromRow(sandboxBindingRow(r))
}

func sandboxBindingReadFromGetActiveRow(r sqlc.GetActiveSandboxBindingForAgentRow) SandboxBindingRead {
	return sandboxBindingReadFromRow(sandboxBindingRow(r))
}

func sandboxBindingReadFromListRow(r sqlc.ListActiveSandboxBindingsForWorkspaceRow) SandboxBindingRead {
	return sandboxBindingReadFromRow(sandboxBindingRow(r))
}

func sandboxBindingReadFromIdleRow(r sqlc.ListIdleSandboxBindingsRow) SandboxBindingRead {
	return sandboxBindingReadFromRow(sandboxBindingRow(r))
}

// unmarshalJSONOrEmpty decodes the metadata jsonb column, degrading to an
// empty map on malformed payload.
func unmarshalJSONOrEmpty(b []byte) map[string]any {
	if len(b) == 0 {
		return map[string]any{}
	}
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return out
}

func nullableTime(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

func nullableText(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	s := t.String
	return &s
}

func nullableTextValue(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

func nullableUUIDString(id pgtype.UUID) *string {
	if !id.Valid {
		return nil
	}
	s := guuid.UUID(id.Bytes).String()
	return &s
}
