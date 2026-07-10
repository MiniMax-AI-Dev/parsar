package dev

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"net/http"
	"strconv"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

// devActorID returns the authenticated caller for dev writes. Require
// middleware should have populated the context before handlers run.
func devActorID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := strings.TrimSpace(auth.UserIDFromContext(r.Context()))
	if userID == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "authenticated user missing from request context"})
		return "", false
	}
	if !isUUID(userID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id must be a valid uuid"})
		return "", false
	}
	return userID, true
}

func parseLimit(r *http.Request, fallback int32) int32 {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return fallback
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return fallback
	}
	if limit > 500 {
		return 500
	}
	return int32(limit)
}

// parseOffset reads ?offset=N for paginated endpoints. Defaults to 0;
// clamps negative inputs to 0 (pagination underflow is meaningless).
func parseOffset(r *http.Request) int32 {
	raw := strings.TrimSpace(r.URL.Query().Get("offset"))
	if raw == "" {
		return 0
	}
	offset, err := strconv.Atoi(raw)
	if err != nil || offset < 0 {
		return 0
	}
	return int32(offset)
}

func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

func writeReadError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, store.ErrUnknownWorkspace), errors.Is(err, store.ErrUnknownConversationForRead), errors.Is(err, store.ErrUnknownAgentRun), errors.Is(err, store.ErrUnknownConversation):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrDuplicateWorkspaceSlug):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrMarketplaceDependents):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "has_marketplace_dependents", "message": err.Error()})
	case errors.Is(err, store.ErrInvalidWorkspaceInput), errors.Is(err, store.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fallback})
	}
}

func writeStoreAgentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrDuplicateAgentSlug):
		suggested := ""
		parts := strings.Split(err.Error(), ": ")
		if len(parts) > 1 {
			suggested = parts[len(parts)-1]
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": "slug_conflict", "suggested": suggested})
	case errors.Is(err, store.ErrUnknownCapability):
		invalid := []string{}
		parts := strings.Split(err.Error(), ": ")
		if len(parts) > 1 && strings.TrimSpace(parts[len(parts)-1]) != "" {
			invalid = strings.Split(parts[len(parts)-1], ",")
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "unknown_capability", "invalid": invalid})
	case errors.Is(err, store.ErrUnknownCapabilityVersion):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrMarketplaceCapabilityUnavailable):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrUnknownAgent), errors.Is(err, store.ErrUnknownAgent), errors.Is(err, store.ErrUnknownWorkspace), errors.Is(err, store.ErrUnknownWorkspace):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrInvalidConnectorType), errors.Is(err, store.ErrInvalidInput), errors.Is(err, store.ErrInvalidAgentVisibility):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "agent operation failed"})
	}
}

func decodeJSONWithField(r *http.Request, target any, field string) (bool, error) {
	fields, err := decodeJSONWithFields(r, target)
	if err != nil {
		return false, err
	}
	return fields[field], nil
}

func decodeJSONWithFields(r *http.Request, target any) (map[string]bool, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, target); err != nil {
		return nil, err
	}
	fields := make(map[string]bool, len(raw))
	for field := range raw {
		fields[field] = true
	}
	return fields, nil
}

func actorIDFromRequest(r *http.Request) string {
	if userID := auth.UserIDFromContext(r.Context()); userID != "" {
		return userID
	}
	return store.DefaultDevFixtureIDs().UserID
}

func requestContextForRBAC(r *http.Request) context.Context {
	if auth.UserIDFromContext(r.Context()) != "" {
		return r.Context()
	}
	return auth.WithUserID(r.Context(), store.DefaultDevFixtureIDs().UserID)
}

func requireWorkspaceOwnerOrAdmin(r *http.Request, runtimeStore RuntimeStore, workspaceID string) error {
	return auth.RequireWorkspaceRole(requestContextForRBAC(r), runtimeStore, workspaceID, "owner", "admin")
}

// requireWorkspaceMember gates read endpoints scoped to a workspace.
// Returns ErrNotMember when the caller is not an active member of the
// workspace.
func requireWorkspaceMember(r *http.Request, runtimeStore RuntimeStore, workspaceID string) error {
	return auth.RequireWorkspaceRole(requestContextForRBAC(r), runtimeStore, workspaceID, "owner", "admin", "member", "viewer")
}

// requireWorkspaceMemberNotViewer gates write endpoints that any
// non-viewer member of the workspace can perform — creating a
// conversation, triggering a run, editing one's own conversation
// title, etc. viewer is read-only.
func requireWorkspaceMemberNotViewer(r *http.Request, runtimeStore RuntimeStore, workspaceID string) error {
	return auth.RequireWorkspaceRole(requestContextForRBAC(r), runtimeStore, workspaceID, "owner", "admin", "member")
}

// gateWorkspaceMember wraps a handler whose URL is
// /workspaces/{workspaceID}/... and rejects callers that aren't an
// active member. Used by sandbox admin endpoints whose handlers don't
// carry runtimeStore — wrapping at register-time avoids polluting
// sandboxAdminDeps.
//
// Returns 503 when runtimeStore is nil (local-mode server without
// DB) so the response surface matches the other DB-backed endpoints
// instead of silently bypassing RBAC.
func gateWorkspaceMember(runtimeStore RuntimeStore, next http.HandlerFunc) http.HandlerFunc {
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
		next(w, r)
	}
}

// gateWorkspaceOwnerOrAdmin gates sandbox kill / rebuild on owner+admin
// only. Mid-run kill interrupts an Agent task in flight.
func gateWorkspaceOwnerOrAdmin(runtimeStore RuntimeStore, next http.HandlerFunc) http.HandlerFunc {
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
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
