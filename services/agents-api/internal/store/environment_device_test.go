package store_test

import (
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestWorkerEnvironmentSelectsCapableDeviceWithoutMovingBinding(t *testing.T) {
	for _, missing := range []string{"preparation", "remote_environment", "durable_input_receipts"} {
		t.Run(missing, func(t *testing.T) {
			h := newDispatchHarness(t)
			_, pool := store.NewTestStore(t)
			released := enableWorkerEnvironment(t, h)
			caps := workerEnvironmentCapabilities()
			caps.Preparation = missing != "preparation"
			caps.RemoteEnvironment = missing != "remote_environment"
			caps.DurableInputReceipts = missing != "durable_input_receipts"
			h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: caps}}})
			awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "reduced device capabilities", func() bool {
				peer, err := h.registry.LookupDevice(h.device.ID)
				if err != nil {
					return false
				}
				info, _, _ := peer.AgentKindStatus("codex")
				return info.Capabilities.Preparation == caps.Preparation && info.Capabilities.RemoteEnvironment == caps.RemoteEnvironment && info.Capabilities.DurableInputReceipts == caps.DurableInputReceipts
			})
			pending := unboundWorkerEnvironmentReservation(t, h)
			bound := workerEnvironmentReservation(t, h)
			frames := workerFrames(t, h)
			_, stop := startEnvironmentExpiryWorker(t, h.d)
			select {
			case frame := <-frames:
				t.Fatal("incapable device received work", frame.Type)
			case <-time.After(time.Second):
			}
			if _, err := h.s.GetSessionDevice(t.Context(), h.tenant, pending.SessionID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("incapable device was bound", err)
			}
			other := connectWorkerEnvironmentDevice(t, h)
			otherFrames := workerFrames(t, other)
			request := nextWorkerFrame(t, otherFrames, proto.TypeExecutionPrepare)
			selected, err := h.s.GetSessionDevice(t.Context(), h.tenant, pending.SessionID)
			if err != nil || selected.ID != other.device.ID {
				t.Fatal("eligible device was not bound before preparation", selected, err)
			}
			original, err := h.s.GetSessionDevice(t.Context(), h.tenant, bound.SessionID)
			if err != nil || original.ID != h.device.ID {
				t.Fatal("existing binding was moved", original, err)
			}
			handle := acknowledgePreparation(other, request.ID)
			other.write(request.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "failed"})
			nextWorkerFrame(t, otherFrames, proto.TypeExecutionRelease)
			stop()
			if released.Load() != 1 {
				t.Fatal("preparation owner not released")
			}
			for _, value := range []store.EnvironmentInputReservation{pending, bound} {
				stored, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, value.SessionID, value.ID)
				if err != nil || stored.State != store.EnvironmentInputPending || !stored.Deadline.Equal(value.Deadline) {
					t.Fatal("device selection changed pending input", stored, err)
				}
				assertEnvironmentExpiryHasNoHistory(t, pool, value.SessionID)
			}
		})
	}
}

func connectWorkerEnvironmentDevice(t *testing.T, h *dispatchHarness) *dispatchHarness {
	t.Helper()
	other := *h
	other.credential = uuid.NewString()
	var err error
	other.device, err = h.s.CreateDevice(t.Context(), h.tenant, "capable alternative", device.HashCredential(other.credential))
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(h.url)
	if err != nil {
		t.Fatal(err)
	}
	u.Scheme, u.Path = "ws", "/api/v1/agent-daemon/ws"
	u.RawQuery = url.Values{"device_id": {other.device.ID}, "token": {other.credential}, "version": {proto.Version}}.Encode()
	other.conn, _, err = websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Fatal("alternative device connection failed")
	}
	t.Cleanup(func() { _ = other.conn.Close() })
	other.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: workerEnvironmentCapabilities()}}})
	awaitDaemonRemoteCondition(t, t.Context(), 3*time.Second, "alternative device capabilities", func() bool {
		peer, err := h.registry.LookupDevice(other.device.ID)
		if err != nil {
			return false
		}
		info, found, known := peer.AgentKindStatus("codex")
		return known && found && info.Capabilities.Preparation
	})
	return &other
}
