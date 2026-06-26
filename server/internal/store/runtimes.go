package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	sqlc "github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// Runtime type / liveness / provider constants mirror DB CHECK
// constraints; using constants surfaces drift between Go and SQL as a
// compile error.
const (
	RuntimeTypeAgentDaemon = "agent_daemon"
	RuntimeTypeSandbox     = "sandbox"
	RuntimeTypeExternal    = "external"

	RuntimeLivenessPendingPairing = "pending_pairing"
	RuntimeLivenessOffline        = "offline"
	RuntimeLivenessOnline         = "online"
	RuntimeLivenessError          = "error"

	RuntimeProviderAgentDaemon        = "agent_daemon"
	RuntimeProviderAgentDaemonSandbox = "agent_daemon_sandbox"
	RuntimeProviderE2BCompatible      = "e2b_compatible"
	RuntimeProviderHTTPAgent          = "http_agent"
)

// ErrPairingTokenInvalid is returned by ConsumePairingToken when no
// matching pending_pairing row exists. API layer maps this to 401 so
// attackers can't distinguish reasons.
var ErrPairingTokenInvalid = errors.New("runtime pairing token invalid or expired")

var ErrRuntimeNotFound = errors.New("runtime not found")

// ErrRuntimeNameTaken signals a collision on uk_runtimes_workspace_name_active.
// API layer maps this to 409 instead of leaking the raw SQLSTATE 23505 text.
var ErrRuntimeNameTaken = errors.New("runtime name already in use in this workspace")

