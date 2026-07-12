package dev

// Shared test fixtures + helpers used across multiple dev_test files.
//
// These IDs and the withTestUserID shim used to live in
// runtime_try_connection_test.go (now retired). They are kept here
// because runtime_credential_test, sandbox_connectivity_test, and
// other RBAC-gated handler tests reuse the same workspace + user
// fixture pairs against the dev router.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
)

func requireStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("expected %d, got %d: %s", want, response.Code, response.Body.String())
	}
}

// withTestUserID returns a copy of req with the dev fixture user
// injected into the context — same shim the existing routes_test.go
// uses for member-level handlers. gateWorkspaceOwnerOrAdmin pulls
// this back out via auth.UserIDFromContext.
func withTestUserID(req *http.Request, userID string) *http.Request {
	return req.WithContext(auth.WithUserID(req.Context(), userID))
}

const testWorkspaceID = "00000000-0000-0000-0000-000000000002"

const (
	testOwnerUserID  = "00000000-0000-0000-0000-000000000aaa"
	testAdminUserID  = "00000000-0000-0000-0000-000000000bbb"
	testMemberUserID = "00000000-0000-0000-0000-000000000ccc"
	testOutsiderID   = "00000000-0000-0000-0000-000000000ddd"
)

// ownerRoleMap is the canonical role assignment used by RBAC-gated
// handler tests — owner gets through, admin gets through, member
// gets 403. Pass to newRoleStubStore when wiring the test router.
func ownerRoleMap() map[string]string {
	return map[string]string{
		testOwnerUserID:  "owner",
		testAdminUserID:  "admin",
		testMemberUserID: "member",
	}
}
