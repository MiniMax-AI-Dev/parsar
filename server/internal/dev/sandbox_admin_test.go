package dev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

// fakeSandboxBindingStore is the test-side SandboxBindingStore stand-in.
type fakeSandboxBindingStore struct {
	mu      sync.Mutex
	binding *store.SandboxBindingRead
	getErr  error
	markErr error
	marked  []struct {
		BindingID string
		Status    string
	}
	listRows  []store.SandboxBindingRead
	listErr   error
	listLimit int32

	// Runtime stub — keyed by runtime id. Used by
	// sandboxConnectivityTest (probes agent_daemon liveness). Tests
	// that don't exercise the connectivity endpoint can leave both
	// fields zero — GetRuntime then returns (zero, false, nil).
	runtimes        map[string]store.RuntimeRead
	getRuntimeErr   error
	getRuntimeCalls []string

	// Agent detail stub — used by rebuildSandbox to load
	// agent.config (sandbox_size etc.) before re-Acquire. Tests that
	// don't care about template selection can leave both fields zero —
	// the fake returns an empty AgentStatusRead with the input
	// id and no error, which makes resolveTemplate fall through to the
	// default size.
	agentDetails   map[string]store.AgentStatusRead
	agentDetailErr error
}

func (f *fakeSandboxBindingStore) GetActiveSandboxBindingForAgent(_ context.Context, _, _ string) (store.SandboxBindingRead, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return store.SandboxBindingRead{}, false, f.getErr
	}
	if f.binding == nil {
		return store.SandboxBindingRead{}, false, nil
	}
	return *f.binding, true, nil
}

func (f *fakeSandboxBindingStore) MarkSandboxBindingKilled(_ context.Context, bindingID, status string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markErr != nil {
		return f.markErr
	}
	f.marked = append(f.marked, struct {
		BindingID string
		Status    string
	}{bindingID, status})
	return nil
}

func (f *fakeSandboxBindingStore) ListActiveSandboxBindings(_ context.Context, _ string, limit int32) ([]store.SandboxBindingRead, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listLimit = limit
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]store.SandboxBindingRead, len(f.listRows))
	copy(out, f.listRows)
	return out, nil
}

func (f *fakeSandboxBindingStore) GetRuntime(_ context.Context, runtimeID string) (store.RuntimeRead, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getRuntimeCalls = append(f.getRuntimeCalls, runtimeID)
	if f.getRuntimeErr != nil {
		return store.RuntimeRead{}, false, f.getRuntimeErr
	}
	rt, ok := f.runtimes[runtimeID]
	if !ok {
		return store.RuntimeRead{}, false, nil
	}
	return rt, true, nil
}

func (f *fakeSandboxBindingStore) GetAgentDetail(_ context.Context, agentID string) (store.AgentStatusRead, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.agentDetailErr != nil {
		return store.AgentStatusRead{}, f.agentDetailErr
	}
	detail, ok := f.agentDetails[agentID]
	if !ok {
		// Behave like the real store: empty config when the agent has
		// no overrides. Tests that want to inject sandbox_size populate
		// f.agentDetails ahead of time.
		return store.AgentStatusRead{AgentID: agentID}, nil
	}
	return detail, nil
}

// fakeDaemonManager is the test-side AgentDaemonSandboxManager stand-in.
type fakeDaemonManager struct {
	mu         sync.Mutex
	released   []string
	releaseErr error

	// Acquire bookkeeping — captures the agent_id of each call
	// so rebuild/acquire tests can assert that Acquire was invoked in
	// the background goroutine. acquireDeviceID defaults to "fake-device"
	// when unset.
	acquireDeviceID string
	acquireCalls    []string
	acquireInputs   []connector.PromptInput
}

func (f *fakeDaemonManager) Acquire(_ context.Context, in connector.PromptInput) (string, error) {
	f.mu.Lock()
	device := f.acquireDeviceID
	if device == "" {
		device = "fake-device"
	}
	f.acquireCalls = append(f.acquireCalls, in.AgentID)
	f.acquireInputs = append(f.acquireInputs, in)
	f.mu.Unlock()
	return device, nil
}

