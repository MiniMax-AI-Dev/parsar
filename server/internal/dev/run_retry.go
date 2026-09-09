package dev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type agentRunRetryStore interface {
	RetryAgentRun(context.Context, store.RetryAgentRunInput) (store.RetryAgentRunResult, error)
}

// retryAgentRun creates a separate manual execution for a terminal run.
//
//	@Summary		Retry an agent run
//	@Description	Creates and dispatches one replacement execution for a failed, cancelled, or interrupted run, preserving its history. Repeated requests return the same replacement. Workspace owner/admin/member only.
//	@Tags			agent-runs
//	@ID				retryAgentRun
//	@Accept			json
//	@Produce		json
//	@Param			runID	path	string	true	"Source run UUID"
//	@Param			body	body	requeueAgentRunBody	false	"Retry reason"
//	@Success		200	{object}	store.RetryAgentRunResult
//	@Failure		400	{object}	map[string]string	"Invalid request"
//	@Failure		403	{object}	map[string]string	"Caller cannot execute runs"
//	@Failure		404	{object}	map[string]string	"Run not found"
//	@Failure		409	{object}	map[string]string	"Run cannot be retried"
//	@Failure		500	{object}	map[string]string	"Retry failed"
//	@Failure		503	{object}	map[string]string	"Retry store unavailable"
//	@Router			/api/v1/agent-runs/{runID}/retry [post]
func retryAgentRun(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		retryStore, ok := runtimeStore.(agentRunRetryStore)
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "run retry is unavailable"})
			return
		}
		runID := chi.URLParam(r, "runID")
		if !isUUID(runID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run_id must be a valid uuid"})
			return
		}
		run, err := runtimeStore.GetAgentRun(r.Context(), runID)
		if err != nil {
			writeReadError(w, err, "failed to get agent run")
			return
		}
		if err := requireWorkspaceMemberNotViewer(r, runtimeStore, run.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		userID, ok := devActorID(w, r)
		if !ok {
			return
		}
		var body requeueAgentRunBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		result, err := retryStore.RetryAgentRun(r.Context(), store.RetryAgentRunInput{RunID: runID, UserID: userID, Reason: strings.TrimSpace(body.Reason)})
		if err != nil {
			writeReadError(w, err, "failed to retry agent run")
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}
