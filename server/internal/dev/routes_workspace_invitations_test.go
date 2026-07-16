package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	authinvite "github.com/MiniMax-AI-Dev/parsar/server/internal/auth/invite"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type invitationRouteStore struct {
	stubRuntimeStore
	created           store.CreateInvitationInput
	invitation        store.InvitationRead
	accepted          store.AcceptInvitationInput
	updatedInviteID   string
	updatedInviteRole string
	callerRole        string
	pending           []store.PendingInvitationRead
	pendingByInviter  []store.PendingInvitationRead
	listedInvitedBy   string
	revokedInviteID   string
	revokedInvitedBy  string
	revokeOwnRows     int64
}

func (s *invitationRouteStore) CreateInvitation(_ context.Context, input store.CreateInvitationInput) error {
	s.created = input
	return nil
}

func (s *invitationRouteStore) GetInvitationByTokenHash(_ context.Context, tokenHash []byte) (store.InvitationRead, error) {
	if !bytes.Equal(tokenHash, authinvite.TokenHash(s.invitationToken())) {
		return store.InvitationRead{}, store.ErrInvitationInvalid
	}
	return s.invitation, nil
}

func (s *invitationRouteStore) AcceptInvitation(_ context.Context, input store.AcceptInvitationInput) (store.AddWorkspaceMemberResult, error) {
	s.accepted = input
	return store.AddWorkspaceMemberResult{
		Member: store.WorkspaceMemberRead{
			WorkspaceID: input.WorkspaceID,
			UserID:      "00000000-0000-0000-0000-000000000003",
			UserEmail:   input.Email,
		},
	}, nil
}

func (s *invitationRouteStore) UpdateInvitationRole(_ context.Context, _, invitationID, role string) (int64, error) {
	s.updatedInviteID = invitationID
	s.updatedInviteRole = role
	return 1, nil
}

func (s *invitationRouteStore) GetWorkspaceMemberRole(_ context.Context, _, _ string) (string, error) {
	if s.callerRole != "" {
		return s.callerRole, nil
	}
	return "owner", nil
}

func (s *invitationRouteStore) ListPendingInvitations(_ context.Context, _ string) ([]store.PendingInvitationRead, error) {
	return s.pending, nil
}

func (s *invitationRouteStore) ListPendingInvitationsByInviter(_ context.Context, _, invitedBy string) ([]store.PendingInvitationRead, error) {
	s.listedInvitedBy = invitedBy
	return s.pendingByInviter, nil
}

func (s *invitationRouteStore) RevokeOwnInvitation(_ context.Context, _, invitationID, invitedBy string) (int64, error) {
	s.revokedInviteID = invitationID
	s.revokedInvitedBy = invitedBy
	return s.revokeOwnRows, nil
}

func (s *invitationRouteStore) invitationToken() string {
	return "legacy.payload-and-signature-token"
}

func invitationTestRouter(store RuntimeStore) http.Handler {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, store, WithInvite(newStubSessions(), false, "https://parsar.example/"))
	return r
}

func TestCreateInvitationUsesStableUUIDTokenAndConfiguredPublicURL(t *testing.T) {
	st := &invitationRouteStore{}
	r := invitationTestRouter(st)
	req := withTestUser(httptest.NewRequest(
		http.MethodPost,
		"/api/v1/workspaces/"+testWorkspaceID+"/invitations",
		strings.NewReader(`{"email":"new@example.com","role":"member"}`),
	))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	requireStatus(t, res, http.StatusCreated)

	var body createInvitationResponse
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	const prefix = "https://parsar.example/invite/"
	if !strings.HasPrefix(body.InviteLink, prefix) {
		t.Fatalf("invite_link = %q, want prefix %q", body.InviteLink, prefix)
	}
	token := strings.TrimPrefix(body.InviteLink, prefix)
	if _, err := uuid.Parse(token); err != nil {
		t.Fatalf("token = %q, want UUID: %v", token, err)
	}
	if token != body.InvitationID {
		t.Fatalf("token = %q, invitation_id = %q; want stable matching values", token, body.InvitationID)
	}
	if !bytes.Equal(st.created.TokenHash, authinvite.TokenHash(token)) {
		t.Fatal("stored token hash does not match generated token")
	}
}

