package dev

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

func listAgentRunEvents(runtimeStore RuntimeStore) http.HandlerFunc {
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
		runID := strings.TrimSpace(chi.URLParam(r, "runID"))
		if !isUUID(runID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run_id must be a valid uuid"})
			return
		}
		run, err := runtimeStore.GetAgentRun(r.Context(), runID)
		if err != nil {
			writeReadError(w, err, "failed to get agent run")
			return
		}
		if run.WorkspaceID != workspaceID {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent run does not belong to workspace"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		afterSequence, ok := parseAfterSequence(w, r)
		if !ok {
			return
		}
		events, err := runtimeStore.ListAgentRunEvents(r.Context(), runID, afterSequence)
		if err != nil {
			writeReadError(w, err, "failed to list agent run events")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events})
	}
}

func parseAfterSequence(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("after_sequence"))
	if raw == "" {
		return 0, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "after_sequence must be a non-negative integer"})
		return 0, false
	}
	return value, true
}
