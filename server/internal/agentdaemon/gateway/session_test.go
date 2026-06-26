package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// fakeConn is the WSConn implementation used by session + registry
// tests. Concurrency-safe.
type fakeConn struct {
	mu       sync.Mutex
	incoming []fakeFrame
	cond     *sync.Cond
	closed   bool

	writes   [][]byte
	writeErr error
}

type fakeFrame struct {
	data []byte
	err  error
}

func newFakeConn() *fakeConn {
	c := &fakeConn{}
	c.cond = sync.NewCond(&c.mu)
	return c
}

func (c *fakeConn) Feed(data []byte) {
	c.mu.Lock()
	c.incoming = append(c.incoming, fakeFrame{data: data})
	c.cond.Broadcast()
	c.mu.Unlock()
}

func (c *fakeConn) FeedError(err error) {
	c.mu.Lock()
	c.incoming = append(c.incoming, fakeFrame{err: err})
	c.cond.Broadcast()
	c.mu.Unlock()
}

func (c *fakeConn) ReadMessage() (int, []byte, error) {
	c.mu.Lock()
	for len(c.incoming) == 0 && !c.closed {
		c.cond.Wait()
	}
	if c.closed && len(c.incoming) == 0 {
		c.mu.Unlock()
		return 0, nil, io.EOF
	}
	f := c.incoming[0]
	c.incoming = c.incoming[1:]
	c.mu.Unlock()
	if f.err != nil {
		return 0, nil, f.err
	}
	return 1, f.data, nil
}

func (c *fakeConn) WriteMessage(_ int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writeErr != nil {
		return c.writeErr
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	c.writes = append(c.writes, cp)
	return nil
}

func (c *fakeConn) SetReadLimit(int64)              {}
func (c *fakeConn) SetReadDeadline(time.Time) error { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error {
	return nil
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.cond.Broadcast()
	c.mu.Unlock()
	return nil
}

func (c *fakeConn) Writes() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.writes))
	copy(out, c.writes)
	return out
}

type fakeHeartbeatStore struct {
	mu       sync.Mutex
	daemonCh chan store.TouchAgentDaemonHeartbeatInput
	daemon   []store.TouchAgentDaemonHeartbeatInput
	runtime  []string
}

func newFakeHeartbeatStore() *fakeHeartbeatStore {
	return &fakeHeartbeatStore{daemonCh: make(chan store.TouchAgentDaemonHeartbeatInput, 4)}
}

func (f *fakeHeartbeatStore) TouchRuntimeHeartbeat(_ context.Context, runtimeID string) (store.HeartbeatStatus, error) {
	f.mu.Lock()
	f.runtime = append(f.runtime, runtimeID)
	f.mu.Unlock()
	return store.HeartbeatStatus{Liveness: store.RuntimeLivenessOnline}, nil
}

func (f *fakeHeartbeatStore) TouchAgentDaemonHeartbeat(_ context.Context, input store.TouchAgentDaemonHeartbeatInput) (store.HeartbeatStatus, error) {
	f.mu.Lock()
	f.daemon = append(f.daemon, input)
	f.mu.Unlock()
	select {
	case f.daemonCh <- input:
	default:
	}
	return store.HeartbeatStatus{Liveness: store.RuntimeLivenessOnline}, nil
}

func (f *fakeHeartbeatStore) MarkRuntimeOffline(_ context.Context, _ string) error {
	return nil
}

func (f *fakeHeartbeatStore) waitDaemonHeartbeat(t *testing.T) store.TouchAgentDaemonHeartbeatInput {
	t.Helper()
	select {
	case input := <-f.daemonCh:
		return input
	case <-time.After(2 * time.Second):
		t.Fatal("daemon heartbeat was not persisted")
	}
	return store.TouchAgentDaemonHeartbeatInput{}
}

// ---- registry tests -------------------------------------------------

func TestRegistry_RegisterAndLookup(t *testing.T) {
	reg := NewRegistry()
	sess := NewSession(newFakeConn(), "dev-1", "wks-1", "0.1.0", reg, nil)
	if prev := reg.Register(sess); prev != nil {
		t.Fatalf("first Register returned non-nil prev")
	}
	got, err := reg.LookupDevice("dev-1")
	if err != nil || got != sess {
		t.Fatalf("LookupDevice round-trip failed: got=%p err=%v", got, err)
	}
}

