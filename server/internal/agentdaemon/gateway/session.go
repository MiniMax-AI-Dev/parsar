package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// Tunables. Package-level so tests can override via small helpers
// without exposing struct fields on every Session.
var (
	// DefaultHeartbeatInterval is the cadence the daemon is told to
	// send heartbeats at via the bootstrap response. The server uses
	// HeartbeatTimeout (not this cadence) to decide a session is dead.
	DefaultHeartbeatInterval = 15 * time.Second

	// HeartbeatTimeout is how long the server tolerates no inbound
	// frame (heartbeat OR data) before declaring the session unhealthy.
	HeartbeatTimeout = 60 * time.Second

	// WriteTimeout caps how long a single outbound frame may block.
	// Past this we treat the peer as wedged and close the session.
	WriteTimeout = 10 * time.Second

	// ReadLimit caps a single inbound frame at 4 MiB. tool_call
	// results can be large but anything past this is almost certainly
	// a misbehaving daemon (or hostile input).
	ReadLimit int64 = 4 * 1024 * 1024

	// CloseRuntimeDeleted is a custom WS close code (4001) sent when
	// a heartbeat discovers the runtime has been deleted. The daemon
	// treats this as a permanent error and exits rather than reconnecting.
	CloseRuntimeDeleted = 4001
)

// ErrSessionClosed is returned by Send / Subscribe when the session
// has shut down.
var ErrSessionClosed = errors.New("agentdaemon gateway: session closed")

// WSConn is the slice of *websocket.Conn the session uses, exported so
// cross-package tests can substitute a fake without a real WS upgrader.
type WSConn interface {
	ReadMessage() (int, []byte, error)
	WriteMessage(messageType int, data []byte) error
	SetReadLimit(limit int64)
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	Close() error
}

// SessionLogger is the minimal logging surface a session needs. Pass
// nil to silence logs.
type SessionLogger func(format string, args ...any)

// Session owns one goroutine for the read loop and serialises writes
// via a single send goroutine so callers can Send concurrently without
// violating gorilla's "single writer" requirement.
type Session struct {
	DeviceID      string
	WorkspaceID   string
	DaemonVersion string
	ConnectedAt   time.Time

	conn WSConn
	log  SessionLogger
	reg  *Registry

	// owner is non-nil in multi-pod mode. It fences this WebSocket
	// against the DB owner row so stale connections from an older pod
	// cannot keep handling prompts after a reconnect claimed a newer
	// generation.
	owner *ownerLease

	// heartbeat persists daemon-advertised capability snapshots.
	heartbeat HeartbeatTouch

	hbMu       sync.Mutex
	lastSeenAt time.Time

	// supportedKinds is the latest daemon-advertised agent_kind snapshot,
	// updated from heartbeat frames and read by the connector before
	// dispatching prompt_request so unsupported engines fail on the server.
	kindsMu        sync.RWMutex
	kindsSeen      bool
	supportedKinds []store.AgentDaemonSupportedAgentKind

	// Subscribers keyed by runID. The read loop only sends on these
	// channels; Unsubscribe is the only place that closes them.
	subsMu sync.Mutex
	subs   map[string]chan proto.Envelope

	// sendCh feeds the WS write loop. Capacity is bounded so a slow
	// peer can't queue unbounded outbound frames; once full, Send
	// blocks up to WriteTimeout then returns an error.
	sendCh chan proto.Envelope

	// closeOnce guards the shutdown path so concurrent Close calls
	// collapse into one.
	closeOnce sync.Once
	closed    chan struct{}
}

// NewSession wires a freshly-upgraded WS connection into a Session.
// The session does NOT start its goroutines automatically — Start runs
// once the handler is ready so the session can't race with response writes.
func NewSession(conn WSConn, deviceID, workspaceID, daemonVersion string, reg *Registry, log SessionLogger) *Session {
	return NewSessionWithOwner(conn, deviceID, workspaceID, daemonVersion, reg, log, nil)
}

// NewSessionWithOwner wires a session with an optional DB-backed owner
// lease. Multi-pod deployments pass the lease returned by
// ClaimAgentDaemonDeviceOwner so heartbeats can fence stale connections.
func NewSessionWithOwner(conn WSConn, deviceID, workspaceID, daemonVersion string, reg *Registry, log SessionLogger, owner *ownerLease) *Session {
	if log == nil {
		log = func(string, ...any) {}
	}
	if reg == nil {
		reg = NewRegistry()
	}
	now := time.Now()
	return &Session{
		DeviceID:      deviceID,
		WorkspaceID:   workspaceID,
		DaemonVersion: daemonVersion,
		ConnectedAt:   now,
		conn:          conn,
		log:           log,
		reg:           reg,
		owner:         owner,
		lastSeenAt:    now,
		subs:          map[string]chan proto.Envelope{},
		sendCh:        make(chan proto.Envelope, 64),
		closed:        make(chan struct{}),
	}
}

