package dev

import (
	"encoding/json"
	"errors"
	"io"

	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	authinvite "github.com/MiniMax-AI-Dev/parsar/server/internal/auth/invite"
	authpassword "github.com/MiniMax-AI-Dev/parsar/server/internal/auth/password"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// listWorkspaceMembers lists members of a workspace.
//
//	@Summary		List workspace members
//	@Description	Returns members of the workspace. Caller must be a workspace member.
//	@Tags			workspace-members
//	@ID				listDevWorkspaceMembers
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Success		200 {object} map[string]interface{} "Member list"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not a workspace member"
//	@Router			/api/v1/workspaces/{workspaceID}/members [get]
func listWorkspaceMembers(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		members, err := runtimeStore.ListWorkspaceMembers(r.Context(), workspaceID, parseLimit(r, 100))
		if err != nil {
			writeReadError(w, err, "failed to list workspace members")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"workspace_id": workspaceID,
			"members":      members,
		})
	}
}

type addWorkspaceMemberRequest struct {
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

type addWorkspaceMemberResponse struct {
	Member      store.WorkspaceMemberRead `json:"member"`
	UserCreated bool                      `json:"user_created"`
}

// addWorkspaceMember adds a user to a workspace.
//
//	@Summary		Add a workspace member
//	@Description	Adds a user to the workspace with the given role. Owner/admin only.
//	@Tags			workspace-members
//	@ID				addDevWorkspaceMember
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string						true	"Workspace UUID"
//	@Param			body		body	addWorkspaceMemberRequest	true	"Member payload"
//	@Success		201 {object} map[string]interface{} "Added member"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		409 {object} map[string]string "User is already a member"
//	@Router			/api/v1/workspaces/{workspaceID}/members [post]
func addWorkspaceMember(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req addWorkspaceMemberRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		req.Email = strings.TrimSpace(req.Email)
		req.Name = strings.TrimSpace(req.Name)
		req.Role = strings.TrimSpace(req.Role)
		if req.Email == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email is required"})
			return
		}
		if !store.IsValidMemberRole(req.Role) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be one of owner|admin|member|viewer"})
			return
		}

		result, err := runtimeStore.AddWorkspaceMember(r.Context(), store.AddWorkspaceMemberInput{
			WorkspaceID: workspaceID,
			Email:       req.Email,
			Name:        req.Name,
			Role:        req.Role,
			Now:         time.Now().UTC(),
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to add workspace member"})
			return
		}

		writeJSON(w, http.StatusCreated, addWorkspaceMemberResponse{
			Member:      result.Member,
			UserCreated: result.UserCreated,
		})
	}
}

// ── Invitation handlers ─────────────────────────────────────────

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

func createInvitation(runtimeStore RuntimeStore, cfg *routerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
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

		token, err := cfg.inviteSigner.Sign(workspaceID, req.Email, req.Role, authinvite.MaxLifetime)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to sign invite token"})
			return
		}

		now := time.Now().UTC()
		expiresAt := now.Add(authinvite.MaxLifetime)
		invID := uuid.New().String()

		callerID := auth.UserIDFromContext(r.Context())
		if callerID == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing caller identity"})
			return
		}

		if err := runtimeStore.CreateInvitation(r.Context(), store.CreateInvitationInput{
			ID:          invID,
			TokenHash:   authinvite.TokenHash(token),
			WorkspaceID: workspaceID,
			Email:       req.Email,
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

func listInvitations(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}

		rows, err := runtimeStore.ListPendingInvitations(r.Context(), workspaceID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list invitations"})
			return
		}
		writeJSON(w, http.StatusOK, rows)
	}
}

func revokeInvitation(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		invitationID := strings.TrimSpace(chi.URLParam(r, "invitationID"))
		if !isUUID(workspaceID) || !isUUID(invitationID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id and invitation_id must be valid uuids"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}

		rows, err := runtimeStore.RevokeInvitation(r.Context(), workspaceID, invitationID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to revoke invitation"})
			return
		}
		if rows == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "invitation not found or already consumed"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type inviteInfoRequest struct {
	Token string `json:"token"`
}

type inviteInfoResponse struct {
	WorkspaceName string `json:"workspace_name"`
	Email         string `json:"email"`
	Role          string `json:"role"`
}

func getInviteInfo(runtimeStore RuntimeStore, cfg *routerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req inviteInfoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		token := strings.TrimSpace(req.Token)
		if token == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token is required"})
			return
		}

		claims, err := cfg.inviteSigner.Verify(token)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid or expired token"})
			return
		}

		inv, err := runtimeStore.GetInvitationByTokenHash(r.Context(), authinvite.TokenHash(token))
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "invitation not found"})
			return
		}
		if inv.AcceptedAt != nil || inv.RevokedAt != nil || inv.ExpiresAt.Before(time.Now()) {
			writeJSON(w, http.StatusGone, map[string]string{"error": "invitation has already been used or revoked"})
			return
		}

		writeJSON(w, http.StatusOK, inviteInfoResponse{
			WorkspaceName: inv.WorkspaceName,
			Email:         claims.Email,
			Role:          claims.Role,
		})
	}
}

type acceptInvitationRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

type acceptInvitationResponse struct {
	UserID      string `json:"user_id"`
	Email       string `json:"email"`
	WorkspaceID string `json:"workspace_id"`
}

func acceptInvitation(runtimeStore RuntimeStore, cfg *routerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req acceptInvitationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		req.Token = strings.TrimSpace(req.Token)
		if req.Token == "" || req.Password == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token and password are required"})
			return
		}

		claims, err := cfg.inviteSigner.Verify(req.Token)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid or expired token"})
			return
		}

		if err := authpassword.Validate(req.Password); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		hash, err := authpassword.Hash(req.Password)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
			return
		}

		now := time.Now().UTC()
		result, err := runtimeStore.AcceptInvitation(r.Context(), store.AcceptInvitationInput{
			TokenHash:    authinvite.TokenHash(req.Token),
			Email:        claims.Email,
			Role:         claims.Role,
			WorkspaceID:  claims.WorkspaceID,
			PasswordHash: hash,
			Now:          now,
		})
		if err != nil {
			if errors.Is(err, store.ErrInvitationInvalid) {
				writeJSON(w, http.StatusGone, map[string]string{"error": "invitation is invalid, expired, or already used"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to accept invitation"})
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

type updateWorkspaceMemberRoleRequest struct {
	Role string `json:"role"`
}

// updateWorkspaceMemberRole changes a member's role.
//
//	@Summary		Update a workspace member's role
//	@Description	Changes a member's role within the workspace. Owner/admin only.
//	@Tags			workspace-members
//	@ID				updateDevWorkspaceMemberRole
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string							true	"Workspace UUID"
//	@Param			userID		path	string							true	"User UUID"
//	@Param			body		body	updateWorkspaceMemberRoleRequest	true	"Role update payload"
//	@Success		200 {object} map[string]interface{} "Updated member"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/members/{userID} [patch]
func updateWorkspaceMemberRole(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		userID := strings.TrimSpace(chi.URLParam(r, "userID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if !isUUID(userID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req updateWorkspaceMemberRoleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		req.Role = strings.TrimSpace(req.Role)
		if !store.IsValidMemberRole(req.Role) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role must be one of owner|admin|member|viewer"})
			return
		}
		// Prevent changing the owner's role — ownership is transferred, not edited.
		targetRole, err := runtimeStore.GetWorkspaceMemberRole(r.Context(), workspaceID, userID)
		if err != nil {
			if errors.Is(err, store.ErrNotMember) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace member not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up member"})
			return
		}
		if targetRole == "owner" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot change the owner's role; use ownership transfer instead"})
			return
		}
		if req.Role == "owner" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot promote to owner; use ownership transfer instead"})
			return
		}
		member, err := runtimeStore.UpdateWorkspaceMemberRole(r.Context(), workspaceID, userID, req.Role, time.Now().UTC())
		if err != nil {
			if errors.Is(err, store.ErrUnknownWorkspaceMember) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace member not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update workspace member role"})
			return
		}
		writeJSON(w, http.StatusOK, member)
	}
}

func removeWorkspaceMember(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		userID := strings.TrimSpace(chi.URLParam(r, "userID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if !isUUID(userID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		// Prevent removing the workspace owner.
		targetRole, err := runtimeStore.GetWorkspaceMemberRole(r.Context(), workspaceID, userID)
		if err != nil {
			if errors.Is(err, store.ErrNotMember) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace member not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up member"})
			return
		}
		if targetRole == "owner" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot remove the workspace owner"})
			return
		}
		result, err := runtimeStore.RemoveWorkspaceMember(r.Context(), workspaceID, userID, time.Now().UTC())
		if err != nil {
			if errors.Is(err, store.ErrUnknownWorkspaceMember) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace member not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove workspace member"})
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// ============================================================
// Workspace self-service join request handlers
//
//   POST /api/v1/workspaces/{wid}/join-requests              submit request
//   GET  /api/v1/workspaces/{wid}/join-requests              owner/admin list
//   POST /api/v1/workspaces/{wid}/join-requests/{rid}/approve owner/admin approve
//   POST /api/v1/workspaces/{wid}/join-requests/{rid}/reject  owner/admin reject
//
// The WHERE status='pending' guard is atomic at the SQL layer; two admins racing
// will get ErrJoinRequestAlreadyHandled, which converts to a 409.
// ============================================================

type createJoinRequestRequest struct {
	Reason string `json:"reason,omitempty"`
}

func createJoinRequest(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		userID, ok := devActorID(w, r)
		if !ok {
			return
		}
		var body createJoinRequestRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		// Optional reason; optional field, length soft limit — prevents abuse without enforcing schema
		if len(body.Reason) > 1000 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reason must be 1000 characters or less"})
			return
		}
		result, err := runtimeStore.RequestJoinWorkspace(r.Context(), store.RequestJoinWorkspaceInput{
			WorkspaceID: workspaceID,
			UserID:      userID,
			Reason:      body.Reason,
			Now:         time.Now().UTC(),
		})
		if err != nil {
			if errors.Is(err, store.ErrUnknownWorkspace) {
				// Covers two cases: does not exist / private and not open — always 404 to prevent enumeration
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found or not open to join requests"})
				return
			}
			writeReadError(w, err, "failed to submit join request")
			return
		}
		if result.Already {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":   "already a member or pending request exists",
				"request": result.Request,
			})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"request": result.Request,
		})
	}
}