func (f *fakeDaemonManager) inputs() []connector.PromptInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]connector.PromptInput, len(f.acquireInputs))
	copy(out, f.acquireInputs)
	return out
}

func (f *fakeDaemonManager) SandboxStatus(_ context.Context, _ string) (connector.SandboxInfo, bool, error) {
	return connector.SandboxInfo{}, false, nil
}

func (f *fakeDaemonManager) Renew(_ context.Context, _ string) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (f *fakeDaemonManager) SandboxRuntimeInfo(_ context.Context, _ string) (time.Time, error) {
	return time.Time{}, nil
}

func (f *fakeDaemonManager) Release(_ context.Context, agentID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.releaseErr != nil {
		return f.releaseErr
	}
	f.released = append(f.released, agentID)
	return nil
}

func newSandboxTestRouter(deps sandboxAdminDeps) chi.Router {
	r := chi.NewRouter()
	rt := &recordingRuntimeStore{}
	r.Get("/api/v1/workspaces/{workspaceID}/agents/{agentID}/sandbox", getSandboxStatus(deps, nil))
	r.Post("/api/v1/workspaces/{workspaceID}/agents/{agentID}/sandbox/kill", killSandbox(deps, rt, nil))
	r.Post("/api/v1/workspaces/{workspaceID}/agents/{agentID}/sandbox/rebuild", rebuildSandbox(deps, rt, nil))
	r.Get("/api/v1/workspaces/{workspaceID}/sandboxes", listSandboxes(deps))
	return r
}

// recordingRuntimeStore is the sandbox_admin_test slice of RuntimeStore
// — only SetAgentRuntime is actually exercised, so we embed the
// shared stubRuntimeStore (defined in routes_test.go) for the other
// 100+ methods and override the one we care about to record calls.
//
// setRuntimeCall is the captured input from each SetAgentRuntime
// invocation. Empty RuntimeID means the handler asked to clear the
// binding (kill path). Non-empty means it wrote a fresh device id
// (rebuild / acquire success path).
type recordingRuntimeStore struct {
	stubRuntimeStore
	mu       sync.Mutex
	setCalls []store.SetAgentRuntimeInput
	setErr   error
}

func (r *recordingRuntimeStore) SetAgentRuntime(_ context.Context, input store.SetAgentRuntimeInput) (store.AgentRuntimeBinding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setCalls = append(r.setCalls, input)
	if r.setErr != nil {
		return store.AgentRuntimeBinding{}, r.setErr
	}
	return store.AgentRuntimeBinding{
		AgentID:     input.AgentID,
		WorkspaceID: input.WorkspaceID,
	}, nil
}

func (r *recordingRuntimeStore) calls() []store.SetAgentRuntimeInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]store.SetAgentRuntimeInput, len(r.setCalls))
	copy(out, r.setCalls)
	return out
}

func TestSandboxAdminStatusReturnsLiveBinding(t *testing.T) {
	now := time.Now().UTC()
	storeFake := &fakeSandboxBindingStore{
		binding: &store.SandboxBindingRead{
			ID:           "11111111-1111-1111-1111-111111111111",
			WorkspaceID:  "00000000-0000-0000-0000-000000000002",
			AgentID:      strPtr("00000000-0000-0000-0000-000000000009"),
			SandboxID:    "sbx_abc",
			TemplateID:   "parsar-sandbox-e2b",
			Status:       store.SandboxBindingStatusActive,
			CreatedAt:    now.Add(-2 * time.Minute),
			LastActiveAt: now.Add(-30 * time.Second),
			Metadata:     map[string]any{"source": "test"},
		},
	}
	router := newSandboxTestRouter(sandboxAdminDeps{store: storeFake})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents/00000000-0000-0000-0000-000000000009/sandbox", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp sandboxStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.SandboxID != "sbx_abc" || resp.StatusKind != "live" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestSandboxAdminStatusReturnsNullOnNoBinding(t *testing.T) {
	// "No active binding" is the normal empty state — agent hasn't
	// run yet, was idle-reaped, or isn't a sandbox-runtime agent at
	// all. The endpoint returns 200 + JSON `null` so it doesn't show
	// up as a "404 no active sandbox binding" error in devtools /
	// server logs.
	router := newSandboxTestRouter(sandboxAdminDeps{store: &fakeSandboxBindingStore{}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents/00000000-0000-0000-0000-000000000009/sandbox", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "null" {
		t.Errorf("expected body=null; got %q", got)
	}
}

func TestSandboxAdminStatus503WhenStoreNotWired(t *testing.T) {
	router := newSandboxTestRouter(sandboxAdminDeps{store: nil})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents/00000000-0000-0000-0000-000000000009/sandbox", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503; got %d", rec.Code)
	}
}

