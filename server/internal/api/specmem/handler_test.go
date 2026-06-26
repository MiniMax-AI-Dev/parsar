package specmem

// Handler-layer unit tests covering pure helpers and validation paths
// that short-circuit BEFORE the service is called. End-to-end happy-path
// coverage with real DB rows belongs in a separate file gated on
// PARSAR_TEST_DATABASE_URL — this one stays sandbox-friendly.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/specmemory"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// ----- pure helpers ---------------------------------------------------------

func TestParseRuntimeLimit(t *testing.T) {
	cases := []struct {
		raw  string
		want int32
	}{
		{"", 0},
		{"abc", 0},
		{"0", 0},
		{"-5", 0},
		{"10", 10},
		{"5000", 5000},
		{"5001", 5000}, // clamped
		{"99999", 5000},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			r := makeQueryReq("k", tc.raw)
			got := parseRuntimeLimit(r, "k")
			if got != tc.want {
				t.Errorf("parseRuntimeLimit(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseLimit(t *testing.T) {
	cases := []struct {
		raw  string
		def  int
		max  int
		want int32
	}{
		{"", 50, 200, 50},     // absent → default
		{"abc", 50, 200, 50},  // garbage → default
		{"0", 50, 200, 50},    // zero → default (not "no override")
		{"-1", 50, 200, 50},   // negative → default
		{"100", 50, 200, 100}, // in range
		{"500", 50, 200, 200}, // clamped
		{"200", 50, 200, 200}, // edge: exactly max
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			r := makeQueryReq("limit", tc.raw)
			got := parseLimit(r, tc.def, tc.max)
			if got != tc.want {
				t.Errorf("parseLimit(%q, %d, %d) = %d, want %d", tc.raw, tc.def, tc.max, got, tc.want)
			}
		})
	}
}

func TestParseTags(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{"  ", nil},
		{",,,", nil},
		{"go", []string{"go"}},
		{"go,db", []string{"go", "db"}},
		{"go, db, http", []string{"go", "db", "http"}},
		{" go ,, db ", []string{"go", "db"}},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			r := makeQueryReq("tag", tc.raw)
			got := parseTags(r)
			if len(got) != len(tc.want) {
				t.Fatalf("parseTags(%q) = %v, want %v", tc.raw, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("parseTags(%q)[%d] = %q, want %q", tc.raw, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestDerefString(t *testing.T) {
	if got := derefString(nil); got != "" {
		t.Errorf("derefString(nil) = %q, want \"\"", got)
	}
	s := "hello"
	if got := derefString(&s); got != "hello" {
		t.Errorf("derefString(&\"hello\") = %q, want \"hello\"", got)
	}
	empty := ""
	if got := derefString(&empty); got != "" {
		t.Errorf("derefString(&\"\") = %q, want \"\"", got)
	}
}

// TestAgentActor: format matters for downstream tools that grep audit
// rows by connector — change the format and you silently break those
// queries.
func TestAgentActor(t *testing.T) {
	connector := "claude"
	projectAgent := "pa-123"
	cases := []struct {
		name string
		id   store.RuntimeIdentity
		want string
	}{
		{
			name: "connector+project_agent",
			id: store.RuntimeIdentity{
				RuntimeID:      "rt-1",
				ConnectorName:  &connector,
				ProjectAgentID: &projectAgent,
			},
			want: "claude:pa-123",
		},
		{
			name: "connector only",
			id: store.RuntimeIdentity{
				RuntimeID:     "rt-1",
				ConnectorName: &connector,
			},
			want: "claude:runtime:rt-1",
		},
		{
			name: "neither (workspace-only runtime)",
			id: store.RuntimeIdentity{
				RuntimeID: "rt-1",
			},
			want: "runtime:rt-1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			actor := agentActor(tc.id)
			if actor.Type != audit.ActorTypeAgent {
				t.Errorf("Type = %q, want %q", actor.Type, audit.ActorTypeAgent)
			}
			if actor.AgentActor != tc.want {
				t.Errorf("AgentActor = %q, want %q", actor.AgentActor, tc.want)
			}
		})
	}
}

func TestUserActor(t *testing.T) {
	actor := userActor("user-42")
	if actor.Type != audit.ActorTypeUser {
		t.Errorf("Type = %q, want %q", actor.Type, audit.ActorTypeUser)
	}
	if actor.UserID != "user-42" {
		t.Errorf("UserID = %q, want \"user-42\"", actor.UserID)
	}
}

// ----- runtime-tree auth/validation (no service mocking needed) -------------

// newRuntimeRouter mounts the runtime tree against a (possibly nil)
// Service. Tests exercising pre-service short-circuits can pass nil.
func newRuntimeRouter(t *testing.T, svc *specmemory.Service) chi.Router {
	t.Helper()
	r := chi.NewRouter()
	RegisterRuntimeRoutes(r, Deps{Service: svc})
	return r
}

// withIdentity stuffs a RuntimeIdentity into ctx like auth.RunnerCredential
// does in production.
func withIdentity(id store.RuntimeIdentity) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithRuntimeIdentity(r.Context(), id)))
		})
	}
}