func TestMemberCanInviteMember(t *testing.T) {
	st := &invitationRouteStore{callerRole: "member"}
	r := invitationTestRouter(st)
	req := withTestUser(httptest.NewRequest(
		http.MethodPost,
		"/api/v1/workspaces/"+testWorkspaceID+"/invitations",
		strings.NewReader(`{"email":"new@example.com","role":"member"}`),
	))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	requireStatus(t, res, http.StatusCreated)
}

func TestMemberCannotChooseInvitationRole(t *testing.T) {
	st := &invitationRouteStore{callerRole: "member"}
	r := invitationTestRouter(st)
	req := withTestUser(httptest.NewRequest(
		http.MethodPost,
		"/api/v1/workspaces/"+testWorkspaceID+"/invitations",
		strings.NewReader(`{"email":"new@example.com","role":"admin"}`),
	))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	requireStatus(t, res, http.StatusForbidden)
}

func TestViewerCannotCreateInvitation(t *testing.T) {
	st := &invitationRouteStore{callerRole: "viewer"}
	r := invitationTestRouter(st)
	req := withTestUser(httptest.NewRequest(
		http.MethodPost,
		"/api/v1/workspaces/"+testWorkspaceID+"/invitations",
		strings.NewReader(`{"email":"new@example.com","role":"member"}`),
	))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	requireStatus(t, res, http.StatusForbidden)
}

func TestListInvitationsRebuildsStableLinksOnly(t *testing.T) {
	const stableID = "00000000-0000-0000-0000-000000000091"
	const legacyID = "00000000-0000-0000-0000-000000000092"
	st := &invitationRouteStore{pending: []store.PendingInvitationRead{
		{ID: stableID, TokenHash: authinvite.TokenHash(stableID), Email: "stable@example.com", Role: "member"},
		{ID: legacyID, TokenHash: authinvite.TokenHash("legacy-random-token"), Email: "legacy@example.com", Role: "member"},
	}}
	r := invitationTestRouter(st)
	req := withTestUser(httptest.NewRequest(
		http.MethodGet,
		"/api/v1/workspaces/"+testWorkspaceID+"/invitations",
		nil,
	))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	requireStatus(t, res, http.StatusOK)

	var body []pendingInvitationResponse
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 2 {
		t.Fatalf("response count = %d, want 2", len(body))
	}
	if body[0].InviteLink != "https://parsar.example/invite/"+stableID {
		t.Fatalf("stable invite_link = %q", body[0].InviteLink)
	}
	if body[1].InviteLink != "" {
		t.Fatalf("legacy invite_link = %q, want omitted", body[1].InviteLink)
	}
}

func TestMemberListsOnlyOwnPendingInvitations(t *testing.T) {
	const invitationID = "00000000-0000-0000-0000-000000000093"
	st := &invitationRouteStore{
		callerRole: "member",
		pendingByInviter: []store.PendingInvitationRead{
			{ID: invitationID, TokenHash: authinvite.TokenHash(invitationID), Email: "mine@example.com", Role: "member"},
		},
	}
	r := invitationTestRouter(st)
	req := withTestUser(httptest.NewRequest(
		http.MethodGet,
		"/api/v1/workspaces/"+testWorkspaceID+"/invitations",
		nil,
	))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	requireStatus(t, res, http.StatusOK)

	if st.listedInvitedBy != store.DefaultDevFixtureIDs().UserID {
		t.Fatalf("listed invited_by = %q, want caller id", st.listedInvitedBy)
	}
	var body []pendingInvitationResponse
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || body[0].Email != "mine@example.com" {
		t.Fatalf("response = %#v, want member's own invitation", body)
	}
}

