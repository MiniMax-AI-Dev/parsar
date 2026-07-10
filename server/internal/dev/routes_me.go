package dev

import (
	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// searchUsers backs the platform-wide user picker. Returns at most 20
// users matching q substring, optionally hiding users already in a
// workspace. RBAC: any authenticated user (add-member action still
// goes through workspace owner/admin gate).
// searchUsers looks up users by keyword.
//
//	@Summary		Search users
//	@Description	Searches directory users by name, email, or handle. Used for member invite pickers.
//	@Tags			me
//	@ID				searchDevUsers
//	@Produce		json
//	@Param			q	query	string	false	"Search keyword"
//	@Success		200 {object} map[string]interface{} "User list"
//	@Failure		401 {object} map[string]string "Caller is not authenticated"
//	@Router			/api/v1/users/search [get]
func searchUsers(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q is required"})
			return
		}
		excludeWS := strings.TrimSpace(r.URL.Query().Get("exclude_workspace"))
		// Silently ignore garbage UUIDs — the store treats unparseable
		// input as "no filter" and the picker is read-only.
		if excludeWS != "" && !isUUID(excludeWS) {
			excludeWS = ""
		}

		items, err := runtimeStore.SearchUsers(r.Context(), store.SearchUsersInput{
			Query:              q,
			ExcludeWorkspaceID: excludeWS,
			Limit:              20,
		})
		if err != nil {
			writeReadError(w, err, "failed to search users")
			return
		}

		// Exclude the caller from results so the picker never offers
		// "add yourself".
		selfID := strings.TrimSpace(auth.UserIDFromContext(r.Context()))
		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			if selfID != "" && it.ID == selfID {
				continue
			}
			out = append(out, map[string]any{
				"id":         it.ID,
				"email":      it.Email,
				"name":       it.Name,
				"avatar_url": it.AvatarURL,
				"status":     it.Status,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	}
}

// listMyWorkspaces returns the workspaces the authenticated caller belongs to.
// Platform admins (auth.IsPlatformAdmin) get the full list of active
// workspaces with role=owner so they can drop into any tenant.
// listMyWorkspaces returns workspaces the caller belongs to.
//
//	@Summary		List my workspaces
//	@Description	Returns workspaces the caller is a member of.
//	@Tags			me
//	@ID				listDevMyWorkspaces
//	@Produce		json
//	@Success		200 {object} map[string]interface{} "Workspace list"
//	@Failure		401 {object} map[string]string "Caller is not authenticated"
//	@Router			/api/v1/me/workspaces [get]
func listMyWorkspaces(runtimeStore RuntimeStore) http.HandlerFunc {
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
		limit := parseLimit(r, 50)
		var (
			workspaces []store.UserWorkspaceRead
			err        error
		)
		if auth.IsPlatformAdmin(userID) {
			workspaces, err = runtimeStore.ListAllActiveWorkspaces(r.Context(), limit)
		} else {
			workspaces, err = runtimeStore.ListUserWorkspaces(r.Context(), userID, limit)
		}
		if err != nil {
			writeReadError(w, err, "failed to list user workspaces")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"user_id":    userID,
			"workspaces": workspaces,
		})
	}
}
