package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"net/http"
	"strconv"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

// disableAgent disables an active agent.
//
//	@Summary		Disable an agent
//	@Description	Marks the agent as disabled so it is no longer scheduled. Owner/admin only; audits the requesting user.
//	@Tags			agents
//	@ID				disableDevAgent
//	@Produce		json
//	@Param			agentID	path	string	true	"Agent UUID"
//	@Success		200 {object} map[string]interface{} "Updated agent"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Agent not found"
//	@Router			/api/v1/agents/{agentID}/disable [post]
func disableAgent(runtimeStore RuntimeStore) http.HandlerFunc {
	return agentStatusHandler(runtimeStore, "disable")
}

// enableAgent enables a disabled agent.
//
//	@Summary		Enable an agent
//	@Description	Marks a disabled agent as enabled so it can be scheduled again. Owner/admin only; audits the requesting user.
//	@Tags			agents
//	@ID				enableDevAgent
//	@Produce		json
//	@Param			agentID	path	string	true	"Agent UUID"
//	@Success		200 {object} map[string]interface{} "Updated agent"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Agent not found"
//	@Router			/api/v1/agents/{agentID}/enable [post]
func enableAgent(runtimeStore RuntimeStore) http.HandlerFunc {
	return agentStatusHandler(runtimeStore, "enable")
}

type createAgentBody struct {
	ResourceBindings []store.InitialAgentCapabilityInput `json:"resource_bindings"`
	Name             string                              `json:"name"`
	Description      string                              `json:"description"`
	ConnectorType    string                              `json:"connector_type"`
	SystemPrompt     string                              `json:"system_prompt"`
	Config           map[string]any                      `json:"config"`
	Visibility       string                              `json:"visibility"`
	Slug             string                              `json:"slug"`
}

type updateAgentBody struct {
	ResourceBindings *[]store.InitialAgentCapabilityInput `json:"resource_bindings"`
	Name             *string                              `json:"name"`
	Description      *string                              `json:"description"`
	ConnectorType    *string                              `json:"connector_type"`
	SystemPrompt     *string                              `json:"system_prompt"`
	Config           map[string]any                       `json:"config"`
}

// createAgent creates a new agent in a workspace. Owner/admin only.
//
//	@Summary		Create an agent in a workspace
//	@Description	Creates an Agent with a workspace catalog model_id, explicit harness and environment. Parsar resolves the execution model; Core runs it. Owner/admin only.
//	@Tags			agents
//	@ID				createDevAgent
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string				true	"Workspace UUID"
//	@Param			body		body	createAgentBody		true	"Agent create payload"
//	@Success		201 {object} map[string]interface{} "Created agent"
//	@Failure		400 {object} map[string]string "workspace_id must be a valid uuid, or body invalid"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		422 {object} map[string]string "Immutable field or unknown capability"
//	@Router			/api/v1/workspaces/{workspaceID}/agents [post]
func createAgent(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if runtimeStore == nil || !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req createAgentBody
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Core Agent payload"})
			return
		}
		if req.ConnectorType == "" {
			req.ConnectorType = "agents_api"
		}
		if req.ConnectorType != "agents_api" {
			writeStoreAgentError(w, store.ErrInvalidConnectorType)
			return
		}
		if err := resolveCatalogAgentModel(r.Context(), runtimeStore, workspaceID, req.Config); err != nil {
			writeReadError(w, err, "model selection failed")
			return
		}
		if err := store.ValidateCoreAgentConfig(req.Config, true); err != nil {
			writeStoreAgentError(w, err)
			return
		}
		if err := validateAgentResources(r.Context(), runtimeStore, workspaceID, req.Visibility, "", req.Config, req.ResourceBindings); err != nil {
			writeStoreAgentError(w, err)
			return
		}
		result, err := runtimeStore.CreateAgent(r.Context(), store.CreateAgentInput{WorkspaceID: workspaceID, Name: req.Name, Description: req.Description, ConnectorType: req.ConnectorType, SystemPrompt: req.SystemPrompt, AgentConfig: req.Config, Visibility: req.Visibility, Slug: req.Slug, CreatedBy: actorIDFromRequest(r), InitialCapabilities: req.ResourceBindings})
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