// newHandlerForValidation returns a handler bound to emptyService.
// Suitable for tests exercising paths that short-circuit BEFORE the
// service is called.
func newHandlerForValidation(t *testing.T) *handler {
	t.Helper()
	return newHandler(Deps{Service: emptyService(t)})
}

// TestRuntimeSnapshotMissingIdentity: without the middleware the
// handler must not blindly call the service.
func TestRuntimeSnapshotMissingIdentity(t *testing.T) {
	h := newHandlerForValidation(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-runtime/injection/snapshot", nil)
	rr := httptest.NewRecorder()
	h.runtimeSnapshot(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "wiring_error") {
		t.Errorf("body = %q, want wiring_error code", rr.Body.String())
	}
}

// TestRuntimeSnapshotMissingOwner: identity present but no OwnerUserID
// → 500 identity_incomplete.
func TestRuntimeSnapshotMissingOwner(t *testing.T) {
	h := newHandlerForValidation(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-runtime/injection/snapshot", nil)
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
	}))
	rr := httptest.NewRecorder()
	h.runtimeSnapshot(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "identity_incomplete") {
		t.Errorf("body = %q, want identity_incomplete", rr.Body.String())
	}
}

// TestRuntimeIncrementalMissingSince: required cursor → 400.
func TestRuntimeIncrementalMissingSince(t *testing.T) {
	h := newHandlerForValidation(t)
	owner := "user-1"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-runtime/injection/incremental", nil)
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
		OwnerUserID: &owner,
	}))
	rr := httptest.NewRecorder()
	h.runtimeIncremental(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "missing_since") {
		t.Errorf("body = %q, want missing_since", rr.Body.String())
	}
}

// TestRuntimeIncrementalBadSince: malformed RFC3339 → 400 with the
// parse error inline so the CLI can surface it.
func TestRuntimeIncrementalBadSince(t *testing.T) {
	h := newHandlerForValidation(t)
	owner := "user-1"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-runtime/injection/incremental?since=yesterday", nil)
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
		OwnerUserID: &owner,
	}))
	rr := httptest.NewRecorder()
	h.runtimeIncremental(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "bad_since") {
		t.Errorf("body = %q, want bad_since", rr.Body.String())
	}
}

func TestRuntimeCreateMemoryBadScope(t *testing.T) {
	h := newHandlerForValidation(t)
	owner := "user-1"
	body := strings.NewReader(`{"scope":"global","memory_type":"user","body":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent-runtime/memories", body)
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
		OwnerUserID: &owner,
	}))
	rr := httptest.NewRecorder()
	h.runtimeCreateMemory(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "bad_scope") {
		t.Errorf("body = %q, want bad_scope", rr.Body.String())
	}
}

// TestRuntimeCreateMemoryBadType: bad memory_type → 400.
func TestRuntimeCreateMemoryBadType(t *testing.T) {
	h := newHandlerForValidation(t)
	owner := "user-1"
	body := strings.NewReader(`{"scope":"user","memory_type":"bogus","body":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent-runtime/memories", body)
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
		OwnerUserID: &owner,
	}))
	rr := httptest.NewRecorder()
	h.runtimeCreateMemory(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "bad_memory_type") {
		t.Errorf("body = %q, want bad_memory_type", rr.Body.String())
	}
}

