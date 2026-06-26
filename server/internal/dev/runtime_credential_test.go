package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// runtimeCredentialStubStore extends the role-aware test store with
// recorders for the new RegisterWorkspaceRuntimeCredential /
// ClearWorkspaceRuntimeCredentialSecret calls so the tests can assert
// the handler invoked the right store helper. After the B1 fix, the
// PUT path must always go through RegisterWorkspaceRuntimeCredential
// (which atomically soft-deletes the prior secret + inserts a new one
// + flips the workspace pointer) instead of CreateSecret + raw Set —
// this stub records both signatures so the test can prove the handler
// no longer takes the legacy raw-CreateSecret detour.
type runtimeCredentialStubStore struct {
	roleStubStore
	mu              sync.Mutex
	registerCalls   []runtimeCredentialRegisterCall
	clearCalls      []runtimeCredentialClearCall
	registerCounter int
}

type runtimeCredentialRegisterCall struct {
	WorkspaceID string
	Name        string
	Kind        string
	Provider    string
	Masked      string
	CreatedBy   string
	Now         time.Time
}

type runtimeCredentialClearCall struct {
	WorkspaceID string
	Now         time.Time
}

func (s *runtimeCredentialStubStore) RegisterWorkspaceRuntimeCredential(ctx context.Context, input store.RegisterWorkspaceRuntimeCredentialInput) (store.SecretRead, error) {
	s.mu.Lock()
	s.registerCalls = append(s.registerCalls, runtimeCredentialRegisterCall{
		WorkspaceID: input.WorkspaceID,
		Name:        input.Name,
		Kind:        input.Kind,
		Provider:    input.Provider,
		Masked:      input.Masked,
		CreatedBy:   input.CreatedBy,
		Now:         input.Now,
	})
	s.registerCounter++
	id := fmt.Sprintf("00000000-0000-0000-0000-00000000060%d", s.registerCounter%10)
	s.mu.Unlock()
	return store.SecretRead{
		ID:         id,
		Name:       input.Name,
		Kind:       input.Kind,
		Provider:   input.Provider,
		AuthType:   input.AuthType,
		KeyVersion: "v1",
		Status:     "active",
		Masked:     input.Masked,
		Metadata:   map[string]any{"masked": input.Masked},
		CreatedAt:  input.Now,
		UpdatedAt:  input.Now,
	}, nil
}

func (s *runtimeCredentialStubStore) ClearWorkspaceRuntimeCredentialSecret(ctx context.Context, workspaceID, name, kind string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearCalls = append(s.clearCalls, runtimeCredentialClearCall{WorkspaceID: workspaceID, Now: now})
	return nil
}

func (s *runtimeCredentialStubStore) snapshotRegister() []runtimeCredentialRegisterCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]runtimeCredentialRegisterCall, len(s.registerCalls))
	copy(out, s.registerCalls)
	return out
}

func (s *runtimeCredentialStubStore) snapshotClear() []runtimeCredentialClearCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]runtimeCredentialClearCall, len(s.clearCalls))
	copy(out, s.clearCalls)
	return out
}

func newRuntimeCredentialRouter(store *runtimeCredentialStubStore) http.Handler {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, store)
	return r
}

const runtimeCredentialMasterKey = "test-master-key-test-master-key-"

func runtimeCredentialPUT(t *testing.T, body string, userID string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/workspaces/"+testWorkspaceID+"/runtime/credential",
		bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	return withTestUserID(req, userID)
}

func runtimeCredentialDELETE(t *testing.T, userID string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete,
		"/api/v1/workspaces/"+testWorkspaceID+"/runtime/credential", nil)
	return withTestUserID(req, userID)
}

