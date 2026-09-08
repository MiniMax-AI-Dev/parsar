package dev

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	authinvite "github.com/MiniMax-AI-Dev/parsar/server/internal/auth/invite"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type createInvitationRequest struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
	Role  string `json:"role"`
}

type createInvitationResponse struct {
	InvitationID string `json:"invitation_id"`
	InviteLink   string `json:"invite_link"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	ExpiresAt    string `json:"expires_at"`
}

// createInvitation saves a workspace invitation and an optional new-account name.
//
// @Summary Create a workspace invitation
// @Description Stores the optional name for new-account creation. Existing account names are preserved.
// @Tags invitations
// @Accept json
// @Produce json
// @Param workspaceID path string true "Workspace UUID"
// @Param body body createInvitationRequest true "Invitation details"
// @Success 201 {object} createInvitationResponse
// @Failure 400 {object} map[string]string "Invalid invitation details"
// @Failure 401 {object} map[string]string "Authentication required"
// @Failure 403 {object} map[string]string "Invitation not allowed"
// @Failure 409 {object} map[string]string "Invitation already pending"
// @Failure 500 {object} map[string]string "Invitation creation failed"
// @Router /api/v1/workspaces/{workspaceID}/invitations [post]
func createInvitation(runtimeStore RuntimeStore, cfg *routerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMemberNotViewer(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req createInvitationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		req.Email = strings.TrimSpace(req.Email)
		req.Role = strings.TrimSpace(req.Role)
		if req.Email == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email is required"})
			return
		}
		if !store.IsValidMemberRole(req.Role) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be one of owner|admin|member|viewer"})
			return
		}

		now := time.Now().UTC()
		expiresAt := now.Add(authinvite.MaxLifetime)
		invID := uuid.New().String()
		token := invID

		callerID, callerRole, err := invitationCallerRole(r, runtimeStore, workspaceID)
		if err != nil {
			writeRBACError(w, err)
			return
		}
		if callerRole == "member" && req.Role != "member" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "members can only invite new members with the member role"})
			return
		}

		if err := runtimeStore.CreateInvitation(r.Context(), store.CreateInvitationInput{
			ID:          invID,
			TokenHash:   authinvite.TokenHash(token),
			WorkspaceID: workspaceID,
			Email:       req.Email,
			Name:        req.Name,
			Role:        req.Role,
			InvitedBy:   callerID,
			ExpiresAt:   expiresAt,
			Now:         now,
		}); err != nil {
			if strings.Contains(err.Error(), "uk_workspace_invitations_pending_email") {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "an invitation is already pending for this email"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create invitation"})
			return
		}

		link := cfg.publicURL + "/invite/" + token
		writeJSON(w, http.StatusCreated, createInvitationResponse{
			InvitationID: invID,
			InviteLink:   link,
			Email:        store.NormalizeEmail(req.Email),
			Role:         req.Role,
			ExpiresAt:    expiresAt.Format(time.RFC3339),
		})
	}
}
