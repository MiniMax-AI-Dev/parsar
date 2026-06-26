// Package binding owns the (conversation, project_agent) ↔ device mapping
// for the agent_daemon connector, persisted via the generic
// connector_session_bindings table:
//
//	connector_type      = "agent_daemon"
//	binding_key         = project_agent id
//	upstream_session_id = device id (runtimes row uuid)
//	metadata.agent_kind / claude_session_id / work_dir
package binding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// ConnectorType is the connector_type value the binding rows carry.
const ConnectorType = "agent_daemon"

// JSON keys used inside connector_session_bindings.metadata.
const (
	MetaAgentKind        = "agent_kind"
	MetaClaudeSessionID  = "claude_session_id"
	MetaWorkDir          = "work_dir"
	MetaCreatedByPicker  = "created_by_picker"
	MetaCreatedBySandbox = "created_by_sandbox"
	// MetaSessionUpdatedAt is the RFC3339 timestamp of the run whose
	// done event most recently wrote claude_session_id. Used as the
	// CAS guard in RememberSession so a stale done event cannot
	// overwrite a fresher session id from a newer run.
	MetaSessionUpdatedAt = "session_updated_at"
)

// Binding is the resolved view a caller cares about.
type Binding struct {
	ConversationID  string
	ProjectAgentID  string
	DeviceID        string
	AgentKind       string
	ClaudeSessionID string
	WorkDir         string
}

// ErrNotBound is returned by Resolve when a conversation has no
// binding for the requested project_agent.
var ErrNotBound = errors.New("agentdaemon: conversation has no device binding for project_agent")

// Binder is the public interface the connector and gateway use.
type Binder interface {
	Resolve(ctx context.Context, conversationID, projectAgentID string) (Binding, error)
	Bind(ctx context.Context, b Binding) error
	// RememberSession folds a Claude session id into an existing
	// binding's metadata. runStartedAt acts as a CAS guard: the new
	// session id is written only when the existing
	// metadata.session_updated_at is older than runStartedAt (or
	// absent). Pass time.Time{} to opt out of the guard.
	RememberSession(ctx context.Context, conversationID, projectAgentID, claudeSessionID string, runStartedAt time.Time) error
	InvalidateConversation(ctx context.Context, conversationID string) error
	InvalidateDevice(ctx context.Context, deviceID string) error
}

// ----------------------------------------------------------------------
// Postgres-backed implementation
// ----------------------------------------------------------------------

// PgBinder is the production wiring over connector_session_bindings.
// A nil logger silently swallows internal-error logs: a DB outage
// degrades to "no binding cached, pick fresh" which is correct, just
// slower.
type PgBinder struct {
	pool   *pgxpool.Pool
	logger func(format string, args ...any)
}

func NewPgBinder(pool *pgxpool.Pool, logger func(format string, args ...any)) *PgBinder {
	if logger == nil {
		logger = func(string, ...any) {}
	}
	return &PgBinder{pool: pool, logger: logger}
}

// Resolve returns ErrNotBound when no row exists for (conversation,
// project_agent); other DB errors surface as-is.
func (b *PgBinder) Resolve(ctx context.Context, conversationID, projectAgentID string) (Binding, error) {
	if conversationID == "" || projectAgentID == "" {
		return Binding{}, ErrNotBound
	}
	q := sqlc.New(b.pool)
	rows, err := q.ListConnectorSessionBindings(ctx, sqlc.ListConnectorSessionBindingsParams{
		ConversationID: conversationID,
		ConnectorType:  ConnectorType,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Binding{}, ErrNotBound
		}
		return Binding{}, fmt.Errorf("agentdaemon binding: list: %w", err)
	}
	for _, r := range rows {
		if r.BindingKey != projectAgentID {
			continue
		}
		out := Binding{
			ConversationID: conversationID,
			ProjectAgentID: projectAgentID,
			DeviceID:       r.UpstreamSessionID,
		}
		applyMetadata(&out, r.Metadata)
		// Best-effort; errors swallowed.
		if touchErr := q.TouchConnectorSessionBinding(ctx, sqlc.TouchConnectorSessionBindingParams{
			ConversationID: conversationID,
			ConnectorType:  ConnectorType,
			BindingKey:     projectAgentID,
		}); touchErr != nil {
			b.logger("agentdaemon binding: touch failed: %v", touchErr)
		}
		return out, nil
	}
	return Binding{}, ErrNotBound
}