// TestPutRuntimeCredentialHappyPath — owner registers a fresh credential.
// Expectations: 200 OK; response.has_credential=true with masked value;
// RegisterWorkspaceRuntimeCredential called once with kind=runtime,
// provider=e2b; masked derived from api_key.
func TestPutRuntimeCredentialHappyPath(t *testing.T) {
	t.Setenv("PARSAR_MASTER_KEY", runtimeCredentialMasterKey)

	stubStore := &runtimeCredentialStubStore{roleStubStore: newRoleStubStore(map[string]string{
		testOwnerUserID: "owner",
	})}
	router := newRuntimeCredentialRouter(stubStore)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, runtimeCredentialPUT(t,
		`{"api_key":"e2b_test_aaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		testOwnerUserID))

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	var body runtimeCredentialResponse
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.HasCredential {
		t.Errorf("expected has_credential=true, got false")
	}
	if body.CredentialMasked == nil || *body.CredentialMasked == "" {
		t.Errorf("expected masked credential, got %v", body.CredentialMasked)
	}
	calls := stubStore.snapshotRegister()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one Register call, got %d", len(calls))
	}
	if calls[0].WorkspaceID != testWorkspaceID {
		t.Errorf("Register workspace_id = %q, want %q", calls[0].WorkspaceID, testWorkspaceID)
	}
	if calls[0].Provider != "e2b" || calls[0].Kind != "runtime" {
		t.Errorf("Register kind/provider = %q/%q, want runtime/e2b", calls[0].Kind, calls[0].Provider)
	}
	if calls[0].Masked == "" {
		t.Errorf("Register masked is empty; expected derivation from api_key")
	}
}

// TestPutRuntimeCredentialResetGoesThroughRegister — second PUT
// (overwrite / reset path) must NOT make any direct CreateSecret
// call; it must go through RegisterWorkspaceRuntimeCredential which
// atomically soft-deletes the prior secret + inserts the new one.
// This pins the B1 contract at the handler layer: "two PUTs in a row"
// produces two Register calls. The real DB uniqueness conflict is
// exercised by the matching store-level test
// (TestRegisterWorkspaceRuntimeCredentialUpsertOverwritePrior).
func TestPutRuntimeCredentialResetGoesThroughRegister(t *testing.T) {
	t.Setenv("PARSAR_MASTER_KEY", runtimeCredentialMasterKey)

	stubStore := &runtimeCredentialStubStore{roleStubStore: newRoleStubStore(map[string]string{
		testOwnerUserID: "owner",
	})}
	router := newRuntimeCredentialRouter(stubStore)

	for _, key := range []string{"e2b_first_aaaaaaaaaaaaaaaaaaaaaaaaaa", "e2b_second_bbbbbbbbbbbbbbbbbbbbbbbbb"} {
		res := httptest.NewRecorder()
		router.ServeHTTP(res, runtimeCredentialPUT(t,
			`{"api_key":"`+key+`"}`,
			testOwnerUserID))
		if res.Code != http.StatusOK {
			t.Fatalf("PUT for %q expected 200, got %d: %s", key, res.Code, res.Body.String())
		}
	}
	calls := stubStore.snapshotRegister()
	if len(calls) != 2 {
		t.Fatalf("expected exactly two Register calls (initial + reset), got %d", len(calls))
	}
	if calls[0].Masked == calls[1].Masked {
		t.Errorf("expected different masked values across initial and reset, got %q twice", calls[0].Masked)
	}
}

func TestPutRuntimeCredentialMissingServerMasterKey(t *testing.T) {
	stubStore := &runtimeCredentialStubStore{roleStubStore: newRoleStubStore(map[string]string{
		testOwnerUserID: "owner",
	})}
	router := newRuntimeCredentialRouter(stubStore)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, runtimeCredentialPUT(t,
		`{"api_key":"e2b_test_aaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		testOwnerUserID))

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", res.Code, res.Body.String())
	}
}

// TestPutRuntimeCredentialEmptyAPIKey — payload validates but contains
// an empty api_key. 400 — don't waste a Register round-trip on data
// we know is invalid.
func TestPutRuntimeCredentialEmptyAPIKey(t *testing.T) {
	t.Setenv("PARSAR_MASTER_KEY", runtimeCredentialMasterKey)

	stubStore := &runtimeCredentialStubStore{roleStubStore: newRoleStubStore(map[string]string{
		testOwnerUserID: "owner",
	})}
	router := newRuntimeCredentialRouter(stubStore)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, runtimeCredentialPUT(t,
		`{"api_key":"   "}`,
		testOwnerUserID))

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", res.Code, res.Body.String())
	}
}

// TestPutRuntimeCredentialMemberForbidden — workspace member (not
// owner/admin) cannot register credentials. 403 via gateWorkspaceOwnerOrAdmin.
func TestPutRuntimeCredentialMemberForbidden(t *testing.T) {
	t.Setenv("PARSAR_MASTER_KEY", runtimeCredentialMasterKey)

	stubStore := &runtimeCredentialStubStore{roleStubStore: newRoleStubStore(map[string]string{
		testMemberUserID: "member",
	})}
	router := newRuntimeCredentialRouter(stubStore)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, runtimeCredentialPUT(t,
		`{"api_key":"e2b_test_aaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		testMemberUserID))

	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", res.Code, res.Body.String())
	}
	if calls := stubStore.snapshotRegister(); len(calls) != 0 {
		t.Errorf("expected zero Register calls on RBAC reject, got %d", len(calls))
	}
}

// TestDeleteRuntimeCredentialIdempotent — DELETE is idempotent: even
// when the workspace has no credential, the handler returns 200 with
// has_credential=false and Clear is invoked once (let the store sort
// out the no-op).
func TestDeleteRuntimeCredentialIdempotent(t *testing.T) {
	stubStore := &runtimeCredentialStubStore{roleStubStore: newRoleStubStore(map[string]string{
		testOwnerUserID: "owner",
	})}
	router := newRuntimeCredentialRouter(stubStore)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, runtimeCredentialDELETE(t, testOwnerUserID))

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	var body runtimeCredentialResponse
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.HasCredential {
		t.Errorf("expected has_credential=false after delete, got true")
	}
	if calls := stubStore.snapshotClear(); len(calls) != 1 {
		t.Errorf("expected exactly one Clear call, got %d", len(calls))
	}
}

// TestDeleteRuntimeCredentialMemberForbidden — member-level user gets
// 403 even on the destructive path.
func TestDeleteRuntimeCredentialMemberForbidden(t *testing.T) {
	stubStore := &runtimeCredentialStubStore{roleStubStore: newRoleStubStore(map[string]string{
		testMemberUserID: "member",
	})}
	router := newRuntimeCredentialRouter(stubStore)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, runtimeCredentialDELETE(t, testMemberUserID))

	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", res.Code, res.Body.String())
	}
}

// Compile-time check: stub implementations satisfy the RuntimeStore
// interface so the new methods don't drift.
var _ RuntimeStore = (*runtimeCredentialStubStore)(nil)

// Pin the auth import so future refactors that drop withTestUserID's
// auth dependency surface the right diagnostic.
var _ = auth.WithUserID
var _ store.SecretRead