func TestSandboxAdminKillMarksDBAndCallsRelease(t *testing.T) {
	binding := store.SandboxBindingRead{
		ID:        "11111111-1111-1111-1111-111111111111",
		SandboxID: "sbx_abc",
		Status:    store.SandboxBindingStatusActive,
	}
	storeFake := &fakeSandboxBindingStore{binding: &binding}
	daemonFake := &fakeDaemonManager{}
	runtimeFake := &recordingRuntimeStore{}
	deps := sandboxAdminDeps{store: storeFake, daemonMgr: daemonFake}

	r := chi.NewRouter()
	r.Post("/api/v1/workspaces/{workspaceID}/agents/{agentID}/sandbox/kill", killSandbox(deps, runtimeFake, daemonFake))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents/00000000-0000-0000-0000-000000000009/sandbox/kill", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := daemonFake.released; len(got) != 1 || got[0] != "00000000-0000-0000-0000-000000000009" {
		t.Errorf("Release should be called once with agent_id; got %v", got)
	}
	if got := storeFake.marked; len(got) != 1 || got[0].BindingID != binding.ID || got[0].Status != "killed" {
		t.Errorf("MarkSandboxBindingKilled should be called once; got %v", got)
	}
	// Kill must also clear agents.runtime_id so dispatch
	// stops handing out the dead device.
	calls := runtimeFake.calls()
	if len(calls) != 1 {
		t.Fatalf("SetAgentRuntime should be called exactly once on kill; got %d calls: %+v", len(calls), calls)
	}
	if calls[0].RuntimeID != "" {
		t.Errorf("kill should clear runtime_id (empty string); got %q", calls[0].RuntimeID)
	}
	if calls[0].AgentID != "00000000-0000-0000-0000-000000000009" {
		t.Errorf("SetAgentRuntime should target the agent from the URL; got %q", calls[0].AgentID)
	}
}

func TestSandboxAdminKill404OnMissingBinding(t *testing.T) {
	router := newSandboxTestRouter(sandboxAdminDeps{store: &fakeSandboxBindingStore{}})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents/00000000-0000-0000-0000-000000000009/sandbox/kill", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 on missing binding; got %d", rec.Code)
	}
}

