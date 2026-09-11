package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type eventStore interface {
	SessionEventCursor(context.Context, string, string) (int64, error)
	ListSessionEvents(context.Context, string, string, int64) ([]store.SessionChange, error)
}

// @Summary Stream live Session events
// @Description Live-only events. Reconnect through Session, Turn and Items reads; missed events are not replayed. A lagging stream closes with an error when its bounded buffer is exceeded.
// @Tags Events
// @Produce text/event-stream
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Success 200 {object} v1.SessionEvent
// @Failure 400,401,404,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/events [get]
func (h *Handler) streamEvents(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Live streams do not accept query parameters.")
		return
	}
	events, ok := h.store.(eventStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "stream_unavailable", "Live events are unavailable.")
		return
	}
	id, tenant := chi.URLParam(r, "session_id"), tenantID(r)
	session, err := h.store.GetSession(r.Context(), tenant, id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if _, err = sessionResponse(session); err != nil {
		writeStoreError(w, r, err)
		return
	}
	cursor, err := events.SessionEventCursor(r.Context(), tenant, id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	write := func(data []byte) error {
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		return controller.Flush()
	}
	if err := write([]byte(": connected\n\n")); err != nil {
		return
	}
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		changes, err := events.ListSessionEvents(r.Context(), tenant, id, cursor)
		if err != nil {
			writeStreamFailure(write, id)
			return
		}
		for _, change := range changes {
			event, err := streamResponse(session, change)
			if err != nil {
				writeStreamFailure(write, id)
				return
			}
			payload, err := json.Marshal(event)
			if err != nil {
				writeStreamFailure(write, id)
				return
			}
			if err := write([]byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event.Type, payload))); err != nil {
				return
			}
			cursor = change.Sequence
		}
		if len(changes) > 0 {
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
		case <-heartbeat.C:
			if err := write([]byte(": keepalive\n\n")); err != nil {
				return
			}
		}
	}
}

func streamResponse(session store.Session, change store.SessionChange) (v1.SessionEvent, error) {
	event := change.Event
	if change.Turn == nil {
		return event, nil
	}
	if strings.HasPrefix(event.Type, "agent.session.turn.") {
		turn, err := turnResponse(session, *change.Turn)
		event.Turn = &turn
		return event, err
	}
	session.LastTurn, session.Usage = change.Turn, change.SessionUsage
	value, err := sessionResponse(session)
	event.Session = &value
	return event, err
}

func writeStreamFailure(write func([]byte) error, session string) {
	event := v1.SessionEvent{Type: "error", EventID: uuid.NewString(), SessionID: session,
		Error: &v1.StreamError{Code: "stream_interrupted", Type: "server_error", Message: "The live stream was interrupted. Reconnect and retrieve the Session and its saved Items to recover."}}
	payload, _ := json.Marshal(event)
	_ = write([]byte(fmt.Sprintf("event: error\ndata: %s\n\n", payload)))
}
