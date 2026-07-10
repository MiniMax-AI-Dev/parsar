package dev

import (
	"encoding/json"
	"errors"
	"io"

	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type requeueAgentRunBody struct {
	Reason string `json:"reason"`
}

// requeueAgentRun re-schedules a failed or stalled agent run.
//
//	@Summary		Re-queue an agent run
//	@Description	Re-schedules a failed or stalled agent run. Owner/admin only.
//	@Tags			agent-runs
//	@ID				requeueDevAgentRun
//	@Accept			json
//	@Produce		json
//	@Param			runID	path	string				true	"Agent run UUID"
//	@Param			body	body	requeueAgentRunBody	true	"Requeue payload"
//	@Success		200 {object} map[string]interface{} "Requeued run"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Run not found"
//	@Router			/api/v1/agent-runs/{runID}/requeue [post]
func requeueAgentRun(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed run retry is disabled"})
			return
		}
		runID := strings.TrimSpace(chi.URLParam(r, "runID"))
		if !isUUID(runID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run_id must be a valid uuid"})
			return
		}

		var req requeueAgentRunBody
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
		}
		run, err := runtimeStore.GetAgentRun(r.Context(), runID)
		if err != nil {
			writeReadError(w, err, "failed to get agent run")
			return
		}
		if err := requireWorkspaceMemberNotViewer(r, runtimeStore, run.WorkspaceID); err != nil {
			if errors.Is(err, auth.ErrNotMember) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
				return
			}
			writeRBACError(w, err)
			return
		}
		reason := strings.TrimSpace(req.Reason)
		if reason == "" {
			reason = "manual_retry"
		}
		result, err := runtimeStore.RequeueFailedAgentRun(r.Context(), store.RequeueAgentRunInput{RunID: runID, Source: "dev_retry", Reason: reason})
		if err != nil {
			switch {
			case errors.Is(err, store.ErrUnknownAgentRun):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrAgentRunNotCompletable):
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to requeue agent run"})
			}
			return
		}
		if recorder, ok := runtimeStore.(runLifecycleEventRecorder); ok {
			recordRunLifecycleEvent(recorder, runID, "run.requeued", map[string]any{"source": "dev_retry", "reason": reason, "previous_status": run.Status}, time.Now().UTC())
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// getAgentRun returns details for a single agent run.
//
//	@Summary		Get an agent run
//	@Description	Returns the full record for a single agent run. Caller must belong to the run's workspace.
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
//	@Description	Returns agent-run rows for the workspace. Caller must be a workspace member.
//	@Tags			agent-runs
//	@ID				listDevWorkspaceAgentRuns
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
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

		result, err := runtimeStore.ListWorkspaceAgentRuns(r.Context(), workspaceID, statuses, limit, offset)
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