// RuntimeRead is the read-side shape returned by every runtime helper.
// Config is pre-decoded jsonb for caller convenience.
type RuntimeRead struct {
	ID                    string
	WorkspaceID           string
	Type                  string
	Name                  string
	Liveness              string
	Provider              string
	OwnerUserID           *string
	Version               string
	Hostname              string
	LastHeartbeatAt       *time.Time
	PairingTokenExpiresAt *time.Time
	Config                map[string]any
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// CreateRuntimePairingInput is the admin-side create payload. The
// pairing token is generated server-side. TokenTTL=0 falls back to
// RuntimePairingTokenTTL.
type CreateRuntimePairingInput struct {
	WorkspaceID string
	Type        string
	Name        string
	Provider    string
	OwnerUserID string
	ActorID     string
	TokenTTL    time.Duration
	Config      map[string]any
}

// CreateRuntimePairingResult bundles the new row with the plaintext
// pairing token. Callers MUST return the token to the API client
// exactly once and never log / persist it.
type CreateRuntimePairingResult struct {
	Runtime      RuntimeRead
	PairingToken string
}

// CreateRuntimePairing mints a runtime row in pending_pairing state,
// returning the activated row plus the one-shot pairing token.
func (s *Store) CreateRuntimePairing(ctx context.Context, input CreateRuntimePairingInput) (CreateRuntimePairingResult, error) {
	workspaceUUID, err := uuid(input.WorkspaceID)
	if err != nil {
		return CreateRuntimePairingResult{}, fmt.Errorf("runtime: workspace_id: %w", err)
	}
	if input.Type == "" {
		return CreateRuntimePairingResult{}, errors.New("runtime: type required")
	}
	if input.Provider == "" {
		return CreateRuntimePairingResult{}, errors.New("runtime: provider required")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return CreateRuntimePairingResult{}, errors.New("runtime: name required")
	}
	ttl := input.TokenTTL
	if ttl <= 0 {
		ttl = RuntimePairingTokenTTL
	}

	plaintext, hash, err := MintRuntimePairingToken()
	if err != nil {
		return CreateRuntimePairingResult{}, err
	}

	now := time.Now().UTC()
	configJSON, err := marshalJSONOrEmpty(input.Config)
	if err != nil {
		return CreateRuntimePairingResult{}, fmt.Errorf("runtime: marshal config: %w", err)
	}

	row, err := sqlc.New(s.db).CreateRuntimePairing(ctx, sqlc.CreateRuntimePairingParams{
		ID:                    mustUUID(newID()),
		WorkspaceID:           workspaceUUID,
		Type:                  input.Type,
		Name:                  name,
		Provider:              input.Provider,
		OwnerUserID:           nullableUUID(input.OwnerUserID),
		PairingTokenHash:      pgtype.Text{String: hash, Valid: true},
		PairingTokenExpiresAt: pgtype.Timestamptz{Time: now.Add(ttl), Valid: true},
		Config:                configJSON,
		Now:                   timestamptz(now),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return CreateRuntimePairingResult{}, ErrRuntimeNameTaken
		}
		return CreateRuntimePairingResult{}, err
	}
	result := CreateRuntimePairingResult{
		Runtime:      runtimeReadFromCreateRow(row),
		PairingToken: plaintext,
	}
	payload := runtimeAuditPayload(result.Runtime)
	payload["liveness"] = result.Runtime.Liveness
	s.emitRuntimeAdminAudit(now, input.ActorID, auditRuntimeCreated, result.Runtime, payload)
	return result, nil
}

// ConsumePairingTokenInput is the daemon-side pair payload. The
// plaintext token is hashed inside the call.
type ConsumePairingTokenInput struct {
	Token           string
	Hostname        string
	Version         string
	RunnerPublicKey string
}

// ConsumePairingToken matches the token against a pending_pairing row
// and promotes it to offline. Atomic — concurrent pair calls cannot
// both consume the same token because the UPDATE WHERE clause checks
// status='pending_pairing'.
//
// Stores the daemon-supplied public key under config.runner_public_key.
// Admin-set config keys present at create-pairing time are preserved
// via SQL-side jsonb-concat.
func (s *Store) ConsumePairingToken(ctx context.Context, input ConsumePairingTokenInput) (RuntimeRead, error) {
	if strings.TrimSpace(input.Token) == "" {
		return RuntimeRead{}, ErrPairingTokenInvalid
	}
	hash := HashRuntimePairingToken(input.Token)
	hostname := strings.TrimSpace(input.Hostname)
	version := strings.TrimSpace(input.Version)
	pubkey := strings.TrimSpace(input.RunnerPublicKey)
	if pubkey == "" {
		return RuntimeRead{}, errors.New("runtime: runner_public_key required")
	}

	cfg := map[string]any{
		"runner_public_key": pubkey,
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return RuntimeRead{}, fmt.Errorf("runtime: marshal config: %w", err)
	}

	now := time.Now().UTC()
	row, err := sqlc.New(s.db).ConsumePairingToken(ctx, sqlc.ConsumePairingTokenParams{
		PairingTokenHash: pgtype.Text{String: hash, Valid: true},
		Hostname:         hostname,
		Version:          version,
		Config:           cfgJSON,
		Now:              timestamptz(now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RuntimeRead{}, ErrPairingTokenInvalid
		}
		return RuntimeRead{}, err
	}
	runtime := runtimeReadFromConsumeRow(row)
	payload := runtimeAuditPayload(runtime)
	payload["liveness"] = runtime.Liveness
	s.emitRuntimeLifecycleAudit(now, auditRuntimePaired, runtime, payload)
	return runtime, nil
}

// HeartbeatStatus is the post-heartbeat liveness the runner uses to
// detect state changes.
type HeartbeatStatus struct {
	Liveness string
	// Deleted is true when the heartbeat UPDATE matched zero rows,
	// meaning the runtime was soft-deleted (or never existed). The
	// gateway uses this to send a permanent WS close frame so the
	// daemon stops reconnecting.
	Deleted bool
}

// AgentDaemonKindCapabilities mirrors the daemon heartbeat capability
// shape after gateway-level normalization. Lives in store rather than
// importing the wire proto package so runtime config persistence stays
// decoupled from transport structs.
type AgentDaemonKindCapabilities struct {
	Streaming   bool `json:"streaming,omitempty"`
	Permissions bool `json:"permissions,omitempty"`
	Usage       bool `json:"usage,omitempty"`
	Resume      bool `json:"resume,omitempty"`
}

// AgentDaemonSupportedAgentKind is the sanitized runtime.config view
// of one daemon-side agent_kind.
type AgentDaemonSupportedAgentKind struct {
	Kind         string                      `json:"kind"`
	Available    bool                        `json:"available"`
	Version      string                      `json:"version,omitempty"`
	Capabilities AgentDaemonKindCapabilities `json:"capabilities,omitempty"`
}

// TouchAgentDaemonHeartbeatInput is the WebSocket daemon heartbeat
// payload after gateway normalization.
type TouchAgentDaemonHeartbeatInput struct {
	RuntimeID           string
	DaemonVersion       string
	ActiveRequests      int
	HeartbeatTimestamp  int64
	SupportedAgentKinds []AgentDaemonSupportedAgentKind
}

// TouchRuntimeHeartbeat bumps last_heartbeat_at and promotes
// offline/error -> online. Deleted rows are not promoted; returns
// offline.
func (s *Store) TouchRuntimeHeartbeat(ctx context.Context, runtimeID string) (HeartbeatStatus, error) {
	id, err := uuid(runtimeID)
	if err != nil {
		return HeartbeatStatus{}, fmt.Errorf("runtime: id: %w", err)
	}
	var before RuntimeRead
	beforeOK := false
	if s.audit != nil {
		before, beforeOK, err = s.GetRuntime(ctx, runtimeID)
		if err != nil {
			return HeartbeatStatus{}, err
		}
	}
	now := time.Now().UTC()
	row, err := sqlc.New(s.db).TouchRuntimeHeartbeat(ctx, sqlc.TouchRuntimeHeartbeatParams{
		Now: timestamptz(now),
		ID:  id,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return HeartbeatStatus{Liveness: RuntimeLivenessOffline, Deleted: true}, nil
		}
		return HeartbeatStatus{}, err
	}
	status := HeartbeatStatus{Liveness: row.Liveness}
	if s.audit != nil && beforeOK && before.Liveness != RuntimeLivenessOnline && status.Liveness == RuntimeLivenessOnline {
		payload := runtimeAuditPayload(before)
		payload["from_liveness"] = before.Liveness
		payload["to_liveness"] = status.Liveness
		s.emitRuntimeLifecycleAudit(now, auditRuntimeOnline, before, payload)
	}
	return status, nil
}

// TouchAgentDaemonHeartbeat bumps an agent_daemon runtime heartbeat and
// persists the daemon-advertised agent_kind capability snapshot. The
// full supported_agent_kinds array is retained for diagnosis;
// supported_agent_kind_names contains only available kinds and powers
// UI filtering.
func (s *Store) TouchAgentDaemonHeartbeat(ctx context.Context, input TouchAgentDaemonHeartbeatInput) (HeartbeatStatus, error) {
	id, err := uuid(input.RuntimeID)
	if err != nil {
		return HeartbeatStatus{}, fmt.Errorf("runtime: id: %w", err)
	}

	var before RuntimeRead
	beforeOK := false
	if s.audit != nil {
		before, beforeOK, err = s.GetRuntime(ctx, input.RuntimeID)
		if err != nil {
			return HeartbeatStatus{}, err
		}
	}

	now := time.Now().UTC()
	heartbeatTS := input.HeartbeatTimestamp
	if heartbeatTS <= 0 {
		heartbeatTS = now.Unix()
	}
	activeRequests := input.ActiveRequests
	if activeRequests < 0 {
		activeRequests = 0
	}
	if activeRequests > 1<<31-1 {
		activeRequests = 1<<31 - 1
	}

	kinds, names, capabilities := normalizeAgentDaemonKindSnapshot(input.SupportedAgentKinds)
	kindsJSON, err := json.Marshal(kinds)
	if err != nil {
		return HeartbeatStatus{}, fmt.Errorf("runtime: marshal supported_agent_kinds: %w", err)
	}
	namesJSON, err := json.Marshal(names)
	if err != nil {
		return HeartbeatStatus{}, fmt.Errorf("runtime: marshal supported_agent_kind_names: %w", err)
	}
	capabilitiesJSON, err := json.Marshal(capabilities)
	if err != nil {
		return HeartbeatStatus{}, fmt.Errorf("runtime: marshal daemon_capabilities: %w", err)
	}

	row, err := sqlc.New(s.db).TouchAgentDaemonHeartbeat(ctx, sqlc.TouchAgentDaemonHeartbeatParams{
		Now:                     timestamptz(now),
		DaemonVersion:           strings.TrimSpace(input.DaemonVersion),
		SupportedAgentKinds:     kindsJSON,
		SupportedAgentKindNames: namesJSON,
		DaemonCapabilities:      capabilitiesJSON,
		ActiveRequests:          int32(activeRequests),
		HeartbeatTs:             heartbeatTS,
		ID:                      id,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return HeartbeatStatus{Liveness: RuntimeLivenessOffline, Deleted: true}, nil
		}
		return HeartbeatStatus{}, err
	}
	status := HeartbeatStatus{Liveness: row.Liveness}
	if s.audit != nil && beforeOK && before.Liveness != RuntimeLivenessOnline && status.Liveness == RuntimeLivenessOnline {
		payload := runtimeAuditPayload(before)
		payload["from_liveness"] = before.Liveness
		payload["to_liveness"] = status.Liveness
		s.emitRuntimeLifecycleAudit(now, auditRuntimeOnline, before, payload)
	}
	return status, nil
}

func normalizeAgentDaemonKindSnapshot(input []AgentDaemonSupportedAgentKind) ([]AgentDaemonSupportedAgentKind, []string, map[string]bool) {
	byKind := map[string]AgentDaemonSupportedAgentKind{}
	for _, in := range input {
		kind := strings.TrimSpace(in.Kind)
		if kind == "" {
			continue
		}
		in.Kind = kind
		in.Version = strings.TrimSpace(in.Version)
		byKind[kind] = in
	}
	kinds := make([]AgentDaemonSupportedAgentKind, 0, len(byKind))
	for _, info := range byKind {
		kinds = append(kinds, info)
	}
	slices.SortFunc(kinds, func(a, b AgentDaemonSupportedAgentKind) int {
		return strings.Compare(a.Kind, b.Kind)
	})

	names := make([]string, 0, len(kinds))
	capabilities := map[string]bool{
		"streaming":    false,
		"cancellation": false,
		"permissions":  false,
		"usage":        false,
		"resume":       false,
		"artifacts":    false,
	}
	for _, info := range kinds {
		if !info.Available {
			continue
		}
		names = append(names, info.Kind)
		capabilities["cancellation"] = true
		capabilities["streaming"] = capabilities["streaming"] || info.Capabilities.Streaming
		capabilities["permissions"] = capabilities["permissions"] || info.Capabilities.Permissions
		capabilities["usage"] = capabilities["usage"] || info.Capabilities.Usage
		capabilities["resume"] = capabilities["resume"] || info.Capabilities.Resume
	}
	return kinds, names, capabilities
}

// GetRuntime returns the runtime by id. (zero, false, nil) when no
// matching active row exists.
func (s *Store) GetRuntime(ctx context.Context, runtimeID string) (RuntimeRead, bool, error) {
	id, err := uuid(runtimeID)
	if err != nil {
		return RuntimeRead{}, false, fmt.Errorf("runtime: id: %w", err)
	}
	row, err := sqlc.New(s.db).GetRuntime(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RuntimeRead{}, false, nil
		}
		return RuntimeRead{}, false, err
	}
	return runtimeReadFromGetRow(row), true, nil
}

// ListRuntimes returns active runtimes for a workspace, optionally
// filtered by type. Empty typeFilter means all types. Limit defaults
// to defaultReadLimit when <= 0.
func (s *Store) ListRuntimes(ctx context.Context, workspaceID, typeFilter string, limit int32) ([]RuntimeRead, error) {
	if limit <= 0 {
		limit = defaultReadLimit
	}
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("runtime: workspace_id: %w", err)
	}
	rows, err := sqlc.New(s.db).ListRuntimesByWorkspace(ctx, sqlc.ListRuntimesByWorkspaceParams{
		WorkspaceID: workspaceUUID,
		Type:        typeFilter,
		LimitN:      limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]RuntimeRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, runtimeReadFromListRow(r))
	}
	return out, nil
}

