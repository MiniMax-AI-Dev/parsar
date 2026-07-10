package dev

import (
	"encoding/json"
	"errors"

	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

// getAgentRuntimeBinding returns the runtime currently bound to
// this agent. Empty runtime_id means the user hasn't picked one
// yet — the dispatcher surfaces "Please bind a Runtime" when a run starts.
// getAgentRuntimeBinding returns the agent's current runtime binding.
//
//	@Summary		Get an agent's runtime binding
//	@Description	Returns the runtime/device currently bound to the agent.
//	@Tags			runtimes
//	@ID				getDevAgentRuntimeBinding
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Param			agentID		path	string	true	"Agent UUID"
//	@Success		200 {object} map[string]interface{} "Runtime binding"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		404 {object} map[string]string "Agent not found"
//	@Router			/api/v1/workspaces/{workspaceID}/agents/{agentID}/runtime [get]
func getAgentRuntimeBinding(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed agent runtime binding is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		binding, err := runtimeStore.GetAgentRuntimeBinding(r.Context(), workspaceID, agentID)
		if err != nil {
			if errors.Is(err, store.ErrUnknownAgent) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read runtime binding"})
			return
		}
		writeJSON(w, http.StatusOK, binding)
	}
}

// setAgentRuntimeBinding writes (or clears) the runtime an
// agent runs on. RuntimeID="" is a valid clear request that
// turns the agent back into an unbound state. Tenant guard: only
// workspace owners / admins can change the binding.
// setAgentRuntimeBinding binds an agent to a runtime/device.
//
//	@Summary		Bind an agent to a runtime
//	@Description	Binds the agent to a runtime/device. Empty runtime_id clears the binding. Owner/admin only.
//	@Tags			runtimes
//	@ID				setDevAgentRuntimeBinding
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string					true	"Workspace UUID"
//	@Param			agentID		path	string					true	"Agent UUID"
//	@Param			body		body	map[string]interface{}	true	"Runtime binding payload"
//	@Success		200 {object} map[string]interface{} "Runtime binding"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Agent not found"
//	@Router			/api/v1/workspaces/{workspaceID}/agents/{agentID}/runtime [post]
func setAgentRuntimeBinding(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed agent runtime binding is disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var body struct {
			RuntimeID string `json:"runtime_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if v := strings.TrimSpace(body.RuntimeID); v != "" && !isUUID(v) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "runtime_id must be a valid uuid or empty"})
			return
		}
		binding, err := runtimeStore.SetAgentRuntime(r.Context(), store.SetAgentRuntimeInput{
			WorkspaceID: workspaceID,
			AgentID:     agentID,
			RuntimeID:   strings.TrimSpace(body.RuntimeID),
		})
		if err != nil {
			if errors.Is(err, store.ErrUnknownAgent) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to set runtime binding"})
			return
		}
		writeJSON(w, http.StatusOK, binding)
	}
}
