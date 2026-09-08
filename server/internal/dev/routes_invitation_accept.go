package dev

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	authinvite "github.com/MiniMax-AI-Dev/parsar/server/internal/auth/invite"
	authpassword "github.com/MiniMax-AI-Dev/parsar/server/internal/auth/password"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type acceptInvitationRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type acceptInvitationResponse struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	WorkspaceID string `json:"workspace_id"`
}

// acceptInvitation consumes an invitation for a new or authenticated existing account.
//
// @Summary Accept a workspace invitation
// @Description New accounts supply a password. Existing accounts must be signed in as the invited user; their password is never changed.
// @Tags invitations
// @Accept json
// @Produce json
// @Param body body acceptInvitationRequest true "Invitation and optional new-account password"
// @Success 200 {object} acceptInvitationResponse
// @Failure 400 {object} map[string]string "Invalid request or new-account password"
// @Failure 401 {object} map[string]string "Sign in as the invited account"
// @Failure 410 {object} map[string]string "Invitation invalid, expired, or already used"
// @Failure 500 {object} map[string]string "Acceptance or session creation failed"
// @Router /api/v1/invite/accept [post]
func acceptInvitation(runtimeStore RuntimeStore, cfg *routerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req acceptInvitationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		req.Token = strings.TrimSpace(req.Token)
		if req.Token == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token is required"})
			return
		}

		inv, err := runtimeStore.GetInvitationByTokenHash(r.Context(), authinvite.TokenHash(req.Token))
		if err != nil {
			writeJSON(w, http.StatusGone, map[string]string{"error": "invitation is invalid, expired, or already used"})
			return
		}
		now := time.Now().UTC()
		if inv.AcceptedAt != nil || inv.RevokedAt != nil || !inv.ExpiresAt.After(now) {
			writeJSON(w, http.StatusGone, map[string]string{"error": "invitation is invalid, expired, or already used"})
			return
		}

		var hash string
		if req.Password != "" {
			if err := authpassword.Validate(req.Password); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}

			hash, err = authpassword.Hash(req.Password)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
				return
			}
		}

		result, err := runtimeStore.AcceptInvitation(r.Context(), store.AcceptInvitationInput{
			TokenHash:    authinvite.TokenHash(req.Token),
			Email:        inv.Email,
			Role:         inv.Role,
			WorkspaceID:  inv.WorkspaceID,
			PasswordHash: hash,
			ActorUserID:  auth.UserIDFromContext(r.Context()),
			Now:          now,
		})
		if err != nil {
			writeReadError(w, err, "failed to accept invitation")
			return
		}

		sid, err := cfg.inviteSessions.Create(r.Context(), auth.CreateSessionInput{
			UserID:    result.Member.UserID,
			UserAgent: r.UserAgent(),
			IP:        r.RemoteAddr,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create session"})
			return
		}
		auth.IssueCookie(w, sid, 0, cfg.inviteCookieSecure)

		writeJSON(w, http.StatusOK, acceptInvitationResponse{
			UserID:      result.Member.UserID,
			Email:       result.Member.UserEmail,
			WorkspaceID: result.Member.WorkspaceID,
		})
	}
}
