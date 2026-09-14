package api

import (
	"context"
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type sessionStreamCreator interface {
	CreateSessionStream(context.Context, string, store.CreateSessionInput) (store.SessionCreation, error)
}

func (h *Handler) createSessionStream(w http.ResponseWriter, r *http.Request, input store.CreateSessionInput) {
	events, ok := h.store.(eventStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "Live events are unavailable.")
		return
	}
	var source any = h.store
	if len(input.InitialInputs) > 0 {
		if h.inputs == nil {
			writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Execution input is not enabled on this service.")
			return
		}
		source = h.inputs
	}
	creator, ok := source.(sessionStreamCreator)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "Streaming creation is unavailable.")
		return
	}
	result, err := creator.CreateSessionStream(r.Context(), tenantID(r), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.respondSessionCreationStream(w, r, events, result)
}

func (h *Handler) respondSessionCreationStream(w http.ResponseWriter, r *http.Request, events eventStore, result store.SessionCreation) {
	response, err := sessionResponse(result.Session, h.executorURL)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	var initial *v1.SessionEvent
	if result.Created {
		initial = &v1.SessionEvent{Type: "agent.session.created", EventID: uuid.NewString(), Session: &response}
	}
	h.serveSessionEvents(w, r, events, result.Session, result.Cursor, initial)
}
