package api

import (
	"context"
	"encoding/json"
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type AgentStore interface {
	CreateAgent(context.Context, string, store.CreateAgentInput) (store.SavedAgent, error)
	GetAgent(context.Context, string, string) (store.SavedAgent, error)
}

// @Summary Create a reusable Agent
// @Description Persists configuration independently of execution. Supports model/name/instructions/metadata, explicit reasoning and service tiers, multi_agent, text/json_schema, function/tool_search/programmatic_tool_calling. Model-derived reasoning defaults, MCP/web_search and public retry conformance remain incomplete. Session references and execution admission are separate.
// @Tags Agents
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param body body v1.CreateAgentRequest true "Reusable Agent configuration"
// @Success 200 {object} v1.SavedAgent
// @Failure 400,401,413,500 {object} v1.ErrorResponse
// @Router /agents [post]
func (h *Handler) createAgent(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Agent creation does not accept query parameters.")
		return
	}
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var request v1.CreateAgentRequest
	if decodeInputObject(raw, &request, "model", "name", "instructions", "metadata", "multi_agent", "reasoning", "service_tier", "text", "tools") != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request must be a JSON object containing supported fields.")
		return
	}
	input, err := resolveSavedAgent(request)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsupported_or_invalid_configuration", err.Error())
		return
	}
	agent, err := h.store.CreateAgent(r.Context(), tenantID(r), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.respondAgent(w, r, agent)
}

// @Summary Retrieve a reusable Agent
// @Description Reads the saved resource owned by the authenticated tenant, independently of execution Sessions.
// @Tags Agents
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param agent_id path string true "Agent ID"
// @Success 200 {object} v1.SavedAgent
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /agents/{agent_id} [get]
func (h *Handler) getAgent(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Agent retrieval does not accept query parameters.")
		return
	}
	id := chi.URLParam(r, "agent_id")
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		writeStoreError(w, r, store.ErrNotFound)
		return
	}
	agent, err := h.store.GetAgent(r.Context(), tenantID(r), id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.respondAgent(w, r, agent)
}

func (h *Handler) respondAgent(w http.ResponseWriter, r *http.Request, agent store.SavedAgent) {
	var response v1.SavedAgent
	if err := json.Unmarshal(agent.Configuration, &response.SavedAgentConfiguration); err != nil {
		writeStoreError(w, r, err)
		return
	}
	response.ID, response.Object = agent.ID, "agent"
	response.Metadata = agent.Metadata
	response.CreatedAt, response.UpdatedAt = agent.CreatedAt.Unix(), agent.UpdatedAt.Unix()
	writeJSON(w, http.StatusOK, response)
}
