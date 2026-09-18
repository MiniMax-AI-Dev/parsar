package store

import (
	"errors"
	"testing"
)

func TestSessionExecutionBindingRetainsStartedExecutionRequirement(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := newTurnSession(t, s)
	foreign, _ := newTurnSession(t, s)
	device, _ := registerTestDevice(t, s, tenant)
	if err := s.BindSessionDevice(t.Context(), tenant, session.ID, device.ID); err != nil {
		t.Fatal(err)
	}
	assertStarted := func(st *Store, want bool) {
		t.Helper()
		bound, err := st.GetSessionExecutionBinding(t.Context(), tenant, session.ID)
		if err != nil || bound.HasStartedTurn != want || bound.NativeSessionID != "" {
			t.Fatalf("binding=%+v err=%v", bound, err)
		}
		if _, err := st.GetSessionExecutionBinding(t.Context(), foreign, session.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign binding", err)
		}
	}
	assertStarted(s, false)
	first := submitMessage(t, s, tenant, session.ID, "first")
	assertStarted(s, false)
	transition(t, s, tenant, session.ID, first.TurnID, TurnQueued, TurnInProgress)
	assertStarted(s, true)
	transition(t, s, tenant, session.ID, first.TurnID, TurnInProgress, TurnFailed)
	pool.Close()
	restarted, _ := testStore(t)
	assertStarted(restarted, true)
	submitMessage(t, restarted, tenant, session.ID, "next")
	assertStarted(restarted, true)
}
