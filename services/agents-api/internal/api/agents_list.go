package api

import (
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

// @Summary List reusable Agents
// @Description Lists only the authenticated tenant's saved Agents, independently of Sessions. Positive int64 limits are accepted; each page returns at most 100 resources with continuation. The local default is 20; exact upstream default/cap, empty cursor fields and error conformance remain unverified.
// @Tags Agents
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param after query string false "Last Agent ID from the previous page"
// @Param limit query int64 false "Maximum requested resources; pages contain at most 100" minimum(1)
// @Param order query string false "Creation order" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.SavedAgentList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /agents [get]
func (h *Handler) listAgents(w http.ResponseWriter, r *http.Request) {
	options, ok := readPageSize(w, r, false)
	if !ok {
		return
	}
	page, err := h.store.ListAgents(r.Context(), tenantID(r), options.after, options.limit, options.ascending)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response := v1.SavedAgentList{Object: "list", Data: make([]v1.SavedAgent, 0, len(page.Agents)), HasMore: page.NextCursor != ""}
	for _, agent := range page.Agents {
		item, err := agentResponse(agent)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		response.Data = append(response.Data, item)
	}
	if len(response.Data) > 0 {
		response.FirstID = &response.Data[0].ID
		response.LastID = &response.Data[len(response.Data)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
}
