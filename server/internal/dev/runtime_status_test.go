package dev

// Runtime status handler tests. Runtime mode is per-Agent
// (`agents.config.runtime`).
//
//  1. Pure-function tests for computeSandboxReachable.
//  2. Router-level tests for the JSON response shape + status codes.
//  3. RBAC test for the workspace-member gate.

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

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

// fakeSandboxProber satisfies SandboxLivenessProber. The default
// zero-value succeeds (ping returns nil); set errReturn to force an
// error path; set block=true to force a context deadline (drives
// the 1s timeout test).
type fakeSandboxProber struct {
	mu        sync.Mutex
	calls     int
	errReturn error
	block     bool
}

func (f *fakeSandboxProber) Ping(ctx context.Context) error {
	f.mu.Lock()
	f.calls++
	err := f.errReturn
	block := f.block
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	return err
}

func (f *fakeSandboxProber) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// newRuntimeStatusTestRouter mounts the handler standalone (no
// gateWorkspaceMember wrapper) so the unit tests below don't have
// to stand up the full auth + role-store chain. The RBAC behaviour
// is exercised separately by TestRuntimeStatusRBAC at the bottom.
func newRuntimeStatusTestRouter(deps RuntimeStatusDeps) chi.Router {
	r := chi.NewRouter()
	r.Get("/api/v1/workspaces/{workspaceID}/runtime/status", runtimeStatus(deps))
	return r
}

type fakeRuntimeSettingsStore struct {
	settings store.WorkspaceRuntimeSettingsRead
	runtimes []store.RuntimeRead
	err      error
}

func (f fakeRuntimeSettingsStore) GetWorkspaceRuntimeSettings(ctx context.Context, workspaceID string) (store.WorkspaceRuntimeSettingsRead, error) {
	if f.err != nil {
		return store.WorkspaceRuntimeSettingsRead{}, f.err
	}
	settings := f.settings
	settings.WorkspaceID = workspaceID
	return settings, nil
}

func (f fakeRuntimeSettingsStore) ListRuntimes(context.Context, string, string, int32) ([]store.RuntimeRead, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.runtimes, nil
}

// runtimeStatusRequest is a tiny helper for the URL the handler is
// mounted on. Body is always empty.
func runtimeStatusRequest() *http.Request {
	return httptest.NewRequest(http.MethodGet,
		"/api/v1/workspaces/00000000-0000-0000-0000-000000000002/runtime/status", nil)
}

// ---------------- pure computeSandboxReachable ----------------

// TestComputeSandboxReachableNilProberFalse — operator set sandbox
// runtime intent but resolveSandboxRuntime returned nil prober (api
// key missing). Banner must render unreachable so operator notices.
func TestComputeSandboxReachableNilProberFalse(t *testing.T) {
	if computeSandboxReachable(context.Background(), nil, time.Second) {
		t.Fatalf("nil prober must report reachable=false")
	}
}

// TestComputeSandboxReachableHealthy — happy path: Ping returns nil
// within timeout.
func TestComputeSandboxReachableHealthy(t *testing.T) {
	prober := &fakeSandboxProber{}
	if !computeSandboxReachable(context.Background(), prober, time.Second) {
		t.Fatalf("healthy prober must report reachable=true")
	}
	if prober.callCount() != 1 {
		t.Errorf("expected exactly one Ping call, got %d", prober.callCount())
	}
}

// TestComputeSandboxReachableProberError — provider returns error
// (provider shut down, e2b 5xx, etc). Banner must show unreachable.
func TestComputeSandboxReachableProberError(t *testing.T) {
	prober := &fakeSandboxProber{errReturn: errors.New("e2b 503")}
	if computeSandboxReachable(context.Background(), prober, time.Second) {
		t.Fatalf("failing prober must report reachable=false")
	}
}

// TestComputeSandboxReachableProberTimeout — provider stalls past
// budget. Banner must show unreachable instead of hanging. 5ms
// budget keeps the test fast.
func TestComputeSandboxReachableProberTimeout(t *testing.T) {
	prober := &fakeSandboxProber{block: true}
	start := time.Now()
	if computeSandboxReachable(context.Background(), prober, 5*time.Millisecond) {
		t.Fatalf("stalled prober must report reachable=false")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("timeout enforcement too loose: elapsed=%s (expected ~5ms)", elapsed)
	}
}