func TestRegistry_RegisterReplacesAndEvictsRuns(t *testing.T) {
	reg := NewRegistry()
	old := NewSession(newFakeConn(), "dev-1", "wks-1", "0.1.0", reg, nil)
	reg.Register(old)
	reg.AttachRun("run-1", old)
	reg.AttachPermission("perm-1", old)

	new := NewSession(newFakeConn(), "dev-1", "wks-1", "0.1.0", reg, nil)
	prev := reg.Register(new)
	if prev != old {
		t.Fatalf("expected old session as displaced previous, got %p", prev)
	}
	// Old session's run/perm indexes must be cleared so a stale
	// Cancel can't be routed to the wrong session.
	if got := reg.LookupRun("run-1"); got != nil {
		t.Fatalf("expected run-1 mapping cleared, got %p", got)
	}
	if _, err := reg.LookupPermission("perm-1"); !errors.Is(err, ErrPermissionNotRegistered) {
		t.Fatalf("expected perm-1 cleared, got %v", err)
	}
}

func TestRegistry_DeregisterPreservesNewer(t *testing.T) {
	reg := NewRegistry()
	old := NewSession(newFakeConn(), "dev-1", "wks-1", "0.1.0", reg, nil)
	reg.Register(old)
	new := NewSession(newFakeConn(), "dev-1", "wks-1", "0.1.0", reg, nil)
	reg.Register(new)
	// A stale Deregister from the old session (e.g. its read loop
	// wakes after preemption) must NOT remove the new session.
	reg.Deregister(old)
	got, err := reg.LookupDevice("dev-1")
	if err != nil || got != new {
		t.Fatalf("new session evicted by stale Deregister: got=%p err=%v", got, err)
	}
}

// ---- session tests --------------------------------------------------

func TestSession_DispatchDeliversToSubscriber(t *testing.T) {
	reg := NewRegistry()
	conn := newFakeConn()
	sess := NewSession(conn, "dev-1", "wks-1", "0.1.0", reg, nil)
	sess.Start()
	defer sess.Close("test done")

	ch, err := sess.Subscribe("run-1")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	env, _ := proto.NewEnvelope(proto.TypeDelta, "run-1", proto.DeltaPayload{Delta: "hi", Sequence: 1})
	raw, _ := jsonMarshal(env)
	conn.Feed(raw)

	select {
	case got := <-ch:
		if got.Type != proto.TypeDelta || got.ID != "run-1" {
			t.Fatalf("unexpected env: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber never received delta")
	}
}

func TestSession_DoneFrameAutoUnsubscribes(t *testing.T) {
	reg := NewRegistry()
	conn := newFakeConn()
	sess := NewSession(conn, "dev-1", "wks-1", "0.1.0", reg, nil)
	sess.Start()
	defer sess.Close("test done")

	ch, _ := sess.Subscribe("run-1")
	env, _ := proto.NewEnvelope(proto.TypeDone, "run-1", proto.DonePayload{Content: "ok"})
	raw, _ := jsonMarshal(env)
	conn.Feed(raw)

	// Drain the done, then expect the channel to be closed by the
	// auto-unsubscribe path.
	deadline := time.After(2 * time.Second)
	gotDone := false
	for {
		select {
		case env, ok := <-ch:
			if !ok {
				if !gotDone {
					t.Fatal("channel closed without delivering done envelope")
				}
				return
			}
			if env.Type == proto.TypeDone {
				gotDone = true
			}
		case <-deadline:
			t.Fatal("subscriber channel never closed after done")
		}
	}
}

func TestSession_PermissionRequestIndexedInRegistry(t *testing.T) {
	reg := NewRegistry()
	conn := newFakeConn()
	sess := NewSession(conn, "dev-1", "wks-1", "0.1.0", reg, nil)
	sess.Start()
	defer sess.Close("test done")

	env, _ := proto.NewEnvelope(proto.TypePermissionRequest, "perm-abc", proto.PermissionRequestPayload{
		Tool:  "Bash",
		Title: "rm -rf /tmp/scratch",
	})
	raw, _ := jsonMarshal(env)
	conn.Feed(raw)

	// Poll because the read loop is async.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got, err := reg.LookupPermission("perm-abc"); err == nil && got == sess {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("perm-abc never indexed in registry")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSession_CloseFansSyntheticErrorAndDone(t *testing.T) {
	reg := NewRegistry()
	conn := newFakeConn()
	sess := NewSession(conn, "dev-1", "wks-1", "0.1.0", reg, nil)
	sess.Start()

	ch, _ := sess.Subscribe("run-1")
	sess.Close("simulated drop")

	gotErr, gotDone := false, false
	deadline := time.After(2 * time.Second)
	for !gotErr || !gotDone {
		select {
		case env, ok := <-ch:
			if !ok {
				if !gotErr || !gotDone {
					t.Fatalf("channel closed before delivering synthetic error+done (gotErr=%v gotDone=%v)", gotErr, gotDone)
				}
				return
			}
			switch env.Type {
			case proto.TypeError:
				gotErr = true
			case proto.TypeDone:
				gotDone = true
			}
		case <-deadline:
			t.Fatalf("synthetic frames not delivered (gotErr=%v gotDone=%v)", gotErr, gotDone)
		}
	}
}

func TestSession_SendOnClosedReturnsError(t *testing.T) {
	reg := NewRegistry()
	sess := NewSession(newFakeConn(), "dev-1", "wks-1", "0.1.0", reg, nil)
	sess.Close("never started")
	env, _ := proto.NewEnvelope(proto.TypePromptCancel, "run-1", nil)
	err := sess.Send(context.Background(), env)
	if !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("expected ErrSessionClosed, got %v", err)
	}
}

