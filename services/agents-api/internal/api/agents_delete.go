package api

import (
	"bytes"
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

// @Summary Delete a reusable Agent
// @Description Deletes only the authenticated tenant's saved configuration. Existing Session snapshots, history and recorded creation retry identities remain independent. Missing and repeated deletion locally return404; exact hosted error and in-flight creation/deletion semantics remain unverified.
// @Tags Agents
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param agent_id path string true "Agent ID"
// @Success 200 {object} v1.AgentDeleted
// @Failure 400,401,404,413,500 {object} v1.ErrorResponse
// @Router /agents/{agent_id} [delete]
func (h *Handler) deleteAgent(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Agent deletion does not accept query parameters.")
		return
	}
	body, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Agent deletion does not accept a request body.")
		return
	}
	id := chi.URLParam(r, "agent_id")
	if !validAgentID(id) {
		writeStoreError(w, r, store.ErrNotFound)
		return
	}
	deleted, err := h.store.DeleteAgent(r.Context(), tenantID(r), id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.AgentDeleted{ID: deleted, Object: "agent.deleted", Deleted: true})
}