// withdrawOwnJoinRequest — requester self-withdraws a pending request. Path
// carries no requestID: the requester has only one pending row, and
// (workspace_id, current_user_id) uniquely locates it, so the client doesn't
// need to hold a request id either.
func withdrawOwnJoinRequest(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		userID, ok := devActorID(w, r)
		if !ok {
			return
		}
		if err := runtimeStore.WithdrawOwnJoinRequest(r.Context(), workspaceID, userID, time.Now().UTC()); err != nil {
			if errors.Is(err, store.ErrJoinRequestAlreadyHandled) {
				// No pending row found: may have been approved/rejected by owner, or the user never applied
				writeJSON(w, http.StatusConflict, map[string]string{"error": "no pending request to withdraw"})
				return
			}
			writeReadError(w, err, "failed to withdraw join request")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func listJoinRequests(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		rows, err := runtimeStore.ListPendingJoinRequests(r.Context(), workspaceID)
		if err != nil {
			writeReadError(w, err, "failed to list join requests")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"workspace_id": workspaceID,
			"requests":     rows,
		})
	}
}

func approveJoinRequest(runtimeStore RuntimeStore) http.HandlerFunc {
	return reviewJoinRequestHandler(runtimeStore, true)
}

func rejectJoinRequest(runtimeStore RuntimeStore) http.HandlerFunc {
	return reviewJoinRequestHandler(runtimeStore, false)
}

func reviewJoinRequestHandler(runtimeStore RuntimeStore, approve bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		requestID := strings.TrimSpace(chi.URLParam(r, "requestID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if !isUUID(requestID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		reviewerID, ok := devActorID(w, r)
		if !ok {
			return
		}
		input := store.ReviewJoinRequestInput{
			WorkspaceID: workspaceID,
			RequestID:   requestID,
			ReviewerID:  reviewerID,
			Now:         time.Now().UTC(),
		}
		var (
			member store.WorkspaceMemberRead
			err    error
		)
		if approve {
			member, err = runtimeStore.ApproveJoinRequest(r.Context(), input)
		} else {
			member, err = runtimeStore.RejectJoinRequest(r.Context(), input)
		}
		if err != nil {
			if errors.Is(err, store.ErrJoinRequestAlreadyHandled) {
				// Already handled by another admin / row is not in pending state
				writeJSON(w, http.StatusConflict, map[string]string{"error": "join request already handled"})
				return
			}
			writeReadError(w, err, "failed to review join request")
			return
		}
		writeJSON(w, http.StatusOK, member)
	}
}

// listDiscoverableWorkspaces — `GET /api/v1/me/discoverable-workspaces`
// Public workspaces the current user can request to join.
//
// Query params:
//   - q     : fuzzy search on workspace.name (case-insensitive); empty returns all
//   - limit : default 50, clamped to [1, 100]
//   - offset: default 0
//
// Response carries a `total` field (post-filter count); the frontend uses it for
// "View all (N)" and pagination.
func listDiscoverableWorkspaces(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		userID := strings.TrimSpace(auth.UserIDFromContext(r.Context()))
		if userID == "" {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "authenticated user missing from request context"})
			return
		}
		if !isUUID(userID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id must be a valid uuid"})
			return
		}
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		// Limit search-term length to prevent malicious overlong strings from slowing ILIKE index scans
		if len(q) > 100 {
			q = q[:100]
		}
		offset := parseOffset(r)
		result, err := runtimeStore.ListDiscoverableWorkspaces(r.Context(), store.ListDiscoverableWorkspacesInput{
			UserID: userID,
			Search: q,
			Limit:  parseLimit(r, 50),
			Offset: offset,
		})
		if err != nil {
			writeReadError(w, err, "failed to list discoverable workspaces")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"user_id":    userID,
			"workspaces": result.Workspaces,
			"total":      result.Total,
			"limit":      parseLimit(r, 50),
			"offset":     offset,
		})
	}
}