func TestSandboxAdminRebuildKillsAndReProvisions(t *testing.T) {
	binding := store.SandboxBindingRead{
		ID:        "11111111-1111-1111-1111-111111111111",
		SandboxID: "sbx_abc",
		Status:    store.SandboxBindingStatusActive,
	}
	storeFake := &fakeSandboxBindingStore{
		binding: &binding,
		agentDetails: map[string]store.AgentStatusRead{
			"00000000-0000-0000-0000-000000000009": {
				AgentID: "00000000-0000-0000-0000-000000000009",
				Config:  map[string]any{"sandbox_size": "xl"},
			},
		},
	}
	daemonFake := &fakeDaemonManager{acquireDeviceID: "device-new"}
	runtimeFake := &recordingRuntimeStore{}
	deps := sandboxAdminDeps{store: storeFake, daemonMgr: daemonFake}

	r := chi.NewRouter()
	r.Post("/api/v1/workspaces/{workspaceID}/agents/{agentID}/sandbox/rebuild", rebuildSandbox(deps, runtimeFake, daemonFake))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents/00000000-0000-0000-0000-000000000009/sandbox/rebuild", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := daemonFake.released; len(got) != 1 {
		t.Errorf("Release should be called once; got %v", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"action":"rebuilt"`) {
		t.Errorf("response action should be 'rebuilt'; body=%s", body)
	}
	// Rebuild fires Acquire + SetAgentRuntime in a background
	// goroutine. Poll briefly to give it room to land before failing
	// — bounded so a regression that drops the call still surfaces.
	if err := waitFor(t, 2*time.Second, func() bool {
		return len(runtimeFake.calls()) >= 1
	}); err != nil {
		t.Fatalf("rebuild should call SetAgentRuntime after re-acquire: %v", err)
	}
	calls := runtimeFake.calls()
	if len(calls) != 1 {
		t.Fatalf("SetAgentRuntime should be called exactly once on rebuild; got %d: %+v", len(calls), calls)
	}
	if calls[0].RuntimeID != "device-new" {
		t.Errorf("rebuild should write the new device id; got %q", calls[0].RuntimeID)
	}
	inputs := daemonFake.inputs()
	if len(inputs) != 1 || inputs[0].AgentConfig["sandbox_size"] != "xl" {
		t.Fatalf("rebuild should pass agent config to Acquire; got %+v", inputs)
	}
}

func TestSandboxAdminListReturnsActiveBindings(t *testing.T) {
	now := time.Now().UTC()
	storeFake := &fakeSandboxBindingStore{
		listRows: []store.SandboxBindingRead{
			{
				ID:           "00000000-0000-0000-0000-000000000aa1",
				WorkspaceID:  "00000000-0000-0000-0000-000000000002",
				AgentID:      strPtr("00000000-0000-0000-0000-000000000009"),
				SandboxID:    "sbx_alpha",
				TemplateID:   "parsar-sandbox-e2b",
				Status:       store.SandboxBindingStatusActive,
				CreatedAt:    now.Add(-5 * time.Minute),
				LastActiveAt: now.Add(-30 * time.Second),
				Metadata:     map[string]any{"sandbox_kind": "agent_daemon"},
			},
			{
				ID:           "00000000-0000-0000-0000-000000000aa2",
				WorkspaceID:  "00000000-0000-0000-0000-000000000002",
				AgentID:      strPtr("00000000-0000-0000-0000-000000000010"),
				SandboxID:    "sbx_beta",
				TemplateID:   "parsar-sandbox-e2b",
				Status:       store.SandboxBindingStatusSpawning,
				CreatedAt:    now.Add(-1 * time.Minute),
				LastActiveAt: now.Add(-1 * time.Minute),
				Metadata:     map[string]any{"sandbox_kind": "agent_daemon"},
			},
		},
	}
	router := newSandboxTestRouter(sandboxAdminDeps{store: storeFake})

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/workspaces/00000000-0000-0000-0000-000000000002/sandboxes", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", rec.Code, rec.Body.String())
	}
	var got listSandboxesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if n := len(got.Sandboxes); n != 2 {
		t.Fatalf("expected 2 sandboxes; got %d", n)
	}
	if got.Sandboxes[0].StatusKind != "live" {
		t.Errorf("active row should map to status_kind=live; got %q", got.Sandboxes[0].StatusKind)
	}
	if got.Sandboxes[1].StatusKind != "transient" {
		t.Errorf("spawning row should map to status_kind=transient; got %q", got.Sandboxes[1].StatusKind)
	}
}

func TestSandboxAdminListEmptyReturnsEmptyArray(t *testing.T) {
	router := newSandboxTestRouter(sandboxAdminDeps{store: &fakeSandboxBindingStore{}})

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/workspaces/00000000-0000-0000-0000-000000000002/sandboxes", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"sandboxes":[]`) {
		t.Errorf("expected empty array literal in body; got %s", rec.Body.String())
	}
}