// Start kicks off the read + write loops. The caller MUST eventually
// call Close (or wait for the read loop to fail) before *Session is
// GC-eligible.
func (s *Session) Start() {
	s.conn.SetReadLimit(ReadLimit)
	go s.writeLoop()
	go s.readLoop()
}

// Closed returns a channel that's closed once the session has shut down.
func (s *Session) Closed() <-chan struct{} { return s.closed }

// IsClosed reports whether Close has been called.
func (s *Session) IsClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

// LastSeen returns the timestamp of the most recent inbound frame.
func (s *Session) LastSeen() time.Time {
	s.hbMu.Lock()
	defer s.hbMu.Unlock()
	return s.lastSeenAt
}

// AgentKindStatus returns the latest advertised descriptor for kind.
// found=false means the daemon has not advertised that kind; snapshotKnown
// distinguishes "no heartbeat yet" from "heartbeat arrived and omitted it".
// Before the first heartbeat, legacy Claude Code behavior is preserved so
// older daemons can still receive claude_code prompt_requests immediately.
func (s *Session) AgentKindStatus(kind string) (info store.AgentDaemonSupportedAgentKind, found bool, snapshotKnown bool) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return store.AgentDaemonSupportedAgentKind{}, false, false
	}
	s.kindsMu.RLock()
	seen := s.kindsSeen
	kinds := make([]store.AgentDaemonSupportedAgentKind, len(s.supportedKinds))
	copy(kinds, s.supportedKinds)
	s.kindsMu.RUnlock()
	if !seen {
		if kind == "claude_code" {
			return legacyClaudeCodeKind(), true, false
		}
		return store.AgentDaemonSupportedAgentKind{}, false, false
	}
	for _, candidate := range kinds {
		if candidate.Kind == kind {
			return candidate, true, true
		}
	}
	return store.AgentDaemonSupportedAgentKind{}, false, true
}

func (s *Session) setSupportedAgentKinds(kinds []store.AgentDaemonSupportedAgentKind) {
	copyKinds := make([]store.AgentDaemonSupportedAgentKind, len(kinds))
	copy(copyKinds, kinds)
	s.kindsMu.Lock()
	s.kindsSeen = true
	s.supportedKinds = copyKinds
	s.kindsMu.Unlock()
}

func legacyClaudeCodeKind() store.AgentDaemonSupportedAgentKind {
	return store.AgentDaemonSupportedAgentKind{
		Kind:      "claude_code",
		Available: true,
		Capabilities: store.AgentDaemonKindCapabilities{
			Streaming:   true,
			Permissions: true,
			Usage:       true,
			Resume:      true,
		},
	}
}

// Close tears the session down: closes the WS, drains subscribers
// with a synthetic error+done pair, and deregisters. Idempotent.
func (s *Session) Close(reason string) {
	s.closeOnce.Do(func() {
		close(s.closed)
		_ = s.conn.Close()
		// Synthetic error + done so the connector's translation loop
		// sees a clean EOF and unsubscribes naturally.
		s.subsMu.Lock()
		subs := s.subs
		s.subs = map[string]chan proto.Envelope{}
		s.subsMu.Unlock()
		for runID, ch := range subs {
			s.deliverSynthetic(runID, ch, reason)
			close(ch)
			s.reg.DetachRun(runID)
		}
		s.reg.Deregister(s)
		s.markOfflineOnClose()
		s.releaseOwnerLease()
	})
}

// CloseWithCode sends a WS close frame with a custom status code so
// permanent conditions (e.g. runtime deleted) can be distinguished
// from transient disconnects.
func (s *Session) CloseWithCode(code int, reason string) {
	_ = s.conn.SetWriteDeadline(time.Now().Add(WriteTimeout))
	_ = s.conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(code, reason))
	s.Close(reason)
}

func (s *Session) deliverSynthetic(runID string, ch chan proto.Envelope, reason string) {
	if reason == "" {
		reason = "device disconnected"
	}
	errEnv, _ := proto.NewEnvelope(proto.TypeError, runID, proto.ErrorPayload{Error: reason})
	doneEnv, _ := proto.NewEnvelope(proto.TypeDone, runID, proto.DonePayload{})
	// Non-blocking — drop rather than hang the close path on a
	// wedged subscriber.
	select {
	case ch <- errEnv:
	default:
	}
	select {
	case ch <- doneEnv:
	default:
	}
}