// ResolveDeviceByConversation returns the device id of any
// agent_daemon binding under conversationID. Used by the feishuoutbound
// card-writer to stamp device_id onto the inflight slot without having
// to thread project_agent_id through — a conversation effectively has
// a single agent_daemon device today, so picking the first binding is
// safe. Returns ErrNotBound when no agent_daemon binding exists.
func (b *PgBinder) ResolveDeviceByConversation(ctx context.Context, conversationID string) (string, error) {
	if conversationID == "" {
		return "", ErrNotBound
	}
	q := sqlc.New(b.pool)
	rows, err := q.ListConnectorSessionBindings(ctx, sqlc.ListConnectorSessionBindingsParams{
		ConversationID: conversationID,
		ConnectorType:  ConnectorType,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotBound
		}
		return "", fmt.Errorf("agentdaemon binding: list: %w", err)
	}
	for _, r := range rows {
		if r.UpstreamSessionID != "" {
			return r.UpstreamSessionID, nil
		}
	}
	return "", ErrNotBound
}

// Bind upserts the (conversation, project_agent) → device mapping.
// agent_kind defaults to "claude_code" when empty.
func (b *PgBinder) Bind(ctx context.Context, in Binding) error {
	if in.ConversationID == "" || in.ProjectAgentID == "" || in.DeviceID == "" {
		return fmt.Errorf("agentdaemon binding: ConversationID, ProjectAgentID, DeviceID all required")
	}
	if in.AgentKind == "" {
		in.AgentKind = "claude_code"
	}
	meta := map[string]any{MetaAgentKind: in.AgentKind}
	if in.ClaudeSessionID != "" {
		meta[MetaClaudeSessionID] = in.ClaudeSessionID
		meta[MetaSessionUpdatedAt] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if in.WorkDir != "" {
		meta[MetaWorkDir] = in.WorkDir
	}
	rawMeta, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("agentdaemon binding: marshal metadata: %w", err)
	}
	if err := sqlc.New(b.pool).UpsertConnectorSessionBinding(ctx, sqlc.UpsertConnectorSessionBindingParams{
		ConversationID:    in.ConversationID,
		ConnectorType:     ConnectorType,
		BindingKey:        in.ProjectAgentID,
		UpstreamSessionID: in.DeviceID,
		Metadata:          rawMeta,
	}); err != nil {
		return fmt.Errorf("agentdaemon binding: upsert: %w", err)
	}
	return nil
}

// RememberSession folds a Claude session id into an existing binding's
// metadata. If no binding exists the call is a no-op.
//
// runStartedAt is the CAS guard: when non-zero, the new session id is
// written only if the existing metadata.session_updated_at is older
// than runStartedAt (or absent). Read-modify-write happens inside a
// transaction with SELECT FOR UPDATE so two concurrent done events
// for the same (conversation, project_agent) can't race.
func (b *PgBinder) RememberSession(ctx context.Context, conversationID, projectAgentID, claudeSessionID string, runStartedAt time.Time) error {
	if claudeSessionID == "" {
		return nil
	}
	if conversationID == "" || projectAgentID == "" {
		return nil
	}
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("agentdaemon binding: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var rawMeta []byte
	var upstream string
	err = tx.QueryRow(ctx, `
select upstream_session_id, metadata
from connector_session_bindings
where conversation_id = $1
  and connector_type = $2
  and binding_key = $3
for update`, conversationID, ConnectorType, projectAgentID).Scan(&upstream, &rawMeta)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("agentdaemon binding: select for update: %w", err)
	}

	var meta map[string]any
	if len(rawMeta) > 0 {
		if err := json.Unmarshal(rawMeta, &meta); err != nil {
			return fmt.Errorf("agentdaemon binding: unmarshal metadata: %w", err)
		}
	}
	if meta == nil {
		meta = map[string]any{}
	}

	// Idempotent: same session id already stored.
	if existing, _ := meta[MetaClaudeSessionID].(string); existing == claudeSessionID {
		return nil
	}

	// Last-write-wins. The original CAS on runStartedAt vs stored
	// MetaSessionUpdatedAt was meant to keep a stale done event from
	// clobbering a newer run's session id, but in the supersede flow
	// (run A asks → user cancels → run A's done arrives after run B
	// has already started + finished) the "older" run actually holds
	// the live transcript, including the ask history we want to
	// --resume from. Same-(conversation, project_agent) runs are
	// serialised by the sibling-blocked queue gate, so two writes
	// never race for legitimate reasons; just keep the most recent.
	_ = runStartedAt

	meta[MetaClaudeSessionID] = claudeSessionID
	meta[MetaSessionUpdatedAt] = time.Now().UTC().Format(time.RFC3339Nano)
	newRaw, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("agentdaemon binding: marshal updated metadata: %w", err)
	}
	if _, err := tx.Exec(ctx, `
update connector_session_bindings
set metadata = $4,
    last_active_at = now()
where conversation_id = $1
  and connector_type = $2
  and binding_key = $3`, conversationID, ConnectorType, projectAgentID, newRaw); err != nil {
		return fmt.Errorf("agentdaemon binding: update metadata: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("agentdaemon binding: commit: %w", err)
	}
	return nil
}