func TestMemberCanRevokeOwnInvitation(t *testing.T) {
	const invitationID = "00000000-0000-0000-0000-000000000094"
	st := &invitationRouteStore{callerRole: "member", revokeOwnRows: 1}
	r := invitationTestRouter(st)
	req := withTestUser(httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/workspaces/"+testWorkspaceID+"/invitations/"+invitationID,
		nil,
	))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	requireStatus(t, res, http.StatusNoContent)

	if st.revokedInviteID != invitationID || st.revokedInvitedBy != store.DefaultDevFixtureIDs().UserID {
		t.Fatalf("revoked = (%q, %q), want own invitation", st.revokedInviteID, st.revokedInvitedBy)
	}
}

func TestViewerCannotListInvitations(t *testing.T) {
	st := &invitationRouteStore{callerRole: "viewer"}
	r := invitationTestRouter(st)
	req := withTestUser(httptest.NewRequest(
		http.MethodGet,
		"/api/v1/workspaces/"+testWorkspaceID+"/invitations",
		nil,
	))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	requireStatus(t, res, http.StatusForbidden)
}

func TestUpdateInvitationRole(t *testing.T) {
	st := &invitationRouteStore{}
	r := invitationTestRouter(st)
	const invitationID = "00000000-0000-0000-0000-000000000099"
	req := withTestUser(httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/workspaces/"+testWorkspaceID+"/invitations/"+invitationID,
		strings.NewReader(`{"role":"viewer"}`),
	))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	requireStatus(t, res, http.StatusNoContent)

	if st.updatedInviteID != invitationID || st.updatedInviteRole != "viewer" {
		t.Fatalf("updated invitation = (%q, %q), want (%q, viewer)", st.updatedInviteID, st.updatedInviteRole, invitationID)
	}
}

func TestUpdateInvitationRoleRejectsInvalidRole(t *testing.T) {
	st := &invitationRouteStore{}
	r := invitationTestRouter(st)
	req := withTestUser(httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/workspaces/"+testWorkspaceID+"/invitations/00000000-0000-0000-0000-000000000099",
		strings.NewReader(`{"role":"superadmin"}`),
	))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	requireStatus(t, res, http.StatusBadRequest)
}

func TestInviteInfoAcceptsExistingLongTokenAndUsesDatabaseFields(t *testing.T) {
	st := &invitationRouteStore{
		invitation: store.InvitationRead{
			WorkspaceID:   testWorkspaceID,
			WorkspaceName: "Demo Workspace",
			Email:         "invitee@example.com",
			Role:          "viewer",
			ExpiresAt:     time.Now().UTC().Add(time.Hour),
		},
	}
	r := invitationTestRouter(st)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/invite/info",
		strings.NewReader(`{"token":"`+st.invitationToken()+`"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	requireStatus(t, res, http.StatusOK)

	var body inviteInfoResponse
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Email != st.invitation.Email || body.Role != st.invitation.Role {
		t.Fatalf("response = %#v, want database email and role", body)
	}
}

func TestAcceptInvitationUsesDatabaseFields(t *testing.T) {
	st := &invitationRouteStore{
		invitation: store.InvitationRead{
			WorkspaceID: testWorkspaceID,
			Email:       "invitee@example.com",
			Role:        "member",
			ExpiresAt:   time.Now().UTC().Add(time.Hour),
		},
	}
	r := invitationTestRouter(st)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/invite/accept",
		strings.NewReader(`{"token":"`+st.invitationToken()+`","password":"correct-horse-battery-staple"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	requireStatus(t, res, http.StatusOK)

	if st.accepted.WorkspaceID != st.invitation.WorkspaceID ||
		st.accepted.Email != st.invitation.Email ||
		st.accepted.Role != st.invitation.Role {
		t.Fatalf("accept input = %#v, want database invitation fields", st.accepted)
	}
	if st.accepted.PasswordHash == "" {
		t.Fatal("password hash was not passed to the store")
	}
	if cookie := res.Result().Cookies(); len(cookie) == 0 || cookie[0].Name != auth.CookieName {
		t.Fatal("accepted invitation did not issue a session cookie")
	}
}