// Send queues an envelope for the WS write loop. Returns ErrSessionClosed
// if the session has shut down, or context.DeadlineExceeded if the send
// queue is full for longer than ctx's timeout.
//
// Side effect: stamps env.Trace from ctx if absent, so every server →
// daemon frame inherits the caller's trace_id. Callers that explicitly
// set env.Trace win.
func (s *Session) Send(ctx context.Context, env proto.Envelope) error {
	if s.IsClosed() {
		return ErrSessionClosed
	}
	if env.Trace == "" {
		if carrier, ok := obslog.TraceFromContext(ctx); ok {
			env.Trace = carrier.String()
		}
	}
	select {
	case s.sendCh <- env:
		return nil
	case <-s.closed:
		return ErrSessionClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Subscribe attaches a receiver channel for one runID. Calling Subscribe
// twice for the same runID replaces (and closes) the previous subscriber.
// The subscriber MUST call Unsubscribe when done; the channel is closed
// by Unsubscribe or by the read loop on a done/error auto-unsubscribe.
func (s *Session) Subscribe(runID string) (<-chan proto.Envelope, error) {
	if runID == "" {
		return nil, fmt.Errorf("agentdaemon gateway: Subscribe requires non-empty runID")
	}
	if s.IsClosed() {
		return nil, ErrSessionClosed
	}
	ch := make(chan proto.Envelope, 32)
	s.subsMu.Lock()
	if existing, ok := s.subs[runID]; ok {
		close(existing)
	}
	s.subs[runID] = ch
	s.subsMu.Unlock()
	s.reg.AttachRun(runID, s)
	return ch, nil
}

// Unsubscribe removes a runID's subscriber and closes its channel.
// Idempotent.
func (s *Session) Unsubscribe(runID string) {
	if runID == "" {
		return
	}
	s.subsMu.Lock()
	ch, ok := s.subs[runID]
	if ok {
		delete(s.subs, runID)
	}
	s.subsMu.Unlock()
	if ok {
		close(ch)
	}
	s.reg.DetachRun(runID)
}

// writeLoop is the single writer goroutine that gorilla/websocket
// requires. Exits when sendCh is closed (Close path) or on a write error.
func (s *Session) writeLoop() {
	for {
		select {
		case env, ok := <-s.sendCh:
			if !ok {
				return
			}
			raw, err := json.Marshal(env)
			if err != nil {
				s.log("agentdaemon gateway: marshal outbound envelope: %v", err)
				continue
			}
			_ = s.conn.SetWriteDeadline(time.Now().Add(WriteTimeout))
			if err := s.conn.WriteMessage(websocket.TextMessage, raw); err != nil {
				s.log("agentdaemon gateway: write %s frame: %v", env.Type, err)
				s.Close("write error: " + err.Error())
				return
			}
		case <-s.closed:
			return
		}
	}
}

// readLoop is the only place that consumes from the WS. It owns the
// heartbeat timestamp and the dispatch into per-run subscribers.
func (s *Session) readLoop() {
	defer s.Close("read loop exit")

	for {
		// Bound the read so a dead peer surfaces as a deadline rather
		// than a hang.
		_ = s.conn.SetReadDeadline(time.Now().Add(HeartbeatTimeout))
		_, raw, err := s.conn.ReadMessage()
		if err != nil {
			s.log("agentdaemon gateway: read frame: %v", err)
			return
		}
		s.markSeen()
		if !s.renewOwnerLease() {
			return
		}

		var env proto.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			s.log("agentdaemon gateway: unmarshal inbound frame: %v", err)
			continue
		}
		s.dispatch(env)
	}
}

func (s *Session) markSeen() {
	s.hbMu.Lock()
	s.lastSeenAt = time.Now()
	s.hbMu.Unlock()
}

func (s *Session) renewOwnerLease() bool {
	if s.owner == nil || s.owner.store == nil {
		return true
	}
	now := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, ok, err := s.owner.store.RenewAgentDaemonDeviceOwner(ctx, store.RenewAgentDaemonDeviceOwnerInput{
		DeviceID:       s.owner.deviceID,
		OwnerPodID:     s.owner.ownerPodID,
		Generation:     s.owner.generation,
		Now:            now,
		LeaseExpiresAt: now.Add(normalizeOwnerTTL(s.owner.ttl)),
	})
	if err != nil {
		s.log("agentdaemon gateway: owner lease renew failed device=%s generation=%d: %v", s.DeviceID, s.owner.generation, err)
		s.Close("owner lease renew failed")
		return false
	}
	if !ok {
		s.log("agentdaemon gateway: owner lease lost device=%s generation=%d", s.DeviceID, s.owner.generation)
		s.Close("owner lease lost to a newer connection")
		return false
	}
	return true
}

func (s *Session) releaseOwnerLease() {
	if s.owner == nil || s.owner.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := s.owner.store.ReleaseAgentDaemonDeviceOwner(ctx, store.ReleaseAgentDaemonDeviceOwnerInput{
		DeviceID:   s.owner.deviceID,
		OwnerPodID: s.owner.ownerPodID,
		Generation: s.owner.generation,
	}); err != nil {
		s.log("agentdaemon gateway: owner lease release failed device=%s generation=%d: %v", s.DeviceID, s.owner.generation, err)
	}
}

func (s *Session) markOfflineOnClose() {
	if s.heartbeat == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.heartbeat.MarkRuntimeOffline(ctx, s.DeviceID); err != nil {
		s.log("agentdaemon gateway: mark offline on close failed device=%s: %v", s.DeviceID, err)
	}
}

func (s *Session) handleHeartbeat(env proto.Envelope) {
	var p proto.HeartbeatPayload
	if err := env.DecodePayload(&p); err != nil {
		s.log("agentdaemon gateway: decode heartbeat payload device=%s: %v", s.DeviceID, err)
		return
	}
	kinds := storeKindsFromHeartbeat(p)
	s.setSupportedAgentKinds(kinds)
	if s.heartbeat == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	status, err := s.heartbeat.TouchAgentDaemonHeartbeat(ctx, store.TouchAgentDaemonHeartbeatInput{
		RuntimeID:           s.DeviceID,
		DaemonVersion:       p.DaemonVersion,
		ActiveRequests:      p.ActiveRequests,
		HeartbeatTimestamp:  p.Timestamp,
		SupportedAgentKinds: kinds,
	})
	if err != nil {
		s.log("agentdaemon gateway: persist heartbeat device=%s: %v", s.DeviceID, err)
		return
	}
	if status.Deleted {
		s.log("agentdaemon gateway: runtime retired, closing session device=%s", s.DeviceID)
		// "retired" rather than "deleted by admin": the row may have
		// been soft-deleted by sandbox stale-row cleanup or by an
		// actual admin action; the daemon only sees it's no longer
		// the current owner.
		s.CloseWithCode(CloseRuntimeDeleted, "runtime retired")
	}
}

func storeKindsFromHeartbeat(p proto.HeartbeatPayload) []store.AgentDaemonSupportedAgentKind {
	if len(p.SupportedAgentKinds) == 0 {
		if !p.ClaudeAvailable {
			return nil
		}
		return []store.AgentDaemonSupportedAgentKind{{
			Kind:      "claude_code",
			Available: true,
			Capabilities: store.AgentDaemonKindCapabilities{
				Streaming:   true,
				Permissions: true,
				Usage:       true,
				Resume:      true,
			},
		}}
	}
	out := make([]store.AgentDaemonSupportedAgentKind, 0, len(p.SupportedAgentKinds))
	for _, info := range p.SupportedAgentKinds {
		out = append(out, store.AgentDaemonSupportedAgentKind{
			Kind:      info.Kind,
			Available: info.Available,
			Version:   info.Version,
			Capabilities: store.AgentDaemonKindCapabilities{
				Streaming:   info.Capabilities.Streaming,
				Permissions: info.Capabilities.Permissions,
				Usage:       info.Capabilities.Usage,
				Resume:      info.Capabilities.Resume,
			},
		})
	}
	return out
}

func (s *Session) dispatch(env proto.Envelope) {
	switch env.Type {
	case proto.TypeHeartbeat:
		s.handleHeartbeat(env)
		return
	case proto.TypePermissionRequest:
		if env.ID != "" {
			s.reg.AttachPermission(env.ID, s)
		}
	case proto.TypePermissionCancel:
		if env.ID != "" {
			s.reg.DetachPermission(env.ID)
		}
	case proto.TypePromptForUserChoice:
		// env.ID is the run id (so the fan path below delivers this
		// frame to the run's subscriber). The ask id rides on the
		// payload — pull it out so SubmitPromptForUserChoice can find
		// the session by ask id via the byAsk index.
		var p proto.PromptForUserChoicePayload
		if err := env.DecodePayload(&p); err == nil && p.AskID != "" {
			s.reg.AttachPromptForUserChoice(p.AskID, s)
		}
	}

	// All run-correlated frames fan to the matching subscriber.
	// Permission events are stamped with the perm id (not the runID)
	// and are handled by AttachPermission/DetachPermission above; the
	// connector routes them via the permission-channel-by-perm-id set,
	// so they bypass this run-channel path.
	if env.ID == "" {
		return
	}
	s.subsMu.Lock()
	ch, ok := s.subs[env.ID]
	s.subsMu.Unlock()
	if !ok {
		return
	}
	// Bounded send so a wedged subscriber can't pin the read loop.
	select {
	case ch <- env:
	case <-s.closed:
		return
	default:
		s.log("agentdaemon gateway: subscriber buffer full for run %s, dropping %s", env.ID, env.Type)
	}

	// Auto-unsubscribe after a terminal frame so the subscriber sees
	// a closed channel without an explicit "done" signal.
	if env.Type == proto.TypeDone {
		s.Unsubscribe(env.ID)
	}
}