// InvalidateConversation drops every binding for the conversation.
func (b *PgBinder) InvalidateConversation(ctx context.Context, conversationID string) error {
	if conversationID == "" {
		return nil
	}
	if err := sqlc.New(b.pool).DeleteConnectorSessionBindingsByConversation(ctx, sqlc.DeleteConnectorSessionBindingsByConversationParams{
		ConversationID: conversationID,
		ConnectorType:  ConnectorType,
	}); err != nil {
		return fmt.Errorf("agentdaemon binding: invalidate conversation: %w", err)
	}
	return nil
}

// InvalidateDevice drops every binding that points at a given device.
// Scoped to connector_type so we don't accidentally evict another
// connector's bindings that happen to share an upstream id.
func (b *PgBinder) InvalidateDevice(ctx context.Context, deviceID string) error {
	if deviceID == "" {
		return nil
	}
	if err := sqlc.New(b.pool).DeleteConnectorSessionBindingsByUpstreamSession(ctx, sqlc.DeleteConnectorSessionBindingsByUpstreamSessionParams{
		ConnectorType:     ConnectorType,
		UpstreamSessionID: deviceID,
	}); err != nil {
		return fmt.Errorf("agentdaemon binding: invalidate device: %w", err)
	}
	return nil
}

func applyMetadata(out *Binding, raw []byte) {
	if len(raw) == 0 {
		return
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return
	}
	if v, ok := m[MetaAgentKind].(string); ok {
		out.AgentKind = v
	}
	if v, ok := m[MetaClaudeSessionID].(string); ok {
		out.ClaudeSessionID = v
	}
	if v, ok := m[MetaWorkDir].(string); ok {
		out.WorkDir = v
	}
}

// ----------------------------------------------------------------------
// In-memory implementation (tests / when no DB is wired)
// ----------------------------------------------------------------------

// InMemoryBinder is the default zero-value binder used by tests and by
// any router that runs without a database. It is concurrency-safe.
type InMemoryBinder struct {
	bindings map[string]Binding // key = conversation_id|project_agent_id
	mu       sync.Mutex
}

func NewInMemoryBinder() *InMemoryBinder {
	return &InMemoryBinder{bindings: map[string]Binding{}}
}

func (b *InMemoryBinder) Resolve(_ context.Context, conversationID, projectAgentID string) (Binding, error) {
	if conversationID == "" || projectAgentID == "" {
		return Binding{}, ErrNotBound
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out, ok := b.bindings[memKey(conversationID, projectAgentID)]
	if !ok {
		return Binding{}, ErrNotBound
	}
	return out, nil
}

func (b *InMemoryBinder) Bind(_ context.Context, in Binding) error {
	if in.ConversationID == "" || in.ProjectAgentID == "" || in.DeviceID == "" {
		return fmt.Errorf("agentdaemon binding: ConversationID, ProjectAgentID, DeviceID all required")
	}
	if in.AgentKind == "" {
		in.AgentKind = "claude_code"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bindings[memKey(in.ConversationID, in.ProjectAgentID)] = in
	return nil
}

func (b *InMemoryBinder) RememberSession(_ context.Context, conversationID, projectAgentID, claudeSessionID string, runStartedAt time.Time) error {
	if claudeSessionID == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	key := memKey(conversationID, projectAgentID)
	cur, ok := b.bindings[key]
	if !ok {
		return nil
	}
	// In-memory binder has no stored timestamp to CAS against; last
	// write wins is the documented semantics for this stub.
	_ = runStartedAt
	cur.ClaudeSessionID = claudeSessionID
	b.bindings[key] = cur
	return nil
}

func (b *InMemoryBinder) InvalidateConversation(_ context.Context, conversationID string) error {
	if conversationID == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	prefix := conversationID + "|"
	for k := range b.bindings {
		if hasPrefix(k, prefix) {
			delete(b.bindings, k)
		}
	}
	return nil
}

func (b *InMemoryBinder) InvalidateDevice(_ context.Context, deviceID string) error {
	if deviceID == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for k, v := range b.bindings {
		if v.DeviceID == deviceID {
			delete(b.bindings, k)
		}
	}
	return nil
}

func memKey(conversationID, projectAgentID string) string {
	return conversationID + "|" + projectAgentID
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
