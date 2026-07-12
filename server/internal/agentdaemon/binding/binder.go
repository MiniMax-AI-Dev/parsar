// Package binding owns agent-daemon runtime bindings and engine sessions.
package binding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

const (
	SessionTypeClaude = "claude_session"
	SessionTypeCodex  = "codex_thread"
	SessionTypePi     = "pi_session"
)

// Binding is the resolved view a caller cares about.
type Binding struct {
	ConversationID   string
	AgentID          string
	DeviceID         string
	AgentKind        string
	AgentSessionID   string
	AgentSessionType string
	AgentStateKey    string
	WorkDir          string
	Metadata         map[string]any
}

// ErrNotBound is returned when a conversation has no runtime binding.
var ErrNotBound = errors.New("agentdaemon: conversation has no device binding for agent")

// Binder persists conversation/agent runtime placement and engine session ids.
type Binder interface {
	Resolve(ctx context.Context, conversationID, agentID, agentKind string) (Binding, error)
	Bind(ctx context.Context, b Binding) error
	RememberSession(ctx context.Context, b Binding) error
	InvalidateConversation(ctx context.Context, conversationID string) error
	InvalidateDevice(ctx context.Context, deviceID string) error
}

// PgBinder is the production wiring over agent_runtime_bindings and agent_engine_sessions.
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

func (b *PgBinder) Resolve(ctx context.Context, conversationID, agentID, agentKind string) (Binding, error) {
	if conversationID == "" || agentID == "" {
		return Binding{}, ErrNotBound
	}
	convUUID, err := uuidParam(conversationID)
	if err != nil {
		return Binding{}, fmt.Errorf("agentdaemon binding: conversation_id: %w", err)
	}
	agentUUID, err := uuidParam(agentID)
	if err != nil {
		return Binding{}, fmt.Errorf("agentdaemon binding: agent_id: %w", err)
	}
	row, err := sqlc.New(b.pool).GetAgentDaemonBinding(ctx, sqlc.GetAgentDaemonBindingParams{
		ConversationID: convUUID,
		AgentID:        agentUUID,
		AgentKind:      normaliseAgentKind(agentKind),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Binding{}, ErrNotBound
		}
		return Binding{}, fmt.Errorf("agentdaemon binding: get: %w", err)
	}
	return Binding{
		ConversationID:   row.ConversationID,
		AgentID:          row.AgentID,
		DeviceID:         row.RuntimeID,
		AgentKind:        normaliseAgentKind(agentKind),
		AgentSessionID:   row.UpstreamSessionID,
		AgentSessionType: row.UpstreamSessionType,
		AgentStateKey:    row.StateDirKey,
		WorkDir:          row.WorkDir,
		Metadata:         decodeMetadata(row.Metadata),
	}, nil
}

func (b *PgBinder) ResolveDeviceByConversation(ctx context.Context, conversationID string) (string, error) {
	if conversationID == "" {
		return "", ErrNotBound
	}
	convUUID, err := uuidParam(conversationID)
	if err != nil {
		return "", fmt.Errorf("agentdaemon binding: conversation_id: %w", err)
	}
	deviceID, err := sqlc.New(b.pool).ResolveAgentDaemonDeviceByConversation(ctx, convUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotBound
		}
		return "", fmt.Errorf("agentdaemon binding: resolve device: %w", err)
	}
	return deviceID, nil
}