// updateAgent applies a partial update to an existing agent.
//
//	@Summary		Update mutable agent fields
//	@Description	Updates Agent display fields, catalog model selection and instructions. Existing conversations retain their Core configuration snapshot. Unknown fields are rejected.
//	@Tags			agents
//	@ID				updateDevAgent
//	@Accept			json
//	@Produce		json
//	@Param			agentID	path	string			true	"Agent UUID"
//	@Param			body	body	updateAgentBody	true	"Partial agent update"
//	@Success		200 {object} map[string]interface{} "Updated agent"
//	@Failure		400 {object} map[string]string "Malformed request body or invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		422 {object} map[string]string "Invalid Agent configuration"
//	@Router			/api/v1/agents/{agentID} [patch]
func updateAgent(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if runtimeStore == nil || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		agent, err := runtimeStore.GetAgent(r.Context(), agentID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, agent.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req updateAgentBody
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Core Agent payload"})
			return
		}
		if req.ConnectorType != nil && *req.ConnectorType != "agents_api" {
			writeStoreAgentError(w, store.ErrInvalidConnectorType)
			return
		}
		if err := resolveCatalogAgentModel(r.Context(), runtimeStore, agent.WorkspaceID, req.Config); err != nil {
			writeReadError(w, err, "model selection failed")
			return
		}
		if err := store.ValidateCoreAgentConfig(req.Config, false); err != nil {
			writeStoreAgentError(w, err)
			return
		}
		config := req.Config
		if config == nil {
			config = agent.Config
		}
		bindings, err := requestedAgentResources(r.Context(), runtimeStore, agentID, req.ResourceBindings)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		if err := validateAgentResources(r.Context(), runtimeStore, agent.WorkspaceID, agent.Visibility, agentID, config, bindings); err != nil {
			writeStoreAgentError(w, err)
			return
		}
		updated, _, err := runtimeStore.UpdateAgent(r.Context(), store.UpdateAgentInput{AgentID: agentID, ActorID: actorIDFromRequest(r), Name: req.Name, Description: req.Description, ConnectorType: req.ConnectorType, SystemPrompt: req.SystemPrompt, Config: req.Config, ConfigSet: req.Config != nil, ResourceBindings: bindings, ResourceBindingsSet: req.ResourceBindings != nil})
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agent": updated})
	}
}

// updateAgentVisibilityBody is the request body for
// PATCH /api/v1/agents/{agentID}/visibility.
type updateAgentVisibilityBody struct {
	Visibility string `json:"visibility"`
}

// updateAgentVisibility flips an Agent's visibility between
// workspace / tenant / public. Owner/admin only. Identical visibility
// is treated as a 200 noop so idempotent replays don't pollute audit.
// updateAgentVisibility updates an agent's visibility setting.
//
//	@Summary		Update an agent's visibility
//	@Description	Updates an agent's visibility (public / tenant / private). Owner/admin only. Rejected if bindings are inconsistent with the requested visibility.
//	@Tags			agents
//	@ID				updateDevAgentVisibility
//	@Accept			json
//	@Produce		json
//	@Param			agentID	path	string						true	"Agent UUID"
//	@Param			body	body	updateAgentVisibilityBody	true	"Visibility payload"
//	@Success		200 {object} map[string]interface{} "Updated agent"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		422 {object} map[string]string "Visibility conflicts with agent bindings"
//	@Router			/api/v1/agents/{agentID}/visibility [patch]
func updateAgentVisibility(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if runtimeStore == nil || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		agent, err := runtimeStore.GetAgent(r.Context(), agentID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, agent.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req updateAgentVisibilityBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if err := validateAgentCapabilityBindingsForVisibility(r.Context(), runtimeStore, agent, req.Visibility); err != nil {
			var validationErr *capabilityCredentialValidationError
			if errors.As(err, &validationErr) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": validationErr.Error()})
				return
			}
			writeStoreAgentError(w, err)
			return
		}
		change, err := runtimeStore.UpdateAgentVisibility(r.Context(), agentID, req.Visibility, actorIDFromRequest(r))
		if err != nil {
			if errors.Is(err, store.ErrInvalidAgentVisibility) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
				return
			}
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"visibility": change})
	}
}