// PatchRuntimeInput represents the admin PATCH payload. Empty string
// means "do not change" — matches the SQL semantics.
type PatchRuntimeInput struct {
	ID      string
	NewName string
	ActorID string
}

func (s *Store) PatchRuntime(ctx context.Context, input PatchRuntimeInput) (RuntimeRead, error) {
	id, err := uuid(input.ID)
	if err != nil {
		return RuntimeRead{}, fmt.Errorf("runtime: id: %w", err)
	}
	before, ok, err := s.GetRuntime(ctx, input.ID)
	if err != nil {
		return RuntimeRead{}, err
	}
	if !ok {
		return RuntimeRead{}, ErrRuntimeNotFound
	}
	now := time.Now().UTC()
	row, err := sqlc.New(s.db).PatchRuntime(ctx, sqlc.PatchRuntimeParams{
		ID:      id,
		NewName: input.NewName,
		Now:     timestamptz(now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RuntimeRead{}, ErrRuntimeNotFound
		}
		if isUniqueViolation(err) {
			return RuntimeRead{}, ErrRuntimeNameTaken
		}
		return RuntimeRead{}, err
	}
	after := runtimeReadFromPatchRow(row)
	if before.Name != after.Name {
		payload := runtimeAuditPayload(after)
		payload["name_from"] = before.Name
		payload["name_to"] = after.Name
		s.emitRuntimeAdminAudit(now, input.ActorID, auditRuntimeUpdated, after, payload)
	}
	return after, nil
}

// SoftDeleteRuntimeByWorkspaceName retires any active runtime with the
// given workspace + name so a replacement can be minted without hitting
// the uk_runtimes_workspace_name_active unique constraint.
func (s *Store) SoftDeleteRuntimeByWorkspaceName(ctx context.Context, workspaceID, name string) error {
	wid, err := uuid(workspaceID)
	if err != nil {
		return fmt.Errorf("runtime: workspace_id: %w", err)
	}
	return sqlc.New(s.db).SoftDeleteRuntimeByWorkspaceName(ctx, sqlc.SoftDeleteRuntimeByWorkspaceNameParams{
		WorkspaceID: wid,
		Name:        name,
		Now:         timestamptz(time.Now().UTC()),
	})
}

// SoftDeleteRuntime marks the runtime deleted_at + disabled. Soft
// delete (not hard) so historical Run Detail can still display
// "ran on <runtime name> (deleted)" via the agent_runs.runtime_id FK.
func (s *Store) SoftDeleteRuntime(ctx context.Context, runtimeID string) error {
	return s.SoftDeleteRuntimeWithActor(ctx, runtimeID, "")
}

func (s *Store) SoftDeleteRuntimeWithActor(ctx context.Context, runtimeID, actorID string) error {
	id, err := uuid(runtimeID)
	if err != nil {
		return fmt.Errorf("runtime: id: %w", err)
	}
	current, ok, err := s.GetRuntime(ctx, runtimeID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	if err := sqlc.New(s.db).SoftDeleteRuntime(ctx, sqlc.SoftDeleteRuntimeParams{
		ID:  id,
		Now: timestamptz(now),
	}); err != nil {
		return err
	}
	payload := runtimeAuditPayload(current)
	payload["liveness"] = current.Liveness
	s.emitRuntimeAdminAudit(now, actorID, auditRuntimeDeleted, current, payload)
	return nil
}

// SweepStaleRuntimes demotes online runtimes whose last_heartbeat_at
// is older than the cutoff. Returns the swept row count for telemetry.
// Server-global (no workspace filter).
func (s *Store) SweepStaleRuntimes(ctx context.Context, cutoff time.Time) (int64, error) {
	return sqlc.New(s.db).SweepStaleRuntimesToOffline(ctx, sqlc.SweepStaleRuntimesToOfflineParams{
		Cutoff: timestamptz(cutoff.UTC()),
		Now:    timestamptz(time.Now().UTC()),
	})
}

// MarkRuntimeOffline immediately transitions a runtime from online to
// offline. Called on WebSocket session close. Idempotent.
func (s *Store) MarkRuntimeOffline(ctx context.Context, runtimeID string) error {
	id, err := uuid(runtimeID)
	if err != nil {
		return fmt.Errorf("runtime: id: %w", err)
	}
	return sqlc.New(s.db).MarkRuntimeOffline(ctx, sqlc.MarkRuntimeOfflineParams{
		ID:  id,
		Now: timestamptz(time.Now().UTC()),
	})
}

// SetRuntimeRunnerCredentialHash persists the post-pair-handshake
// long-lived bearer hash under runtimes.config.runner_credential_hash.
// jsonb-concat semantics preserve runner_public_key and other admin-set
// keys.
func (s *Store) SetRuntimeRunnerCredentialHash(ctx context.Context, runtimeID, hash string) error {
	id, err := uuid(runtimeID)
	if err != nil {
		return fmt.Errorf("runtime: id: %w", err)
	}
	return sqlc.New(s.db).SetRuntimeRunnerCredentialHash(ctx, sqlc.SetRuntimeRunnerCredentialHashParams{
		Hash: hash,
		Now:  timestamptz(time.Now().UTC()),
		ID:   id,
	})
}

func (s *Store) emitRuntimeAdminAudit(now time.Time, actorID, eventType string, runtime RuntimeRead, payload map[string]any) {
	actorID = strings.TrimSpace(actorID)
	actorType := audit.ActorTypeUser
	if actorID == "" {
		actorType = audit.ActorTypeSystem
	}
	s.emitAuditEvent(audit.Event{
		OccurredAt:  now,
		Source:      audit.SourceAdmin,
		EventType:   eventType,
		ActorType:   actorType,
		ActorID:     actorID,
		TargetType:  "runtime",
		TargetID:    runtime.ID,
		WorkspaceID: runtime.WorkspaceID,
		Payload:     payload,
	})
}

func (s *Store) emitRuntimeLifecycleAudit(now time.Time, eventType string, runtime RuntimeRead, payload map[string]any) {
	s.emitAuditEvent(audit.Event{
		OccurredAt:  now,
		Source:      audit.SourceRuntime,
		EventType:   eventType,
		ActorType:   audit.ActorTypeExternal,
		ActorID:     runtime.ID,
		TargetType:  "runtime",
		TargetID:    runtime.ID,
		WorkspaceID: runtime.WorkspaceID,
		Payload:     payload,
	})
}

func runtimeAuditPayload(runtime RuntimeRead) map[string]any {
	payload := map[string]any{
		"name":     runtime.Name,
		"type":     runtime.Type,
		"provider": runtime.Provider,
	}
	if runtime.OwnerUserID != nil && strings.TrimSpace(*runtime.OwnerUserID) != "" {
		payload["owner_user_id"] = strings.TrimSpace(*runtime.OwnerUserID)
	}
	if strings.TrimSpace(runtime.Hostname) != "" {
		payload["hostname"] = strings.TrimSpace(runtime.Hostname)
	}
	if strings.TrimSpace(runtime.Version) != "" {
		payload["version"] = strings.TrimSpace(runtime.Version)
	}
	return payload
}

// One mapper per sqlc row type so column-addition surfaces as a
// compile error here instead of a silent missing field at runtime.

func runtimeReadFromCreateRow(r sqlc.CreateRuntimePairingRow) RuntimeRead {
	return RuntimeRead{
		ID:                    r.ID,
		WorkspaceID:           r.WorkspaceID,
		Type:                  r.Type,
		Name:                  r.Name,
		Liveness:              r.Liveness,
		Provider:              r.Provider,
		OwnerUserID:           nullableUUIDString(r.OwnerUserID),
		Version:               r.Version,
		Hostname:              r.Hostname,
		LastHeartbeatAt:       nullableTime(r.LastHeartbeatAt),
		PairingTokenExpiresAt: nullableTime(r.PairingTokenExpiresAt),
		Config:                unmarshalJSONOrEmpty(r.Config),
		CreatedAt:             r.CreatedAt.Time,
		UpdatedAt:             r.UpdatedAt.Time,
	}
}

func runtimeReadFromConsumeRow(r sqlc.ConsumePairingTokenRow) RuntimeRead {
	return RuntimeRead{
		ID:          r.ID,
		WorkspaceID: r.WorkspaceID,
		Type:        r.Type,
		Name:        r.Name,
		Liveness:    r.Liveness,
		Provider:    r.Provider,
		OwnerUserID: nullableUUIDString(r.OwnerUserID),
		Hostname:    r.Hostname,
		Version:     r.Version,
		Config:      unmarshalJSONOrEmpty(r.Config),
		CreatedAt:   r.CreatedAt.Time,
		UpdatedAt:   r.UpdatedAt.Time,
	}
}

func runtimeReadFromGetRow(r sqlc.GetRuntimeRow) RuntimeRead {
	return RuntimeRead{
		ID:                    r.ID,
		WorkspaceID:           r.WorkspaceID,
		Type:                  r.Type,
		Name:                  r.Name,
		Liveness:              r.Liveness,
		Provider:              r.Provider,
		OwnerUserID:           nullableUUIDString(r.OwnerUserID),
		Version:               r.Version,
		Hostname:              r.Hostname,
		LastHeartbeatAt:       nullableTime(r.LastHeartbeatAt),
		PairingTokenExpiresAt: nullableTime(r.PairingTokenExpiresAt),
		Config:                unmarshalJSONOrEmpty(r.Config),
		CreatedAt:             r.CreatedAt.Time,
		UpdatedAt:             r.UpdatedAt.Time,
	}
}

func runtimeReadFromListRow(r sqlc.ListRuntimesByWorkspaceRow) RuntimeRead {
	return RuntimeRead{
		ID:                    r.ID,
		WorkspaceID:           r.WorkspaceID,
		Type:                  r.Type,
		Name:                  r.Name,
		Liveness:              r.Liveness,
		Provider:              r.Provider,
		OwnerUserID:           nullableUUIDString(r.OwnerUserID),
		Version:               r.Version,
		Hostname:              r.Hostname,
		LastHeartbeatAt:       nullableTime(r.LastHeartbeatAt),
		PairingTokenExpiresAt: nullableTime(r.PairingTokenExpiresAt),
		Config:                unmarshalJSONOrEmpty(r.Config),
		CreatedAt:             r.CreatedAt.Time,
		UpdatedAt:             r.UpdatedAt.Time,
	}
}

func runtimeReadFromPatchRow(r sqlc.PatchRuntimeRow) RuntimeRead {
	return RuntimeRead{
		ID:                    r.ID,
		WorkspaceID:           r.WorkspaceID,
		Type:                  r.Type,
		Name:                  r.Name,
		Liveness:              r.Liveness,
		Provider:              r.Provider,
		OwnerUserID:           nullableUUIDString(r.OwnerUserID),
		Version:               r.Version,
		Hostname:              r.Hostname,
		LastHeartbeatAt:       nullableTime(r.LastHeartbeatAt),
		PairingTokenExpiresAt: nullableTime(r.PairingTokenExpiresAt),
		Config:                unmarshalJSONOrEmpty(r.Config),
		CreatedAt:             r.CreatedAt.Time,
		UpdatedAt:             r.UpdatedAt.Time,
	}
}

// marshalJSONOrEmpty serializes a map for a jsonb column write,
// substituting "{}" when the caller passes nil.
func marshalJSONOrEmpty(m map[string]any) ([]byte, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}
