package store_test

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestEnvironmentInputWorkFiltersAndPagesDevices(t *testing.T) {
	h := newDispatchHarness(t)
	_, pool := store.NewTestStore(t)
	wanted := map[string]string{}
	for range 103 {
		pending := workerEnvironmentReservation(t, h)
		wanted[pending.ID] = pending.SessionID
	}
	for _, state := range []string{"expired", "cancelled", "deleted", "unbound", "revoked", "disconnected"} {
		pending := workerEnvironmentReservation(t, h)
		switch state {
		case "expired":
			makeEnvironmentExpiryDue(t, pool, &pending)
		case "cancelled":
			if _, err := h.s.CancelEnvironmentInput(t.Context(), h.tenant, pending.SessionID, pending.ID); err != nil {
				t.Fatal(err)
			}
		case "deleted":
			if err := h.s.DeleteSession(t.Context(), h.tenant, pending.SessionID); err != nil {
				t.Fatal(err)
			}
		case "unbound":
			wanted[pending.ID] = pending.SessionID
			if _, err := pool.Exec(t.Context(), "DELETE FROM session_devices WHERE session_id=$1", pending.SessionID); err != nil {
				t.Fatal(err)
			}
		default:
			other, err := h.s.CreateDevice(t.Context(), h.tenant, state, device.HashCredential(uuid.NewString()))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), "UPDATE session_devices SET device_id=$2 WHERE session_id=$1", pending.SessionID, other.ID); err != nil {
				t.Fatal(err)
			}
			if state == "revoked" {
				if err := h.s.RevokeDevice(t.Context(), h.tenant, other.ID); err != nil {
					t.Fatal(err)
				}
				work, err := h.s.ListEnvironmentInputWork(t.Context(), "", []string{other.ID})
				if err != nil || len(work) != 0 {
					t.Fatal("revoked device selected", work, err)
				}
			}
		}
	}
	for _, devices := range [][]string{nil, {}, {uuid.NewString()}} {
		work, err := h.s.ListEnvironmentInputWork(t.Context(), "", devices)
		if err != nil || len(work) != 0 {
			t.Fatal("unconnected work selected", work, err)
		}
	}
	foreign := *h
	foreign.tenant = uuid.NewString()
	unboundWorkerEnvironmentReservation(t, &foreign)
	seen, cursor := 0, ""
	for _, count := range []int{100, 4, 0} {
		work, err := h.s.ListEnvironmentInputWork(t.Context(), cursor, []string{h.device.ID})
		if err != nil || len(work) != count {
			t.Fatal("environment work page", len(work), count, err)
		}
		for _, item := range work {
			if item.TenantID != h.tenant || item.SessionID != wanted[item.ReservationID] || item.ReservationID <= cursor {
				t.Fatal("wrong scope or pagination", item)
			}
			cursor = item.ReservationID
			seen++
		}
	}
	if seen != len(wanted) {
		t.Fatal("pending work lost across pages")
	}
}