func TestSandboxAdminList503WhenStoreNotWired(t *testing.T) {
	router := newSandboxTestRouter(sandboxAdminDeps{})

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/workspaces/00000000-0000-0000-0000-000000000002/sandboxes", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503; got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSandboxAdminList400OnInvalidWorkspaceID(t *testing.T) {
	router := newSandboxTestRouter(sandboxAdminDeps{store: &fakeSandboxBindingStore{}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/not-a-uuid/sandboxes", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400; got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSandboxAdminListLimitOverride(t *testing.T) {
	storeFake := &fakeSandboxBindingStore{}
	router := newSandboxTestRouter(sandboxAdminDeps{store: storeFake})

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/workspaces/00000000-0000-0000-0000-000000000002/sandboxes?limit=25", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200; got %d body=%s", rec.Code, rec.Body.String())
	}
	if storeFake.listLimit != 25 {
		t.Errorf("?limit=25 should reach the store; got listLimit=%d", storeFake.listLimit)
	}
}

func TestSandboxAdminListLimitNegativeIgnored(t *testing.T) {
	cases := []string{"-5", "foo", "0"}
	for _, raw := range cases {
		t.Run("limit="+raw, func(t *testing.T) {
			storeFake := &fakeSandboxBindingStore{}
			router := newSandboxTestRouter(sandboxAdminDeps{store: storeFake})

			req := httptest.NewRequest(http.MethodGet,
				"/api/v1/workspaces/00000000-0000-0000-0000-000000000002/sandboxes?limit="+raw, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200; got %d body=%s", rec.Code, rec.Body.String())
			}
			if storeFake.listLimit != 0 {
				t.Errorf("invalid limit should be ignored; got listLimit=%d", storeFake.listLimit)
			}
		})
	}
}

var _ = errors.New

func strPtr(s string) *string { return &s }

// waitFor polls cond until it returns true or timeout elapses. Used by
// tests that assert on side-effects of a fire-and-forget goroutine
// (rebuild / acquire) without a hard sleep.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	if cond() {
		return nil
	}
	return errors.New("waitFor: condition never satisfied within " + timeout.String())
}

func TestSandboxAdminAcquireWritesRuntimeIDOnSuccess(t *testing.T) {
	// No active binding → handler should kick off Acquire in a goroutine
	// and, on success, persist the new device id to
	// agents.runtime_id.
	storeFake := &fakeSandboxBindingStore{
		agentDetails: map[string]store.AgentStatusRead{
			"00000000-0000-0000-0000-000000000009": {
				AgentID: "00000000-0000-0000-0000-000000000009",
				Config:  map[string]any{"sandbox_size": "xl"},
			},
		},
	} // no binding
	daemonFake := &fakeDaemonManager{acquireDeviceID: "device-fresh"}
	runtimeFake := &recordingRuntimeStore{}
	deps := sandboxAdminDeps{store: storeFake, daemonMgr: daemonFake}

	r := chi.NewRouter()
	r.Post("/api/v1/workspaces/{workspaceID}/agents/{agentID}/sandbox/acquire", acquireSandbox(deps, runtimeFake, daemonFake))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/00000000-0000-0000-0000-000000000002/agents/00000000-0000-0000-0000-000000000009/sandbox/acquire", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202; got %d body=%s", rec.Code, rec.Body.String())
	}
	if err := waitFor(t, 2*time.Second, func() bool {
		return len(runtimeFake.calls()) >= 1
	}); err != nil {
		t.Fatalf("acquire should call SetAgentRuntime after Acquire: %v", err)
	}
	calls := runtimeFake.calls()
	if len(calls) != 1 {
		t.Fatalf("SetAgentRuntime should be called exactly once; got %d: %+v", len(calls), calls)
	}
	if calls[0].RuntimeID != "device-fresh" {
		t.Errorf("acquire should write the new device id; got %q", calls[0].RuntimeID)
	}
	if calls[0].AgentID != "00000000-0000-0000-0000-000000000009" {
		t.Errorf("SetAgentRuntime should target the agent from URL; got %q", calls[0].AgentID)
	}
	inputs := daemonFake.inputs()
	if len(inputs) != 1 || inputs[0].AgentConfig["sandbox_size"] != "xl" {
		t.Fatalf("manual acquire should pass agent config to Acquire; got %+v", inputs)
	}
}
