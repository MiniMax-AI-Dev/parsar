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
)

type invitationRouteStore struct {
	stubRuntimeStore
	created    store.CreateInvitationInput
	invitation store.InvitationRead
	accepted   store.AcceptInvitationInput
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

func (s *invitationRouteStore) invitationToken() string {
	return "legacy.payload-and-signature-token"
}

func invitationTestRouter(store RuntimeStore) http.Handler {
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, store, WithInvite(newStubSessions(), false, "https://parsar.example/"))
	return r
}

func TestCreateInvitationUsesShortTokenAndConfiguredPublicURL(t *testing.T) {
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
	if len(token) != 32 {
		t.Fatalf("token length = %d, want 32", len(token))
	}
	if !bytes.Equal(st.created.TokenHash, authinvite.TokenHash(token)) {
		t.Fatal("stored token hash does not match generated token")
	}
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
