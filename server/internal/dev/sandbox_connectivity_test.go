package dev

// Tests for the per-binding sandbox connectivity test endpoint.
// Confirms the agent_daemon-flavoured probe:
//
//   binding missing                 -> 404
//   binding has no device_id        -> daemon_paired fail, runtimeDown
//   runtime row missing             -> daemon_paired fail, runtimeDown
//   runtime offline / pending       -> daemon_online fail, unreachable
//   heartbeat stale                 -> daemon_online fail, unreachable
//   liveness=online + fresh hb      -> both pass, overall=pass

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

const (
	connTestWorkspaceID = "00000000-0000-0000-0000-000000000002"
	connTestAgentID     = "00000000-0000-0000-0000-000000000009"
	connTestDeviceID    = "11111111-2222-3333-4444-555555555555"
)

func newConnectivityTestRouter(deps sandboxAdminDeps) chi.Router {
	r := chi.NewRouter()
	r.Post(
		"/api/v1/workspaces/{workspaceID}/agents/{agentID}/sandbox/test-connection",
		sandboxConnectivityTest(deps, nil),
	)
	return r
}

func newConnectivityRequest(t *testing.T) *http.Request {
	t.Helper()
	url := "/api/v1/workspaces/" + connTestWorkspaceID +
		"/agents/" + connTestAgentID + "/sandbox/test-connection"
	return httptest.NewRequest(http.MethodPost, url, strings.NewReader(""))
}

func decodeConnectivityResp(t *testing.T, body []byte) sandboxConnectivityResponse {
	t.Helper()
	var resp sandboxConnectivityResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode response: %v\nbody=%s", err, body)
	}
	return resp
}

func findCheck(t *testing.T, resp sandboxConnectivityResponse, name string) sandboxConnectivityCheck {
	t.Helper()
	for _, c := range resp.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q not present; got %+v", name, resp.Checks)
	return sandboxConnectivityCheck{}
}