func (b *PgBinder) Bind(ctx context.Context, in Binding) error {
	if in.ConversationID == "" || in.AgentID == "" || in.DeviceID == "" {
		return fmt.Errorf("agentdaemon binding: ConversationID, AgentID, DeviceID all required")
	}
	convUUID, err := uuidParam(in.ConversationID)
	if err != nil {
		return fmt.Errorf("agentdaemon binding: conversation_id: %w", err)
	}
	agentUUID, err := uuidParam(in.AgentID)
	if err != nil {
		return fmt.Errorf("agentdaemon binding: agent_id: %w", err)
	}
	deviceUUID, err := uuidParam(in.DeviceID)
	if err != nil {
		return fmt.Errorf("agentdaemon binding: runtime_id: %w", err)
	}
	if err := sqlc.New(b.pool).UpsertAgentDaemonRuntimeBinding(ctx, sqlc.UpsertAgentDaemonRuntimeBindingParams{
		ConversationID: convUUID,
		AgentID:        agentUUID,
		RuntimeID:      deviceUUID,
		WorkDir:        in.WorkDir,
	}); err != nil {
		return fmt.Errorf("agentdaemon binding: upsert runtime: %w", err)
	}
	if strings.TrimSpace(in.AgentSessionID) != "" {
		return b.RememberSession(ctx, in)
	}
	return nil
}

func (b *PgBinder) RememberSession(ctx context.Context, in Binding) error {
	if strings.TrimSpace(in.AgentSessionID) == "" {
		return nil
	}
	if in.ConversationID == "" || in.AgentID == "" {
		return nil
	}
	convUUID, err := uuidParam(in.ConversationID)
	if err != nil {
		return fmt.Errorf("agentdaemon binding: conversation_id: %w", err)
	}
	agentUUID, err := uuidParam(in.AgentID)
	if err != nil {
		return fmt.Errorf("agentdaemon binding: agent_id: %w", err)
	}
	metadata, err := json.Marshal(nonNilMetadata(in.Metadata))
	if err != nil {
		return fmt.Errorf("agentdaemon binding: marshal metadata: %w", err)
	}
	if err := sqlc.New(b.pool).UpsertAgentDaemonEngineSession(ctx, sqlc.UpsertAgentDaemonEngineSessionParams{
		ConversationID:      convUUID,
		AgentID:             agentUUID,
		AgentKind:           normaliseAgentKind(in.AgentKind),
		UpstreamSessionID:   in.AgentSessionID,
		UpstreamSessionType: normaliseSessionType(in.AgentSessionType),
		StateDirKey:         defaultStateKey(in),
		Metadata:            metadata,
	}); err != nil {
		return fmt.Errorf("agentdaemon binding: upsert engine session: %w", err)
	}
	return nil
}

func (b *PgBinder) InvalidateConversation(ctx context.Context, conversationID string) error {
	if conversationID == "" {
		return nil
	}
	convUUID, err := uuidParam(conversationID)
	if err != nil {
		return fmt.Errorf("agentdaemon binding: conversation_id: %w", err)
	}
	if err := sqlc.New(b.pool).DeleteAgentDaemonBindingByConversation(ctx, convUUID); err != nil {
		return fmt.Errorf("agentdaemon binding: invalidate conversation: %w", err)
	}
	return nil
}

func (b *PgBinder) InvalidateDevice(ctx context.Context, deviceID string) error {
	if deviceID == "" {
		return nil
	}
	deviceUUID, err := uuidParam(deviceID)
	if err != nil {
		return fmt.Errorf("agentdaemon binding: runtime_id: %w", err)
	}
	if err := sqlc.New(b.pool).DeleteAgentDaemonBindingByRuntime(ctx, deviceUUID); err != nil {
		return fmt.Errorf("agentdaemon binding: invalidate device: %w", err)
	}
	return nil
}

// InMemoryBinder is the default zero-value binder used by tests and by routers without a database.
type InMemoryBinder struct {
	runtimeBindings map[string]Binding
	engineSessions  map[string]Binding
	mu              sync.Mutex
}

func NewInMemoryBinder() *InMemoryBinder {
	return &InMemoryBinder{
		runtimeBindings: map[string]Binding{},
		engineSessions:  map[string]Binding{},
	}
}