// TestRuntimeCreateMemoryProjectScopeWithoutBinding: scope=project but
// runtime isn't bound to a project. The agent can't pick a project; it
// must come from runtime config.
func TestRuntimeCreateMemoryProjectScopeWithoutBinding(t *testing.T) {
	h := newHandlerForValidation(t)
	owner := "user-1"
	body := strings.NewReader(`{"scope":"project","memory_type":"project","body":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent-runtime/memories", body)
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
		OwnerUserID: &owner,
	}))
	rr := httptest.NewRecorder()
	h.runtimeCreateMemory(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "no_project_binding") {
		t.Errorf("body = %q, want no_project_binding", rr.Body.String())
	}
}

// TestRuntimeCreateMemoryBadJSON: catches a regression where
// DisallowUnknownFields gets removed and a client typo silently writes
// to the wrong column.
func TestRuntimeCreateMemoryBadJSON(t *testing.T) {
	h := newHandlerForValidation(t)
	owner := "user-1"
	body := strings.NewReader(`{"scope":"user","memory_type":"user","body":"x","junk":"field"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent-runtime/memories", body)
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
		OwnerUserID: &owner,
	}))
	rr := httptest.NewRecorder()
	h.runtimeCreateMemory(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (unknown field rejected)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "bad_json") {
		t.Errorf("body = %q, want bad_json", rr.Body.String())
	}
}

