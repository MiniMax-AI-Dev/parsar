package dev

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

func agentStatusHandler(runtimeStore RuntimeStore, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed agent lifecycle is disabled"})
			return
		}
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		workspaceID, ok := workspaceIDForAgent(w, r.Context(), runtimeStore, agentID)
		if !ok {
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var (
			result store.AgentStatusRead
			err    error
		)
		switch action {
		case "disable":
			result, err = runtimeStore.DisableAgent(r.Context(), agentID, actorIDFromRequest(r))
		case "enable":
			result, err = runtimeStore.EnableAgent(r.Context(), agentID, actorIDFromRequest(r))
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unsupported agent action"})
			return
		}
		if err != nil {
			if errors.Is(err, store.ErrUnknownAgent) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to %s agent", action)})
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}
