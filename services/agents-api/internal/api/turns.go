package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

// @Summary Retrieve an execution Turn
// @Tags Turns
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Param turn_id path string true "Turn ID"
// @Success 200 {object} v1.Turn
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/turns/{turn_id} [get]
func (h *Handler) getTurn(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Turn retrieval does not accept query parameters.")
		return
	}
	sessionID := chi.URLParam(r, "session_id")
	turn, err := h.store.GetTurn(r.Context(), tenantID(r), sessionID, chi.URLParam(r, "turn_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	session, err := h.store.GetSession(r.Context(), tenantID(r), sessionID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response, err := turnResponse(session, turn)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// @Summary List execution Turns
// @Description Returns persisted state in creation order. The cursor belongs to the same Session and tenant. Usage contains the latest recorded complete token breakdown; missing measurements remain null.
// @Tags Turns
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Param after query string false "Last Turn ID from the previous page"
// @Param limit query int false "Page size" minimum(1) maximum(100) default(20)
// @Param order query string false "Creation order" Enums(asc,desc) default(desc)
// @Success 200 {object} v1.TurnList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/turns [get]
func (h *Handler) listTurns(w http.ResponseWriter, r *http.Request) {
	options, ok := readPage(w, r)
	if !ok {
		return
	}
	sessionID := chi.URLParam(r, "session_id")
	session, err := h.store.GetSession(r.Context(), tenantID(r), sessionID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	page, err := h.store.ListTurns(r.Context(), tenantID(r), sessionID, options.after, options.limit, options.ascending)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	response := v1.TurnList{Data: make([]v1.Turn, 0, len(page.Turns)), HasMore: page.NextCursor != ""}
	for _, turn := range page.Turns {
		item, err := turnResponse(session, turn)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
		response.Data = append(response.Data, item)
	}
	writeJSON(w, http.StatusOK, response)
}

func turnResponse(session store.Session, turn store.Turn) (v1.Turn, error) {
	var cfg configuration
	if err := json.Unmarshal(session.Configuration, &cfg); err != nil || cfg.Agent.ID == "" {
		return v1.Turn{}, errors.New("missing stored agent identity")
	}
	response := v1.Turn{Usage: tokenUsage(turn.Usage), ID: turn.ID, SessionID: turn.SessionID, AgentID: cfg.Agent.ID, Object: "agent.session.turn", Status: turn.Status, CreatedAt: turn.CreatedAt.Unix(), StartedAt: unixTime(turn.StartedAt), CompletedAt: unixTime(turn.CompletedAt)}
	if turn.Status == store.TurnFailed {
		// Native errors can contain secrets; publish a stable category without raw diagnostics.
		response.Error = &v1.TurnError{Code: "internal_error", Message: "The execution could not complete."}
	}
	return response, nil
}

func unixTime(value time.Time) *int64 {
	if value.IsZero() {
		return nil
	}
	seconds := value.Unix()
	return &seconds
}