func TestRuntimeCreateFragmentMissingIdentity(t *testing.T) {
	h := newHandlerForValidation(t)
	body := strings.NewReader(`{"title":"x","body":"y"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent-runtime/spec/fragments", body)
	rr := httptest.NewRecorder()
	h.runtimeCreateFragment(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rr.Code)
	}
}

// ----- runtime list / update / delete validation paths ---------------------

// TestRuntimeListFragmentsMissingIdentity: list with no ctx identity → 500.
func TestRuntimeListFragmentsMissingIdentity(t *testing.T) {
	h := newHandlerForValidation(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-runtime/spec/fragments", nil)
	rr := httptest.NewRecorder()
	h.runtimeListFragments(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "wiring_error") {
		t.Errorf("body = %q, want wiring_error", rr.Body.String())
	}
}

// TestRuntimeListFragmentsBadSource: ?source=garbage → 400 before the
// store is touched.
func TestRuntimeListFragmentsBadSource(t *testing.T) {
	h := newHandlerForValidation(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-runtime/spec/fragments?source=bogus", nil)
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
	}))
	rr := httptest.NewRecorder()
	h.runtimeListFragments(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "bad_source") {
		t.Errorf("body = %q, want bad_source", rr.Body.String())
	}
}

// TestRuntimeUpdateFragmentMissingIdentity: PATCH without identity → 500.
func TestRuntimeUpdateFragmentMissingIdentity(t *testing.T) {
	h := newHandlerForValidation(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agent-runtime/spec/fragments/f1", strings.NewReader(`{}`))
	req = chiURLParam(req, "fragmentID", "f1")
	rr := httptest.NewRecorder()
	h.runtimeUpdateFragment(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rr.Code)
	}
}

// TestRuntimeUpdateFragmentNotFound: noopStore returns !found → 404.
func TestRuntimeUpdateFragmentNotFound(t *testing.T) {
	h := newHandlerForValidation(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agent-runtime/spec/fragments/missing", strings.NewReader(`{"title":"x"}`))
	req = chiURLParam(req, "fragmentID", "missing")
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
	}))
	rr := httptest.NewRecorder()
	h.runtimeUpdateFragment(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "not_found") {
		t.Errorf("body = %q, want not_found", rr.Body.String())
	}
}

// TestRuntimeUpdateFragmentCrossWorkspace: a fragment in another
// workspace must surface as 404, not 403 — cross-workspace IDs must
// not be enumerable.
func TestRuntimeUpdateFragmentCrossWorkspace(t *testing.T) {
	h := newHandlerWithStub(t, stubStore{
		fragment: store.SpecFragmentRead{
			ID:          "f-other",
			WorkspaceID: "other-ws",
			Title:       "neighbour",
			Body:        "leak me",
			Source:      "manual",
		},
	})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agent-runtime/spec/fragments/f-other", strings.NewReader(`{"title":"x"}`))
	req = chiURLParam(req, "fragmentID", "f-other")
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
	}))
	rr := httptest.NewRecorder()
	h.runtimeUpdateFragment(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404 (cross-workspace must be invisible)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "not_found") {
		t.Errorf("body = %q, want not_found", rr.Body.String())
	}
}

// TestRuntimeDeleteFragmentMissingIdentity: DELETE without identity → 500.
func TestRuntimeDeleteFragmentMissingIdentity(t *testing.T) {
	h := newHandlerForValidation(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/agent-runtime/spec/fragments/f1", nil)
	req = chiURLParam(req, "fragmentID", "f1")
	rr := httptest.NewRecorder()
	h.runtimeDeleteFragment(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rr.Code)
	}
}

// TestRuntimeDeleteFragmentNotFound: !found → 404.
func TestRuntimeDeleteFragmentNotFound(t *testing.T) {
	h := newHandlerForValidation(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/agent-runtime/spec/fragments/missing", nil)
	req = chiURLParam(req, "fragmentID", "missing")
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
	}))
	rr := httptest.NewRecorder()
	h.runtimeDeleteFragment(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rr.Code)
	}
}

// TestRuntimeDeleteFragmentCrossWorkspace: same guard as update.
func TestRuntimeDeleteFragmentCrossWorkspace(t *testing.T) {
	h := newHandlerWithStub(t, stubStore{
		fragment: store.SpecFragmentRead{
			ID:          "f-other",
			WorkspaceID: "other-ws",
			Title:       "neighbour",
			Body:        "leak me",
			Source:      "manual",
		},
	})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/agent-runtime/spec/fragments/f-other", nil)
	req = chiURLParam(req, "fragmentID", "f-other")
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
	}))
	rr := httptest.NewRecorder()
	h.runtimeDeleteFragment(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rr.Code)
	}
}

// TestRuntimeListMemoriesMissingIdentity: list without ctx → 500.
func TestRuntimeListMemoriesMissingIdentity(t *testing.T) {
	h := newHandlerForValidation(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-runtime/memories", nil)
	rr := httptest.NewRecorder()
	h.runtimeListMemories(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rr.Code)
	}
}

// TestRuntimeListMemoriesMissingOwner: workspace-only runtimes can
// read spec but not user-scope memory.
func TestRuntimeListMemoriesMissingOwner(t *testing.T) {
	h := newHandlerForValidation(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-runtime/memories?scope=user", nil)
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
	}))
	rr := httptest.NewRecorder()
	h.runtimeListMemories(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "identity_incomplete") {
		t.Errorf("body = %q, want identity_incomplete", rr.Body.String())
	}
}

// TestRuntimeListMemoriesMissingScope: no ?scope= → 400. Forces the
// CLI to be explicit so user / project lists can't mix.
func TestRuntimeListMemoriesMissingScope(t *testing.T) {
	h := newHandlerForValidation(t)
	owner := "user-1"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-runtime/memories", nil)
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
		OwnerUserID: &owner,
	}))
	rr := httptest.NewRecorder()
	h.runtimeListMemories(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "bad_scope") {
		t.Errorf("body = %q, want bad_scope", rr.Body.String())
	}
}

// TestRuntimeListMemoriesBadType: scope=user&memory_type=bogus → 400.
func TestRuntimeListMemoriesBadType(t *testing.T) {
	h := newHandlerForValidation(t)
	owner := "user-1"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-runtime/memories?scope=user&memory_type=bogus", nil)
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
		OwnerUserID: &owner,
	}))
	rr := httptest.NewRecorder()
	h.runtimeListMemories(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "bad_memory_type") {
		t.Errorf("body = %q, want bad_memory_type", rr.Body.String())
	}
}

// TestRuntimeListMemoriesProjectScopeWithoutBinding: scope=project
// without a project binding → 400 (don't leak other projects or return
// empty).
func TestRuntimeListMemoriesProjectScopeWithoutBinding(t *testing.T) {
	h := newHandlerForValidation(t)
	owner := "user-1"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agent-runtime/memories?scope=project", nil)
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
		OwnerUserID: &owner,
	}))
	rr := httptest.NewRecorder()
	h.runtimeListMemories(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "no_project_binding") {
		t.Errorf("body = %q, want no_project_binding", rr.Body.String())
	}
}

// TestRuntimeUpdateMemoryMissingIdentity: PATCH without ctx → 500.
func TestRuntimeUpdateMemoryMissingIdentity(t *testing.T) {
	h := newHandlerForValidation(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agent-runtime/memories/m1", strings.NewReader(`{}`))
	req = chiURLParam(req, "memoryID", "m1")
	rr := httptest.NewRecorder()
	h.runtimeUpdateMemory(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rr.Code)
	}
}

// TestRuntimeUpdateMemoryNotFound: noopStore !found → 404.
func TestRuntimeUpdateMemoryNotFound(t *testing.T) {
	h := newHandlerForValidation(t)
	owner := "user-1"
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agent-runtime/memories/missing", strings.NewReader(`{"body":"x"}`))
	req = chiURLParam(req, "memoryID", "missing")
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
		OwnerUserID: &owner,
	}))
	rr := httptest.NewRecorder()
	h.runtimeUpdateMemory(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rr.Code)
	}
}

// TestRuntimeUpdateMemoryCrossUser: catches the regression where the
// runtimeOwnsMemory check is dropped.
func TestRuntimeUpdateMemoryCrossUser(t *testing.T) {
	h := newHandlerWithStub(t, stubStore{
		memory: store.MemoryRead{
			ID:         "m-other",
			Scope:      "user",
			UserID:     "user-2",
			MemoryType: "user",
			Body:       "leak",
			Source:     "manual",
		},
	})
	owner := "user-1"
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agent-runtime/memories/m-other", strings.NewReader(`{"body":"x"}`))
	req = chiURLParam(req, "memoryID", "m-other")
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
		OwnerUserID: &owner,
	}))
	rr := httptest.NewRecorder()
	h.runtimeUpdateMemory(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404 (cross-user must be invisible)", rr.Code)
	}
}

// TestRuntimeUpdateMemoryCrossProject: same enumeration guard for
// project-scope.
func TestRuntimeUpdateMemoryCrossProject(t *testing.T) {
	h := newHandlerWithStub(t, stubStore{
		memory: store.MemoryRead{
			ID:         "m-other",
			Scope:      "project",
			UserID:     "user-1",
			ProjectID:  "proj-2",
			MemoryType: "project",
			Body:       "leak",
			Source:     "manual",
		},
	})
	owner, proj := "user-1", "proj-1"
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/agent-runtime/memories/m-other", strings.NewReader(`{"body":"x"}`))
	req = chiURLParam(req, "memoryID", "m-other")
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
		OwnerUserID: &owner,
		ProjectID:   &proj,
	}))
	rr := httptest.NewRecorder()
	h.runtimeUpdateMemory(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rr.Code)
	}
}

// TestRuntimeDeleteMemoryMissingIdentity: DELETE without ctx → 500.
func TestRuntimeDeleteMemoryMissingIdentity(t *testing.T) {
	h := newHandlerForValidation(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/agent-runtime/memories/m1", nil)
	req = chiURLParam(req, "memoryID", "m1")
	rr := httptest.NewRecorder()
	h.runtimeDeleteMemory(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", rr.Code)
	}
}

// TestRuntimeDeleteMemoryNotFound: noopStore !found → 404.
func TestRuntimeDeleteMemoryNotFound(t *testing.T) {
	h := newHandlerForValidation(t)
	owner := "user-1"
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/agent-runtime/memories/missing", nil)
	req = chiURLParam(req, "memoryID", "missing")
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
		OwnerUserID: &owner,
	}))
	rr := httptest.NewRecorder()
	h.runtimeDeleteMemory(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rr.Code)
	}
}

// TestRuntimeDeleteMemoryCrossUser: same guard as update.
func TestRuntimeDeleteMemoryCrossUser(t *testing.T) {
	h := newHandlerWithStub(t, stubStore{
		memory: store.MemoryRead{
			ID:         "m-other",
			Scope:      "user",
			UserID:     "user-2",
			MemoryType: "user",
			Body:       "leak",
			Source:     "manual",
		},
	})
	owner := "user-1"
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/agent-runtime/memories/m-other", nil)
	req = chiURLParam(req, "memoryID", "m-other")
	req = req.WithContext(auth.WithRuntimeIdentity(req.Context(), store.RuntimeIdentity{
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
		OwnerUserID: &owner,
	}))
	rr := httptest.NewRecorder()
	h.runtimeDeleteMemory(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rr.Code)
	}
}

// ----- admin-tree validation (no service mocking needed) -------------------

func TestAdminCreateMemoryBadScope(t *testing.T) {
	h := newHandlerForValidation(t)
	h.deps.Membership = fakeMembership{}
	body := strings.NewReader(`{"scope":"workspace","memory_type":"user","body":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/memories", body)
	rr := httptest.NewRecorder()
	h.createMemory(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "bad_scope") {
		t.Errorf("body = %q, want bad_scope", rr.Body.String())
	}
}

// TestAdminListMemoriesMissingScope: list with no scope → 400. Forces
// the client to be explicit so user-scope and project-scope memories
// can't leak via the wrong route.
func TestAdminListMemoriesMissingScope(t *testing.T) {
	h := newHandlerForValidation(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/memories", nil)
	rr := httptest.NewRecorder()
	h.listMemories(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "bad_scope") {
		t.Errorf("body = %q, want bad_scope", rr.Body.String())
	}
}

// TestAdminListMemoriesProjectScopeMissingID: scope=project without
// project_id → 400 before membership lookup.
func TestAdminListMemoriesProjectScopeMissingID(t *testing.T) {
	h := newHandlerForValidation(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/memories?scope=project", nil)
	req = req.WithContext(auth.WithUserID(req.Context(), "user-1"))
	rr := httptest.NewRecorder()
	h.listMemories(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "missing_project_id") {
		t.Errorf("body = %q, want missing_project_id", rr.Body.String())
	}
}

// TestAdminListMemoriesUserScopeUnauthenticated: catches a regression
// where requireSession is removed from the listMemories user branch.
func TestAdminListMemoriesUserScopeUnauthenticated(t *testing.T) {
	h := newHandlerForValidation(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/memories?scope=user", nil)
	rr := httptest.NewRecorder()
	h.listMemories(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rr.Code)
	}
}

// TestAdminImportSpecEmptyText: import with no text → 400.
func TestAdminImportSpecEmptyText(t *testing.T) {
	h := newHandlerForValidation(t)
	h.deps.Membership = fakeMembership{wsRole: "admin"}
	body := strings.NewReader(`{"text":"","confirm":false}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws-1/spec/import", body)
	req = chiURLParam(req, "workspaceID", "ws-1")
	req = req.WithContext(auth.WithUserID(req.Context(), "user-1"))
	rr := httptest.NewRecorder()
	h.importSpec(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "empty_text") {
		t.Errorf("body = %q, want empty_text", rr.Body.String())
	}
}

// TestAdminListFragmentsBadSource: bogus source filter → 400 before
// any store call.
func TestAdminListFragmentsBadSource(t *testing.T) {
	h := newHandlerForValidation(t)
	h.deps.Membership = fakeMembership{wsRole: "member"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/ws-1/spec/fragments?source=bogus", nil)
	req = chiURLParam(req, "workspaceID", "ws-1")
	req = req.WithContext(auth.WithUserID(req.Context(), "user-1"))
	rr := httptest.NewRecorder()
	h.listFragments(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "bad_source") {
		t.Errorf("body = %q, want bad_source", rr.Body.String())
	}
}

// TestAdminCreateFragmentForbiddenForNonMember: catches a regression
// where the workspace membership check is dropped from create.
func TestAdminCreateFragmentForbiddenForNonMember(t *testing.T) {
	h := newHandlerForValidation(t)
	h.deps.Membership = fakeMembership{wsErr: errors.New("not member")}
	body := strings.NewReader(`{"title":"x","body":"y"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws-1/spec/fragments", body)
	req = chiURLParam(req, "workspaceID", "ws-1")
	req = req.WithContext(auth.WithUserID(req.Context(), "user-1"))
	rr := httptest.NewRecorder()
	h.createFragment(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "not_member") {
		t.Errorf("body = %q, want not_member", rr.Body.String())
	}
}

// TestAdminCreateFragmentUnauthenticated: no session → 401, no
// membership lookup attempted.
func TestAdminCreateFragmentUnauthenticated(t *testing.T) {
	h := newHandlerForValidation(t)
	called := false
	h.deps.Membership = fakeMembership{onCall: func() { called = true }}
	body := strings.NewReader(`{"title":"x","body":"y"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/ws-1/spec/fragments", body)
	req = chiURLParam(req, "workspaceID", "ws-1")
	rr := httptest.NewRecorder()
	h.createFragment(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rr.Code)
	}
	if called {
		t.Error("membership lookup ran on unauthenticated request — should short-circuit")
	}
}

// ----- registration smoke tests ---------------------------------------------

// TestRegisterAdminRoutes seals the route table: the path strings are
// the API contract with the UI.
func TestRegisterAdminRoutes(t *testing.T) {
	r := chi.NewRouter()
	RegisterAdminRoutes(r, Deps{Service: emptyService(t), Membership: fakeMembership{}})
	// chi.Walk reports collection routes mounted via r.Get("/", ...)
	// inside r.Route(...) with a trailing slash; item routes keep the
	// literal path.
	want := map[string][]string{
		"/api/v1/workspaces/{workspaceID}/spec/fragments/":             {"GET", "POST"},
		"/api/v1/workspaces/{workspaceID}/spec/fragments/{fragmentID}": {"PATCH", "DELETE"},
		"/api/v1/workspaces/{workspaceID}/spec/import":                 {"POST"},
		"/api/v1/memories/":           {"GET", "POST"},
		"/api/v1/memories/{memoryID}": {"PATCH", "DELETE"},
	}
	walkExpectRoutes(t, r, want)
}

func TestRegisterRuntimeRoutes(t *testing.T) {
	r := chi.NewRouter()
	RegisterRuntimeRoutes(r, Deps{Service: emptyService(t)})
	want := map[string][]string{
		"/api/v1/agent-runtime/injection/snapshot":          {"GET"},
		"/api/v1/agent-runtime/injection/incremental":       {"GET"},
		"/api/v1/agent-runtime/spec/fragments/":             {"GET", "POST"},
		"/api/v1/agent-runtime/spec/fragments/{fragmentID}": {"PATCH", "DELETE"},
		"/api/v1/agent-runtime/memories/":                   {"GET", "POST"},
		"/api/v1/agent-runtime/memories/{memoryID}":         {"PATCH", "DELETE"},
	}
	walkExpectRoutes(t, r, want)
}

// TestRegisterAdminRoutesPanicsWithoutMembership confirms the
// "Membership is required" guard fires.
func TestRegisterAdminRoutesPanicsWithoutMembership(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when Membership is nil")
		}
	}()
	RegisterAdminRoutes(chi.NewRouter(), Deps{Service: emptyService(t)})
}

// TestNewHandlerPanicsOnNilService: catch the wiring bug at startup.
func TestNewHandlerPanicsOnNilService(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when Service is nil")
		}
	}()
	newHandler(Deps{})
}

// ----- test helpers ---------------------------------------------------------

// chiURLParam shims a URL param onto a request as chi's router would,
// for tests that call handlers directly rather than via a chi mux.
func chiURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// makeQueryReq sets a single query value via r.URL.RawQuery. Avoids
// httptest.NewRequest's URL parser blowing up on whitespace / non-encoded
// characters in test fixtures.
func makeQueryReq(key, value string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	q := r.URL.Query()
	q.Set(key, value)
	r.URL.RawQuery = q.Encode()
	return r
}

// fakeMembership is the minimal MembershipStore for deterministic
// pass/fail in tests.
type fakeMembership struct {
	wsRole      string
	wsErr       error
	projWsID    string // workspaceID returned by GetProjectWorkspace
	projWsErr   error  // error returned by GetProjectWorkspace
	onCall      func()
}

func (f fakeMembership) GetWorkspaceMemberRole(ctx context.Context, workspaceID, userID string) (string, error) {
	if f.onCall != nil {
		f.onCall()
	}
	return f.wsRole, f.wsErr
}

func (f fakeMembership) GetProjectWorkspace(ctx context.Context, projectID string) (string, error) {
	if f.projWsErr != nil {
		return "", f.projWsErr
	}
	if f.projWsID != "" {
		return f.projWsID, nil
	}
	return "ws-test", nil
}

// emptyService returns a real *specmemory.Service backed by a no-op
// Store. Used by route-walk tests that don't fire any handler.
func emptyService(t *testing.T) *specmemory.Service {
	t.Helper()
	return specmemory.NewService(noopStore{}, nil, specmemory.Options{})
}

// noopStore satisfies specmemory.Store with zero-value returns. Only
// for tests that don't actually invoke read/write paths.
type noopStore struct{}

func (noopStore) GetSpecFragment(ctx context.Context, id string) (store.SpecFragmentRead, bool, error) {
	return store.SpecFragmentRead{}, false, nil
}
func (noopStore) InsertSpecFragment(ctx context.Context, in store.InsertSpecFragmentInput) (store.SpecFragmentRead, error) {
	return store.SpecFragmentRead{}, nil
}
func (noopStore) UpdateSpecFragment(ctx context.Context, in store.UpdateSpecFragmentInput) (store.SpecFragmentRead, bool, error) {
	return store.SpecFragmentRead{}, false, nil
}
func (noopStore) SoftDeleteSpecFragment(ctx context.Context, id string, now time.Time) error {
	return nil
}
func (noopStore) GetMemory(ctx context.Context, id string) (store.MemoryRead, bool, error) {
	return store.MemoryRead{}, false, nil
}
func (noopStore) InsertMemory(ctx context.Context, in store.InsertMemoryInput) (store.MemoryRead, error) {
	return store.MemoryRead{}, nil
}
func (noopStore) UpdateMemory(ctx context.Context, in store.UpdateMemoryInput) (store.MemoryRead, bool, error) {
	return store.MemoryRead{}, false, nil
}
func (noopStore) SoftDeleteMemory(ctx context.Context, id string, now time.Time) error { return nil }
func (noopStore) ListWorkspaceSpecFragments(ctx context.Context, in store.ListWorkspaceSpecFragmentsInput) ([]store.SpecFragmentRead, error) {
	return nil, nil
}
func (noopStore) ListUserMemories(ctx context.Context, in store.ListUserMemoriesInput) ([]store.MemoryRead, error) {
	return nil, nil
}
func (noopStore) ListProjectMemories(ctx context.Context, in store.ListProjectMemoriesInput) ([]store.MemoryRead, error) {
	return nil, nil
}
func (noopStore) ListUserMemoriesSince(ctx context.Context, in store.ListUserMemoriesSinceInput) ([]store.MemoryRead, error) {
	return nil, nil
}
func (noopStore) ListProjectMemoriesSince(ctx context.Context, in store.ListProjectMemoriesSinceInput) ([]store.MemoryRead, error) {
	return nil, nil
}

// stubStore extends noopStore with canned Get returns. Suitable for
// runtime-tree ownership-check tests where the pre-fetch must succeed
// with a row owned by someone else so the 404 branch fires.
// Source / Scope / MemoryType must be valid enum values (the service's
// *FromStoreRow conversion rejects unknowns).
type stubStore struct {
	noopStore
	fragment store.SpecFragmentRead
	memory   store.MemoryRead
}

func (s stubStore) GetSpecFragment(ctx context.Context, id string) (store.SpecFragmentRead, bool, error) {
	if s.fragment.ID != "" {
		return s.fragment, true, nil
	}
	return store.SpecFragmentRead{}, false, nil
}

func (s stubStore) GetMemory(ctx context.Context, id string) (store.MemoryRead, bool, error) {
	if s.memory.ID != "" {
		return s.memory, true, nil
	}
	return store.MemoryRead{}, false, nil
}

// newHandlerWithStub builds a handler whose Service is backed by the
// given stubStore.
func newHandlerWithStub(t *testing.T, s stubStore) *handler {
	t.Helper()
	return newHandler(Deps{Service: specmemory.NewService(s, nil, specmemory.Options{})})
}

// walkExpectRoutes confirms every expected (path, methods) pair
// appears. Inclusion check, not strict equality.
func walkExpectRoutes(t *testing.T, r chi.Router, want map[string][]string) {
	t.Helper()
	got := make(map[string]map[string]struct{})
	err := chi.Walk(r, func(method, route string, handler http.Handler, mws ...func(http.Handler) http.Handler) error {
		if _, ok := got[route]; !ok {
			got[route] = make(map[string]struct{})
		}
		got[route][method] = struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	for route, methods := range want {
		methodsOnRoute, ok := got[route]
		if !ok {
			t.Errorf("expected route %s not registered", route)
			continue
		}
		for _, m := range methods {
			if _, ok := methodsOnRoute[m]; !ok {
				t.Errorf("expected %s %s, got methods %v", m, route, keys(methodsOnRoute))
			}
		}
	}
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func decodeBody(t *testing.T, rr *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.NewDecoder(rr.Body).Decode(dst); err != nil {
		t.Fatalf("decode body %q: %v", rr.Body.String(), err)
	}
}