func TestSession_SendWritesToWire(t *testing.T) {
	reg := NewRegistry()
	conn := newFakeConn()
	sess := NewSession(conn, "dev-1", "wks-1", "0.1.0", reg, nil)
	sess.Start()
	defer sess.Close("test done")

	env, _ := proto.NewEnvelope(proto.TypePromptRequest, "run-1", proto.PromptRequestPayload{
		AgentKind: "claude_code",
		RunID:     "run-1",
		Prompt:    "hello",
	})
	if err := sess.Send(context.Background(), env); err != nil {
		t.Fatalf("Send: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if len(conn.Writes()) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("write loop never flushed envelope to wire")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSession_HeartbeatPersistsSupportedAgentKinds(t *testing.T) {
	reg := NewRegistry()
	conn := newFakeConn()
	heartbeat := newFakeHeartbeatStore()
	sess := NewSession(conn, "dev-1", "wks-1", "0.1.0", reg, nil)
	sess.heartbeat = heartbeat
	sess.Start()
	defer sess.Close("test done")

	env, _ := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{
		Timestamp:      1710000000,
		ActiveRequests: 2,
		DaemonVersion:  "0.2.0-test",
		SupportedAgentKinds: []proto.SupportedAgentKind{
			{
				Kind:      "opencode",
				Available: false,
				Version:   "missing",
				Capabilities: proto.AgentKindCapabilities{
					Streaming: true,
				},
			},
			{
				Kind:      "claude_code",
				Available: true,
				Version:   "1.2.3",
				Capabilities: proto.AgentKindCapabilities{
					Streaming:   true,
					Permissions: true,
					Usage:       true,
					Resume:      true,
				},
			},
		},
	})
	raw, _ := jsonMarshal(env)
	conn.Feed(raw)

	got := heartbeat.waitDaemonHeartbeat(t)
	if got.RuntimeID != "dev-1" || got.DaemonVersion != "0.2.0-test" || got.ActiveRequests != 2 || got.HeartbeatTimestamp != 1710000000 {
		t.Fatalf("heartbeat metadata not preserved: %+v", got)
	}
	if len(got.SupportedAgentKinds) != 2 {
		t.Fatalf("SupportedAgentKinds len = %d, want 2: %#v", len(got.SupportedAgentKinds), got.SupportedAgentKinds)
	}
	byKind := map[string]store.AgentDaemonSupportedAgentKind{}
	for _, info := range got.SupportedAgentKinds {
		byKind[info.Kind] = info
	}
	claude := byKind["claude_code"]
	if !claude.Available || claude.Version != "1.2.3" || !claude.Capabilities.Permissions || !claude.Capabilities.Usage || !claude.Capabilities.Resume {
		t.Fatalf("claude_code descriptor not converted: %#v", claude)
	}
	opencode := byKind["opencode"]
	if opencode.Available || opencode.Version != "missing" || !opencode.Capabilities.Streaming {
		t.Fatalf("opencode descriptor not converted: %#v", opencode)
	}
}

func TestSession_HeartbeatInfersClaudeCodeFromLegacyFlag(t *testing.T) {
	reg := NewRegistry()
	conn := newFakeConn()
	heartbeat := newFakeHeartbeatStore()
	sess := NewSession(conn, "dev-legacy", "wks-1", "0.1.0", reg, nil)
	sess.heartbeat = heartbeat
	sess.Start()
	defer sess.Close("test done")

	env, _ := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{
		Timestamp:       1710000100,
		DaemonVersion:   "0.1.0-old",
		ClaudeAvailable: true,
	})
	raw, _ := jsonMarshal(env)
	conn.Feed(raw)

	got := heartbeat.waitDaemonHeartbeat(t)
	if len(got.SupportedAgentKinds) != 1 {
		t.Fatalf("SupportedAgentKinds len = %d, want 1: %#v", len(got.SupportedAgentKinds), got.SupportedAgentKinds)
	}
	claude := got.SupportedAgentKinds[0]
	if claude.Kind != "claude_code" || !claude.Available || !claude.Capabilities.Streaming || !claude.Capabilities.Permissions || !claude.Capabilities.Usage || !claude.Capabilities.Resume {
		t.Fatalf("legacy claude_available fallback not inferred: %#v", claude)
	}
}

// jsonMarshal aliases encoding/json.Marshal so call sites read cleanly.
func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}
