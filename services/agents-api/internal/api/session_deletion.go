package api

import (
	"bytes"
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// @Summary Delete an execution Session
// @Description Removes the Session and its history from the public API. Active work receives a cancellation request; confirmation does not guarantee native execution has stopped. Internal records and native history are retained pending separate physical cleanup. Missing/repeated deletion locally returns 404; exact hosted errors and overlapping stream timing remain unverified.
// @Tags Sessions
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Success 200 {object} v1.SessionDeleted
// @Failure 400,401,404,413,500 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id} [delete]
func (h *Handler) deleteSession(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Session deletion does not accept query parameters.")
		return
	}
	body, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Session deletion does not accept a request body.")
		return
	}
	id := chi.URLParam(r, "session_id")
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		writeStoreError(w, r, store.ErrNotFound)
		return
	}
	if err := h.store.DeleteSession(r.Context(), tenantID(r), id); err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.SessionDeleted{ID: parsed.String(), Object: "agent.session.deleted", Deleted: true})
}