func TestNormalizeRuntimeProfile(t *testing.T) {
	cases := map[string]string{
		"":          "oss",
		"oss":       "oss",
		" managed ": "managed",
		"SELFHOST":  "selfhost",
		"internal":  "oss",
	}
	for input, want := range cases {
		if got := normalizeRuntimeProfile(input); got != want {
			t.Errorf("normalizeRuntimeProfile(%q) = %q, want %q", input, got, want)
		}
	}
}

// ---------------- handler / router shape ----------------

// TestRuntimeStatusNoCredential — fresh workspace, no E2B credential
// recorded. Response: has_credential=false, available=false (prober
// is not even invoked when no credential), sandbox_agent_count=0.
func TestRuntimeStatusNoCredential(t *testing.T) {
	prober := &fakeSandboxProber{}
	r := newRuntimeStatusTestRouter(RuntimeStatusDeps{
		SettingsStore: fakeRuntimeSettingsStore{settings: store.WorkspaceRuntimeSettingsRead{}},
		SandboxProber: prober,
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, runtimeStatusRequest())

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := resp["has_credential"]; got != false {
		t.Errorf("has_credential: want false, got %v", got)
	}
	if got := resp["available"]; got != false {
		t.Errorf("available without credential must be false, got %v", got)
	}
	if got := resp["credential_masked"]; got != nil {
		t.Errorf("credential_masked must be null, got %v", got)
	}
	if got := resp["sandbox_agent_count"]; got != float64(0) {
		t.Errorf("sandbox_agent_count: want 0, got %v", got)
	}
	if got := resp["profile"]; got != "oss" {
		t.Errorf("profile: want oss, got %v", got)
	}
	if prober.callCount() != 0 {
		t.Errorf("prober must NOT be invoked when has_credential=false (saves a probe), got %d calls", prober.callCount())
	}
}

func TestRuntimeStatusIncludesProviders(t *testing.T) {
	r := newRuntimeStatusTestRouter(RuntimeStatusDeps{
		SettingsStore: fakeRuntimeSettingsStore{
			settings: store.WorkspaceRuntimeSettingsRead{},
			runtimes: []store.RuntimeRead{{
				Type:     store.RuntimeTypeAgentDaemon,
				Provider: store.RuntimeProviderAgentDaemon,
				Liveness: store.RuntimeLivenessOnline,
			}},
		},
		Providers: []RuntimeProviderStatus{
			{
				ID:          "manual_daemon",
				Label:       "Manual daemon",
				Kind:        "manual",
				Configured:  false,
				Available:   false,
				Recommended: true,
				Missing:     []string{"paired_runtime"},
			},
			{
				ID:        "e2b_compatible",
				Label:     "E2B compatible",
				Kind:      "managed",
				Requires:  []string{"AGENT_DAEMON_SANDBOX_TEMPLATE", "workspace_runtime_credential"},
				Missing:   []string{"workspace_runtime_credential"},
				Available: false,
			},
		},
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, runtimeStatusRequest())

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	providers, ok := resp["providers"].([]any)
	if !ok || len(providers) != 2 {
		t.Fatalf("providers: got %#v", resp["providers"])
	}
	manual, _ := providers[0].(map[string]any)
	if got := manual["id"]; got != "manual_daemon" {
		t.Errorf("manual provider id: got %v", got)
	}
	if got := manual["available"]; got != true {
		t.Errorf("manual provider available: got %v", got)
	}
	if got := manual["configured"]; got != true {
		t.Errorf("manual provider configured: got %v", got)
	}
	e2b, _ := providers[1].(map[string]any)
	if got := e2b["id"]; got != "e2b_compatible" {
		t.Errorf("e2b provider id: got %v", got)
	}
	if got := e2b["available"]; got != false {
		t.Errorf("e2b provider available without credential: got %v", got)
	}
}

func TestRuntimeStatusManagedNoCredentialReachable(t *testing.T) {
	prober := &fakeSandboxProber{}
	r := newRuntimeStatusTestRouter(RuntimeStatusDeps{
		Profile:       "managed",
		SandboxProber: prober,
		SettingsStore: fakeRuntimeSettingsStore{settings: store.WorkspaceRuntimeSettingsRead{
			SandboxAgentCount: 2,
		}},
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, runtimeStatusRequest())

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := resp["profile"]; got != "managed" {
		t.Errorf("profile: want managed, got %v", got)
	}
	if got := resp["has_credential"]; got != false {
		t.Errorf("managed profile should not require workspace credential, got %v", got)
	}
	if got := resp["available"]; got != true {
		t.Errorf("managed profile should use prober even without credential, got %v", got)
	}
	if got := resp["sandbox_agent_count"]; got != float64(2) {
		t.Errorf("sandbox_agent_count: want 2, got %v", got)
	}
	if prober.callCount() != 1 {
		t.Errorf("managed profile should probe platform sandbox provider once, got %d", prober.callCount())
	}
}

func TestRuntimeStatusManagedNoCredentialUnreachable(t *testing.T) {
	prober := &fakeSandboxProber{errReturn: errors.New("sandbox provider disabled")}
	r := newRuntimeStatusTestRouter(RuntimeStatusDeps{
		Profile:       "managed",
		SandboxProber: prober,
		SettingsStore: fakeRuntimeSettingsStore{settings: store.WorkspaceRuntimeSettingsRead{}},
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, runtimeStatusRequest())

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := resp["profile"]; got != "managed" {
		t.Errorf("profile: want managed, got %v", got)
	}
	if got := resp["has_credential"]; got != false {
		t.Errorf("managed profile should not require workspace credential, got %v", got)
	}
	if got := resp["available"]; got != false {
		t.Errorf("failing managed prober should report unavailable, got %v", got)
	}
	if prober.callCount() != 1 {
		t.Errorf("managed profile should probe once, got %d", prober.callCount())
	}
}

// TestRuntimeStatusHasCredentialReachable — workspace has a
// registered credential, prober is healthy. Response:
// has_credential=true, available=true, masked surfaces, prober
// invoked once.
func TestRuntimeStatusHasCredentialReachable(t *testing.T) {
	prober := &fakeSandboxProber{}
	r := newRuntimeStatusTestRouter(RuntimeStatusDeps{
		SandboxProber: prober,
		SettingsStore: fakeRuntimeSettingsStore{settings: store.WorkspaceRuntimeSettingsRead{
			RuntimeCredentialSecretID: "00000000-0000-0000-0000-000000000123",
			RuntimeCredentialMasked:   "e2b_•••wxyz",
			SandboxAgentCount:         3,
		}},
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, runtimeStatusRequest())

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := resp["has_credential"]; got != true {
		t.Errorf("has_credential: want true, got %v", got)
	}
	if got := resp["available"]; got != true {
		t.Errorf("available: want true, got %v", got)
	}
	if got := resp["credential_masked"]; got != "e2b_•••wxyz" {
		t.Errorf("credential_masked: want 'e2b_•••wxyz', got %v", got)
	}
	if got := resp["sandbox_agent_count"]; got != float64(3) {
		t.Errorf("sandbox_agent_count: want 3, got %v", got)
	}
	if prober.callCount() != 1 {
		t.Errorf("expected one prober call, got %d", prober.callCount())
	}
}

// TestRuntimeStatusHasCredentialUnreachableProberError — credential
// registered but prober errors. Response must report
// available=false; status code stays 200 because the banner endpoint
// surfaces runner health as data, not as a 5xx.
func TestRuntimeStatusHasCredentialUnreachableProberError(t *testing.T) {
	prober := &fakeSandboxProber{errReturn: errors.New("e2b 503")}
	r := newRuntimeStatusTestRouter(RuntimeStatusDeps{
		SandboxProber: prober,
		SettingsStore: fakeRuntimeSettingsStore{settings: store.WorkspaceRuntimeSettingsRead{
			RuntimeCredentialSecretID: "00000000-0000-0000-0000-000000000123",
			RuntimeCredentialMasked:   "e2b_•••wxyz",
		}},
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, runtimeStatusRequest())

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := resp["available"]; got != false {
		t.Errorf("available: want false, got %v", got)
	}
	if got := resp["has_credential"]; got != true {
		t.Errorf("has_credential: want true, got %v", got)
	}
}

// TestRuntimeStatusHasCredentialNoProber — operator registered a
// credential but resolveSandboxRuntime returned nil (somehow no
// prober wired). Response must still be 200, available=false,
// has_credential=true.
func TestRuntimeStatusHasCredentialNoProber(t *testing.T) {
	r := newRuntimeStatusTestRouter(RuntimeStatusDeps{
		SettingsStore: fakeRuntimeSettingsStore{settings: store.WorkspaceRuntimeSettingsRead{
			RuntimeCredentialSecretID: "00000000-0000-0000-0000-000000000123",
		}},
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, runtimeStatusRequest())

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := resp["available"]; got != false {
		t.Errorf("available: want false, got %v", got)
	}
}

// TestRuntimeStatusUnwired — router built without WithRuntimeStatus
// returns 503. By design — the StatusBanner can render an explicit
// "not configured" rather than guess at a default.
func TestRuntimeStatusUnwired(t *testing.T) {
	// Pass zero-value deps to simulate the no-op wiring (no
	// SettingsStore).
	r := newRuntimeStatusTestRouter(RuntimeStatusDeps{})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, runtimeStatusRequest())

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not wired") {
		t.Errorf("expected 'not wired' in body, got %s", rec.Body.String())
	}
}

// TestRuntimeStatusPingTimeoutDefault — when deps.PingTimeout is 0,
// the handler uses defaultRuntimePingTimeout. Wire a credential +
// blocking prober and assert the handler returns within ~1.2s
// (default 1s + slack) rather than hanging.
func TestRuntimeStatusPingTimeoutDefault(t *testing.T) {
	prober := &fakeSandboxProber{block: true}
	r := newRuntimeStatusTestRouter(RuntimeStatusDeps{
		SandboxProber: prober,
		SettingsStore: fakeRuntimeSettingsStore{settings: store.WorkspaceRuntimeSettingsRead{
			RuntimeCredentialSecretID: "00000000-0000-0000-0000-000000000123",
		}},
		// PingTimeout intentionally left 0 → defaults to 1s.
	})

	done := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(done)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, runtimeStatusRequest())
	}()
	select {
	case <-done:
		elapsed := time.Since(start)
		// Allow 1s budget + generous CI slack. The test's main
		// point is "handler doesn't hang forever"; tightening
		// the upper bound would just flake on busy runners.
		if elapsed > 5*time.Second {
			t.Errorf("handler exceeded default timeout budget: elapsed=%s", elapsed)
		}
		if elapsed < 800*time.Millisecond {
			t.Errorf("handler returned too fast — default timeout not honoured: elapsed=%s", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("handler hung past timeout budget")
	}
}

// TestRuntimeStatusRBACOutsiderBlocked exercises the full router
// (with gateWorkspaceMember wrapping the handler) — outsider must
// get the RBAC 404 "not a member", NOT the handler's 503 or 200.
// Confirms the registration in routes.go wraps the route correctly.
func TestRuntimeStatusRBACOutsiderBlocked(t *testing.T) {
	const memberUser = "00000000-0000-0000-0000-0000000000aa"
	const outsiderUser = "00000000-0000-0000-0000-0000000000bb"

	r := registerRoutesWithRBACStore(newRoleStubStore(map[string]string{
		memberUser: "member",
		// outsiderUser absent → ErrNotMember → 404
	}), true)

	req := newRequestWithDevUser(http.MethodGet,
		"/api/v1/workspaces/00000000-0000-0000-0000-000000000002/runtime/status",
		"", outsiderUser)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("outsider must get 404, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "not a member") {
		t.Fatalf("outsider 404 must come from RBAC layer, got: %s", res.Body.String())
	}
}

// TestRuntimeStatusRBACMemberPassesGate is the converse — a member
// of the workspace passes the gate. Since the test router doesn't
// wire WithRuntimeStatus, the handler itself returns 503 — that 503
// proves "RBAC layer let me through".
func TestRuntimeStatusRBACMemberPassesGate(t *testing.T) {
	const memberUser = "00000000-0000-0000-0000-0000000000aa"

	r := registerRoutesWithRBACStore(newRoleStubStore(map[string]string{
		memberUser: "member",
	}), true)

	req := newRequestWithDevUser(http.MethodGet,
		"/api/v1/workspaces/00000000-0000-0000-0000-000000000002/runtime/status",
		"", memberUser)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code == http.StatusNotFound && strings.Contains(res.Body.String(), "not a member") {
		t.Fatalf("member should have passed RBAC gate, got 404 not-a-member: %s", res.Body.String())
	}
	// 503 is the expected default for a router that didn't wire
	// WithRuntimeStatus — proves the gate let us through to the
	// handler.
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (handler unwired) once RBAC passes, got %d body=%s",
			res.Code, res.Body.String())
	}
}