func TestSandboxConnectivityReturns404WhenNoBinding(t *testing.T) {
	deps := sandboxAdminDeps{store: &fakeSandboxBindingStore{}}
	rec := httptest.NewRecorder()
	newConnectivityTestRouter(deps).ServeHTTP(rec, newConnectivityRequest(t))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestSandboxConnectivityFailsWhenBindingHasNoDeviceID(t *testing.T) {
	storeFake := &fakeSandboxBindingStore{
		binding: &store.SandboxBindingRead{
			ID:          "b1",
			WorkspaceID: connTestWorkspaceID,
			SandboxID:   "sbx_no_device",
			Status:      store.SandboxBindingStatusActive,
			Metadata:    map[string]any{}, // intentionally empty
		},
	}
	deps := sandboxAdminDeps{store: storeFake}
	rec := httptest.NewRecorder()
	newConnectivityTestRouter(deps).ServeHTTP(rec, newConnectivityRequest(t))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	resp := decodeConnectivityResp(t, rec.Body.Bytes())
	if resp.Overall != "fail" {
		t.Errorf("overall = %q, want fail", resp.Overall)
	}
	paired := findCheck(t, resp, sandboxConnectivityCheckPaired)
	if paired.Pass {
		t.Error("daemon_paired pass=true, want false")
	}
	if paired.Error == nil || paired.Error.Category != tryConnectionErrRuntimeDown {
		t.Errorf("paired.error = %+v, want category=%s", paired.Error, tryConnectionErrRuntimeDown)
	}
	if len(resp.Checks) != 1 {
		t.Errorf("checks = %d, want 1 (daemon_online must be skipped)", len(resp.Checks))
	}
}

func TestSandboxConnectivityFailsWhenRuntimeRowMissing(t *testing.T) {
	storeFake := &fakeSandboxBindingStore{
		binding: &store.SandboxBindingRead{
			ID:          "b2",
			WorkspaceID: connTestWorkspaceID,
			SandboxID:   "sbx_orphan",
			Status:      store.SandboxBindingStatusActive,
			Metadata:    map[string]any{"device_id": connTestDeviceID},
		},
		// runtimes map intentionally empty -> GetRuntime returns ok=false
	}
	deps := sandboxAdminDeps{store: storeFake}
	rec := httptest.NewRecorder()
	newConnectivityTestRouter(deps).ServeHTTP(rec, newConnectivityRequest(t))

	resp := decodeConnectivityResp(t, rec.Body.Bytes())
	paired := findCheck(t, resp, sandboxConnectivityCheckPaired)
	if paired.Pass || paired.Error == nil || paired.Error.Category != tryConnectionErrRuntimeDown {
		t.Errorf("paired = %+v, want fail+runtimeDown", paired)
	}
	if got := storeFake.getRuntimeCalls; len(got) != 1 || got[0] != connTestDeviceID {
		t.Errorf("GetRuntime calls = %v, want [%s]", got, connTestDeviceID)
	}
}

func TestSandboxConnectivityFailsWhenLivenessOffline(t *testing.T) {
	storeFake := &fakeSandboxBindingStore{
		binding: &store.SandboxBindingRead{
			ID:          "b3",
			WorkspaceID: connTestWorkspaceID,
			SandboxID:   "sbx_offline",
			Status:      store.SandboxBindingStatusActive,
			Metadata:    map[string]any{"device_id": connTestDeviceID},
		},
		runtimes: map[string]store.RuntimeRead{
			connTestDeviceID: {
				ID:       connTestDeviceID,
				Type:     store.RuntimeTypeAgentDaemon,
				Liveness: store.RuntimeLivenessOffline,
			},
		},
	}
	deps := sandboxAdminDeps{store: storeFake}
	rec := httptest.NewRecorder()
	newConnectivityTestRouter(deps).ServeHTTP(rec, newConnectivityRequest(t))

	resp := decodeConnectivityResp(t, rec.Body.Bytes())
	if resp.Overall != "partial" {
		t.Errorf("overall = %q, want partial (paired passes, online fails)", resp.Overall)
	}
	online := findCheck(t, resp, sandboxConnectivityCheckOnline)
	if online.Pass {
		t.Error("daemon_online pass=true, want false")
	}
	if online.Error == nil || online.Error.Category != tryConnectionErrUnreachable {
		t.Errorf("online.error = %+v, want category=%s", online.Error, tryConnectionErrUnreachable)
	}
}

func TestSandboxConnectivityFailsWhenHeartbeatStale(t *testing.T) {
	old := time.Now().UTC().Add(-10 * time.Minute)
	storeFake := &fakeSandboxBindingStore{
		binding: &store.SandboxBindingRead{
			ID:          "b4",
			WorkspaceID: connTestWorkspaceID,
			SandboxID:   "sbx_stale",
			Status:      store.SandboxBindingStatusActive,
			Metadata:    map[string]any{"device_id": connTestDeviceID},
		},
		runtimes: map[string]store.RuntimeRead{
			connTestDeviceID: {
				ID:              connTestDeviceID,
				Type:            store.RuntimeTypeAgentDaemon,
				Liveness:        store.RuntimeLivenessOnline,
				LastHeartbeatAt: &old,
			},
		},
	}
	deps := sandboxAdminDeps{store: storeFake}
	rec := httptest.NewRecorder()
	newConnectivityTestRouter(deps).ServeHTTP(rec, newConnectivityRequest(t))

	resp := decodeConnectivityResp(t, rec.Body.Bytes())
	online := findCheck(t, resp, sandboxConnectivityCheckOnline)
	if online.Pass {
		t.Error("daemon_online pass=true on stale heartbeat, want false")
	}
	if online.Error == nil || online.Error.Category != tryConnectionErrUnreachable {
		t.Errorf("online.error = %+v, want unreachable", online.Error)
	}
}

func TestSandboxConnectivityPassesWhenDaemonHealthy(t *testing.T) {
	now := time.Now().UTC()
	storeFake := &fakeSandboxBindingStore{
		binding: &store.SandboxBindingRead{
			ID:          "b5",
			WorkspaceID: connTestWorkspaceID,
			SandboxID:   "sbx_ok",
			Status:      store.SandboxBindingStatusActive,
			Metadata:    map[string]any{"device_id": connTestDeviceID},
		},
		runtimes: map[string]store.RuntimeRead{
			connTestDeviceID: {
				ID:              connTestDeviceID,
				Type:            store.RuntimeTypeAgentDaemon,
				Liveness:        store.RuntimeLivenessOnline,
				LastHeartbeatAt: &now,
			},
		},
	}
	deps := sandboxAdminDeps{store: storeFake}
	rec := httptest.NewRecorder()
	newConnectivityTestRouter(deps).ServeHTTP(rec, newConnectivityRequest(t))

	resp := decodeConnectivityResp(t, rec.Body.Bytes())
	if resp.Overall != "pass" {
		t.Fatalf("overall = %q, want pass; resp=%+v", resp.Overall, resp)
	}
	if len(resp.Checks) != 2 {
		t.Fatalf("checks = %d, want 2", len(resp.Checks))
	}
	for _, c := range resp.Checks {
		if !c.Pass {
			t.Errorf("check %q pass=false, want true; err=%+v", c.Name, c.Error)
		}
	}
	if resp.SandboxID != "sbx_ok" {
		t.Errorf("sandbox_id = %q, want sbx_ok", resp.SandboxID)
	}
}
