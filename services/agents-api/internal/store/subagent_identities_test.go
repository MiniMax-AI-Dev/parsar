package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func subagentIdentityEvent(child, parent string, created int64) ExecutionEvent {
	raw, _ := json.Marshal(proto.SubagentIdentityPayload{NativeID: child, ParentNativeID: parent,
		NativeCreatedAt: created, ParentTurnID: "native-turn", SourceItemID: "native-spawn-item"})
	return ExecutionEvent{Kind: proto.TypeSubagentIdentity, Payload: raw}
}

func TestSubagentIdentityIsAtomicScopedAndImmutable(t *testing.T) {
	s, pool := testStore(t)
	lease := executionLease(t, s)
	w := lease.Store()
	ctx := t.Context()
	tenant, session := newTurnSession(t, s)
	host, err := s.CreateDevice(ctx, tenant, "identity test", device.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	if err = w.BindSessionDevice(ctx, tenant, session.ID, host.ID); err != nil {
		t.Fatal(err)
	}
	input := submitMessage(t, s, tenant, session.ID, "first")
	transition(t, w, tenant, session.ID, input.TurnID, TurnQueued, TurnInProgress)
	a, b := subagentIdentityEvent("child-a", "root", 102), subagentIdentityEvent("child-b", "root", 101)
	batch := []ExecutionEvent{a, b, a}
	if err = s.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 1, batch); err == nil {
		t.Fatal("unleased discovery accepted")
	}
	for range 2 {
		if err = w.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 1, batch); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := s.GetSubagentIdentity(ctx, tenant, session.ID, "child-a")
	if err != nil || saved.ID == "" || saved.ID == saved.NativeID || saved.SessionID != session.ID || saved.FirstTurnID != input.TurnID || saved.FirstEventOrdinal != 1 || saved.NativeCreatedAt != 102 || saved.FirstObservedAt.IsZero() {
		t.Fatal(saved, err)
	}
	other, err := s.GetSubagentIdentity(ctx, tenant, session.ID, "child-b")
	if err != nil || other.ID == saved.ID || other.NativeCreatedAt != 101 || other.FirstEventOrdinal != 2 {
		t.Fatal("discovery order replaced identity or creation", other, err)
	}
	for _, owner := range []struct{ tenant, session string }{{uuid.NewString(), session.ID}, {tenant, uuid.NewString()}} {
		if _, err = s.GetSubagentIdentity(ctx, owner.tenant, owner.session, "child-a"); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign read", err)
		}
		if err = w.AppendTurnEvents(ctx, owner.tenant, owner.session, input.TurnID, 4, []ExecutionEvent{a}); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign write", err)
		}
	}
	before, err := s.SessionEventCursor(ctx, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, conflict := range []ExecutionEvent{
		subagentIdentityEvent("child-a", "other-root", 102),
		subagentIdentityEvent("child-a", "root", 103),
		subagentIdentityEvent("child-new", "other-root", 104),
	} {
		// A preceding new identity and public output must roll back with the conflict.
		bad := []ExecutionEvent{subagentIdentityEvent("rollback-child", "root", 105),
			{Kind: proto.TypeDelta, Payload: json.RawMessage(`{"delta":"must roll back","sequence":1}`)}, conflict}
		if err = w.AppendTurnEvents(ctx, tenant, session.ID, input.TurnID, 4, bad); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatal("conflicting facts accepted", err)
		}
		if _, err = s.GetSubagentIdentity(ctx, tenant, session.ID, "rollback-child"); !errors.Is(err, ErrNotFound) {
			t.Fatal("partial identity survived", err)
		}
		events, err := s.ListTurnEvents(ctx, tenant, session.ID, input.TurnID, 0, 100)
		if err != nil || len(events) != 3 {
			t.Fatal("partial journal survived", len(events), err)
		}
		cursor, err := s.SessionEventCursor(ctx, tenant, session.ID)
		if err != nil || cursor != before {
			t.Fatal("partial public projection survived", cursor, err)
		}
	}
	foreign, err := s.CreateSession(ctx, tenant, CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: "foreign"})
	if err != nil {
		t.Fatal(err)
	}
	if err = w.BindSessionDevice(ctx, tenant, foreign.ID, host.ID); err != nil {
		t.Fatal(err)
	}
	foreignInput := submitMessage(t, s, tenant, foreign.ID, "first")
	transition(t, w, tenant, foreign.ID, foreignInput.TurnID, TurnQueued, TurnInProgress)
	if err = w.AppendTurnEvents(ctx, tenant, foreign.ID, foreignInput.TurnID, 1, []ExecutionEvent{a}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("same device/native child reassigned to another Session", err)
	}
	if _, err = pool.Exec(ctx, "UPDATE session_devices SET native_session_id='known-root' WHERE session_id=$1", foreign.ID); err != nil {
		t.Fatal(err)
	}
	if err = w.AppendTurnEvents(ctx, tenant, foreign.ID, foreignInput.TurnID, 1, []ExecutionEvent{subagentIdentityEvent("other-child", "root", 101)}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("known root binding ignored", err)
	}
	if _, err = w.CompleteExecution(ctx, tenant, session.ID, input.TurnID, TurnCompleted, json.RawMessage(`{}`), "root", input.Sequence); err != nil {
		t.Fatal(err)
	}
	if err = lease.Close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, _ := testStore(t)
	nextOwner := executionLease(t, reopened)
	again, err := reopened.GetSubagentIdentity(ctx, tenant, session.ID, "child-a")
	if err != nil || !reflect.DeepEqual(again, saved) {
		t.Fatal("restart changed identity", again, err)
	}
	second := submitMessage(t, reopened, tenant, session.ID, "second")
	transition(t, nextOwner.Store(), tenant, session.ID, second.TurnID, TurnQueued, TurnInProgress)
	if err = w.AppendTurnEvents(ctx, tenant, session.ID, second.TurnID, 1, []ExecutionEvent{a}); err == nil {
		t.Fatal("closed owner wrote identity")
	}
	continued := proto.SubagentIdentityPayload{NativeID: "child-a", ParentNativeID: "root", NativeCreatedAt: 102, ParentTurnID: "later-native-turn", SourceItemID: "resume-item"}
	raw, _ := json.Marshal(continued)
	if err = nextOwner.Store().AppendTurnEvents(ctx, tenant, session.ID, second.TurnID, 1, []ExecutionEvent{{Kind: proto.TypeSubagentIdentity, Payload: raw}}); err != nil {
		t.Fatal(err)
	}
	again, err = reopened.GetSubagentIdentity(ctx, tenant, session.ID, "child-a")
	if err != nil || !reflect.DeepEqual(again, saved) {
		t.Fatal("continuation changed immutable first observation", again, err)
	}
	if err = reopened.DeleteSession(ctx, tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.GetSubagentIdentity(ctx, tenant, session.ID, "child-a"); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted Session exposed identity", err)
	}
}

func TestSubagentIdentityRejectsLostLease(t *testing.T) {
	s, pool := testStore(t)
	old := executionLease(t, s)
	tenant, session := newTurnSession(t, s)
	input := submitMessage(t, s, tenant, session.ID, "first")
	transition(t, old.Store(), tenant, session.ID, input.TurnID, TurnQueued, TurnInProgress)
	var killed bool
	if err := pool.QueryRow(t.Context(), "SELECT pg_terminate_backend($1, 1000)", old.conn.Conn().PgConn().PID()).Scan(&killed); err != nil || !killed {
		t.Fatal(killed, err)
	}
	successor := executionLease(t, s)
	if err := old.Store().AppendTurnEvents(t.Context(), tenant, session.ID, input.TurnID, 1, []ExecutionEvent{subagentIdentityEvent("child", "root", 100)}); err == nil {
		t.Fatal("lost owner committed identity")
	}
	if err := successor.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListTurnEvents(t.Context(), tenant, session.ID, input.TurnID, 0, 100)
	if err != nil || len(events) != 0 {
		t.Fatal(events, err)
	}
}
