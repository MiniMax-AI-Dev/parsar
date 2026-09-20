package dev

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

type requeueAgentRunBody struct {
	Reason string `json:"reason"`
}

// getAgentRun returns details for a single agent run.
//
//	@Summary		Get an agent run
//	@Description	Returns a run, including retained history and agent_deleted state after Agent deletion. Caller must belong to the run's workspace.
//	@Tags			agent-runs
//	@ID				getDevAgentRun
//	@Produce		json
//	@Param			runID	path	string	true	"Agent run UUID"
//	@Success		200 {object} map[string]interface{} "Agent run"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller lacks permission"
//	@Failure		404 {object} map[string]string "Run not found"
//	@Router			/api/v1/agent-runs/{runID} [get]
func getAgentRun(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
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
		// Load first to discover the parent workspace, then gate.
		if err := requireWorkspaceMember(r, runtimeStore, run.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, run)
	}
}

// listWorkspaceAgentRuns lists agent runs within a workspace.
//
//	@Summary		List workspace agent runs
//	@Description	Returns agent-run rows, including retained history and agent_deleted state after Agent deletion. Caller must be a workspace member.
//	@Tags			agent-runs
//	@ID				listDevWorkspaceAgentRuns
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Param			q			query	string	false	"Case-insensitive literal search over run ID, Agent name/slug, and conversation ID"
//	@Param			status		query	string	false	"Comma-separated run statuses"
//	@Param			limit		query	int		false	"Page size" default(100)
//	@Param			offset		query	int		false	"Result offset" default(0)
//	@Success		200 {object} map[string]interface{} "Agent run rows"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not a workspace member"
//	@Router			/api/v1/workspaces/{workspaceID}/agent-runs [get]
func listWorkspaceAgentRuns(runtimeStore RuntimeStore) http.HandlerFunc {
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

		// `?status=` accepts a comma-separated list so the admin
		// "In progress" tab can union {running,queued} in one round-trip.
		// Empty values are stripped. The SQL `cardinality(...) = 0`
		// branch handles the no-filter case.
		statuses := parseStatusList(r.URL.Query().Get("status"))
		limit := parseLimit(r, 100)
		offset := parseOffset(r)

		result, err := runtimeStore.ListWorkspaceAgentRuns(r.Context(), workspaceID, statuses, limit, offset, strings.TrimSpace(r.URL.Query().Get("q")))
		if err != nil {
			writeReadError(w, err, "failed to list agent runs")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"workspace_id": workspaceID,
			"statuses":     statuses,
			"agent_runs":   result.Runs,
			"total":        result.Total,
			"limit":        limit,
			"offset":       offset,
		})
	}
}

// parseStatusList splits `?status=a,b,c` into a trimmed, non-empty
// list. Returns nil for "no filter" (empty query string or all blanks)
// so handler code can pass it straight through to the store layer.
func parseStatusList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
