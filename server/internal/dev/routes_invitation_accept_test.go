package dev

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type invitationAcceptanceStore struct {
	invitationRouteStore
	err error
}

func (s *invitationAcceptanceStore) AcceptInvitation(ctx context.Context, input store.AcceptInvitationInput) (store.AddWorkspaceMemberResult, error) {
	if s.err != nil {
		return store.AddWorkspaceMemberResult{}, s.err
	}
	return s.invitationRouteStore.AcceptInvitation(ctx, input)
}

func TestAcceptInvitationAuthenticatedAccountNeedsNoPassword(t *testing.T) {
	const actorID = "00000000-0000-0000-0000-000000000001"
	st := &invitationAcceptanceStore{}
	st.invitation = store.InvitationRead{WorkspaceID: testWorkspaceID, Email: "invitee@example.com", Role: "member", ExpiresAt: time.Now().Add(time.Hour)}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/invite/accept", strings.NewReader(`{"token":"`+st.invitationToken()+`"}`))
	req = req.WithContext(auth.WithUserID(req.Context(), actorID))
	res := httptest.NewRecorder()
	invitationTestRouter(st).ServeHTTP(res, req)
	requireStatus(t, res, http.StatusOK)
	if st.accepted.ActorUserID != actorID || st.accepted.PasswordHash != "" {
		t.Fatalf("unexpected acceptance input: %+v", st.accepted)
	}
}

func TestAcceptInvitationRejectsWithoutIssuingSession(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{store.ErrInvitationSignInRequired, http.StatusUnauthorized},
		{store.ErrInvitationInvalid, http.StatusGone},
		{store.ErrInvalidInput, http.StatusBadRequest},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			st := &invitationAcceptanceStore{err: tc.err}
			st.invitation = store.InvitationRead{WorkspaceID: testWorkspaceID, Email: "invitee@example.com", Role: "member", ExpiresAt: time.Now().Add(time.Hour)}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/invite/accept", strings.NewReader(`{"token":"`+st.invitationToken()+`"}`))
			res := httptest.NewRecorder()
			invitationTestRouter(st).ServeHTTP(res, req)
			requireStatus(t, res, tc.status)
			if len(res.Result().Cookies()) != 0 {
				t.Fatal("rejected invitation issued a session")
			}
		})
	}
}
