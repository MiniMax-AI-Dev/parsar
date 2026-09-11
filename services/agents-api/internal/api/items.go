package api

import (
	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/go-chi/chi/v5"
	"net/http"
)

// @Summary List persisted execution Items
// @Description Returns supported message and tool Items in first-observation order. Native engine fields are projected explicitly; unfinished Items on terminal Turns are incomplete. Cursors belong to the same tenant and Session.
// @Tags Items
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Param after query string false "Last Item ID from the previous page"
// @Param limit query int false "Page size" minimum(1) maximum(100) default(20)
// @Param order query string false "Creation order" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.ItemList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/items [get]
func (h *Handler) listItems(w http.ResponseWriter, r *http.Request) {
	options, ok := readPage(w, r)
	if !ok {
		return
	}
	page, err := h.store.ListItems(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), options.after, options.limit, options.ascending)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.ItemList{Data: page.Items, HasMore: page.HasMore})
}