func (b *InMemoryBinder) Resolve(_ context.Context, conversationID, agentID, agentKind string) (Binding, error) {
	if conversationID == "" || agentID == "" {
		return Binding{}, ErrNotBound
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out, ok := b.runtimeBindings[memKey(conversationID, agentID)]
	if !ok {
		return Binding{}, ErrNotBound
	}
	out.AgentKind = normaliseAgentKind(agentKind)
	if sess, ok := b.engineSessions[sessionKey(conversationID, agentID, agentKind)]; ok {
		out.AgentSessionID = sess.AgentSessionID
		out.AgentSessionType = sess.AgentSessionType
		out.AgentStateKey = sess.AgentStateKey
		out.Metadata = sess.Metadata
	}
	return out, nil
}

func (b *InMemoryBinder) Bind(_ context.Context, in Binding) error {
	if in.ConversationID == "" || in.AgentID == "" || in.DeviceID == "" {
		return fmt.Errorf("agentdaemon binding: ConversationID, AgentID, DeviceID all required")
	}
	in.AgentKind = normaliseAgentKind(in.AgentKind)
	in.AgentStateKey = defaultStateKey(in)
	b.mu.Lock()
	defer b.mu.Unlock()
	runtime := in
	runtime.AgentSessionID = ""
	runtime.AgentSessionType = ""
	runtime.Metadata = nil
	b.runtimeBindings[memKey(in.ConversationID, in.AgentID)] = runtime
	if in.AgentSessionID != "" {
		b.engineSessions[sessionKey(in.ConversationID, in.AgentID, in.AgentKind)] = in
	}
	return nil
}

func (b *InMemoryBinder) RememberSession(_ context.Context, in Binding) error {
	if strings.TrimSpace(in.AgentSessionID) == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	key := memKey(in.ConversationID, in.AgentID)
	cur, ok := b.runtimeBindings[key]
	if !ok {
		return nil
	}
	in.AgentKind = normaliseAgentKind(in.AgentKind)
	in.AgentSessionType = normaliseSessionType(in.AgentSessionType)
	in.AgentStateKey = defaultStateKey(in)
	cur.AgentKind = in.AgentKind
	cur.AgentSessionID = in.AgentSessionID
	cur.AgentSessionType = in.AgentSessionType
	cur.AgentStateKey = in.AgentStateKey
	cur.Metadata = nonNilMetadata(in.Metadata)
	b.engineSessions[sessionKey(in.ConversationID, in.AgentID, in.AgentKind)] = cur
	return nil
}

func (b *InMemoryBinder) InvalidateConversation(_ context.Context, conversationID string) error {
	if conversationID == "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	prefix := conversationID + "|"
	for k := range b.runtimeBindings {
		if strings.HasPrefix(k, prefix) {
			delete(b.runtimeBindings, k)
		}
	}
	for k := range b.engineSessions {
		if strings.HasPrefix(k, prefix) {
			delete(b.engineSessions, k)
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
	for k, v := range b.runtimeBindings {
		if v.DeviceID == deviceID {
			delete(b.runtimeBindings, k)
			for sessKey := range b.engineSessions {
				if strings.HasPrefix(sessKey, k+"|") {
					delete(b.engineSessions, sessKey)
				}
			}
		}
	}
	return nil
}

func uuidParam(v string) (pgtype.UUID, error) {
	var out pgtype.UUID
	if err := out.Scan(v); err != nil {
		return pgtype.UUID{}, err
	}
	return out, nil
}

func decodeMetadata(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}

func nonNilMetadata(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	return in
}

func normaliseAgentKind(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "claude_code"
	}
	return v
}

func normaliseSessionType(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "agent_session"
	}
	return v
}

func defaultStateKey(in Binding) string {
	if strings.TrimSpace(in.AgentStateKey) != "" {
		return in.AgentStateKey
	}
	return in.ConversationID + "/" + in.AgentID + "/" + normaliseAgentKind(in.AgentKind)
}

func memKey(conversationID, agentID string) string {
	return conversationID + "|" + agentID
}

func sessionKey(conversationID, agentID, agentKind string) string {
	return conversationID + "|" + agentID + "|" + normaliseAgentKind(agentKind)
}