// deleteAgent soft-deletes an agent. Owner/admin only.
//
//	@Summary		Soft-delete an agent
//	@Description	Marks the agent as deleted; existing conversations are retained. Owner/admin only.
//	@Tags			agents
//	@ID				deleteDevAgent
//	@Produce		json
//	@Param			agentID	path	string	true	"Agent UUID"
//	@Success		200 {object} map[string]interface{} "Deleted agent"
//	@Failure		400 {object} map[string]string "agent_id must be a valid uuid"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Unknown agent"
//	@Router			/api/v1/agents/{agentID} [delete]
func deleteAgent(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if runtimeStore == nil || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		agent, err := runtimeStore.GetAgent(r.Context(), agentID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, agent.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		result, runCount, err := runtimeStore.DeleteAgent(r.Context(), agentID, actorIDFromRequest(r))
		if errors.Is(err, store.ErrInFlightAgentRuns) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "in_flight_runs", "run_count": runCount})
			return
		}
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// listWorkspaceEnabledAgents lists the agents visible to the caller in
// a workspace. By default only enabled/active agents are returned;
// `?include_disabled=true` widens the query to the admin view.
//
//	@Summary		List active agents in a workspace
//	@Description	Returns agents the caller can address in the given workspace. By default only enabled/active agents; pass include_disabled=true for the admin view.
//	@Tags			agents
//	@ID				listDevAgents
//	@Produce		json
//	@Param			workspaceID			path	string	true	"Workspace UUID"
//	@Param			include_disabled	query	bool	false	"Include disabled/archived agents in the response"
//	@Success		200 {object} map[string]interface{} "Active workspace agents with profile basics"
//	@Failure		400 {object} map[string]string "workspace_id must be a valid uuid"
//	@Failure		403 {object} map[string]string "Caller is not an active workspace member"
//	@Failure		503 {object} map[string]string "Database-backed read APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/agents [get]
func listWorkspaceEnabledAgents(runtimeStore RuntimeStore) http.HandlerFunc {
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

		includeDisabled := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_disabled")), "true")
		var (
			agents []store.AgentRead
			err    error
		)
		if includeDisabled {
			agents, err = runtimeStore.ListWorkspaceAgentsForAdmin(r.Context(), workspaceID)
		} else {
			agents, err = runtimeStore.ListWorkspaceEnabledAgents(r.Context(), workspaceID)
		}
		if err != nil {
			writeReadError(w, err, "failed to list agents")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "agents": agents})
	}
}

// getWorkspaceAgent returns one Agent after workspace membership and ownership checks.
//
//	@Summary		Get an agent in a workspace
//	@Description	Returns the complete agent summary and persisted config. The caller must be an active member of the workspace, and the agent must belong to that workspace.
//	@Tags			agents
//	@ID			getDevAgent
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Param			agentID		path	string	true	"Agent UUID"
//	@Success		200 {object} store.AgentSummary "Complete agent summary and config"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is forbidden from accessing the workspace"
//	@Failure		404 {object} map[string]string "Workspace membership or agent not found"
//	@Failure		503 {object} map[string]string "Database-backed read APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/agents/{agentID} [get]
func getWorkspaceAgent(runtimeStore RuntimeStore) http.HandlerFunc {
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
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}

		agent, err := runtimeStore.GetAgent(r.Context(), agentID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		if agent.WorkspaceID != workspaceID {
			writeStoreAgentError(w, fmt.Errorf("%w: %s", store.ErrUnknownAgent, agentID))
			return
		}

		writeJSON(w, http.StatusOK, agent)
	}
}

// getAgentMetrics returns aggregated run-history counters for
// a single agent over a sliding window. Powers the agent-detail
// "Last N days performance" panel: completion count, success rate, average duration.
// `?days=` is optional and clamps to [1, 365]; default 30.
// getAgentMetrics returns runtime/usage metrics for an agent.
//
//	@Summary		Get agent metrics
//	@Description	Returns runtime/usage metrics for the agent within the workspace. Caller must be a workspace member.
//	@Tags			agents
//	@ID				getDevAgentMetrics
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Param			agentID		path	string	true	"Agent UUID"
//	@Success		200 {object} map[string]interface{} "Agent metrics"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not a workspace member"
//	@Failure		404 {object} map[string]string "Agent not found"
//	@Router			/api/v1/workspaces/{workspaceID}/agents/{agentID}/metrics [get]
func getAgentMetrics(runtimeStore RuntimeStore) http.HandlerFunc {
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
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}

		days := int32(30)
		if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil {
				if v < 1 {
					v = 1
				} else if v > 365 {
					v = 365
				}
				days = int32(v)
			}
		}

		metrics, err := runtimeStore.GetAgentMetrics(r.Context(), agentID, days)
		if err != nil {
			writeReadError(w, err, "failed to load agent metrics")
			return
		}
		writeJSON(w, http.StatusOK, metrics)
	}
}

func workspaceIDForAgent(w http.ResponseWriter, ctx context.Context, runtimeStore RuntimeStore, agentID string) (string, bool) {
	agent, err := runtimeStore.GetAgentDetail(ctx, agentID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownAgent) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return "", false
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load agent"})
		return "", false
	}
	return agent.WorkspaceID, true
}
