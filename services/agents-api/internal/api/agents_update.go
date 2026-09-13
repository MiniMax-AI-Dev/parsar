package api

import (
	"encoding/json"
	"errors"
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// @Summary Update a reusable Agent
// @Description Preserves omitted fields and replaces supplied fields using shared saved-configuration validation. Null name/instructions clear; null or empty metadata clears all pairs. Existing Session snapshots are unchanged. Nested replacement/null defaults, model-derived reasoning and exact hosted error/no-op timestamp behavior remain incompletely verified.
// @Tags Agents
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param agent_id path string true "Agent ID"
// @Param body body v1.UpdateAgentRequest true "Supplied reusable Agent fields"
// @Success 200 {object} v1.SavedAgent
// @Failure 400,401,404,413,500 {object} v1.ErrorResponse
// @Router /agents/{agent_id} [post]
func (h *Handler) updateAgent(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Agent updates do not accept query parameters.")
		return
	}
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	input, err := resolveAgentUpdate(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsupported_or_invalid_configuration", err.Error())
		return
	}
	id := chi.URLParam(r, "agent_id")
	if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
		writeStoreError(w, r, store.ErrNotFound)
		return
	}
	updated, err := h.store.UpdateAgent(r.Context(), tenantID(r), id, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.respondAgent(w, r, updated)
}

func resolveAgentUpdate(raw []byte) (store.UpdateAgentInput, error) {
	var request v1.UpdateAgentRequest
	if decodeInputObject(raw, &request, "model", "name", "instructions", "metadata", "multi_agent", "reasoning", "service_tier", "text", "tools") != nil {
		return store.UpdateAgentInput{}, errors.New("Request must be a JSON object containing supported fields.")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return store.UpdateAgentInput{}, err
	}
	if _, supplied := fields["model"]; supplied && request.Model == nil {
		return store.UpdateAgentInput{}, errors.New("model must be a string when supplied.")
	}
	normalized, err := resolveSavedFields(v1.CreateAgentRequest(request))
	if err != nil {
		return store.UpdateAgentInput{}, err
	}
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(normalized.Configuration, &patch); err != nil {
		return store.UpdateAgentInput{}, err
	}
	for field := range patch {
		if _, supplied := fields[field]; !supplied {
			delete(patch, field)
		}
	}
	result := store.UpdateAgentInput{}
	if _, supplied := fields["metadata"]; supplied {
		result.Metadata = &normalized.Metadata
	}
	result.Configuration, err = json.Marshal(patch)
	return result, err
}
