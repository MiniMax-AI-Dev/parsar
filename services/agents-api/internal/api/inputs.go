package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type InputSubmitter interface {
	SubmitInputs(context.Context, string, string, string, []store.Input) ([]store.InputReceipt, error)
}

type Option func(*Handler)

// WithExecution enables durable admission when the service owns an execution worker.
func WithExecution(s InputSubmitter) Option { return func(h *Handler) { h.inputs = s } }

// @Summary Submit Session input events
// @Description Atomically accepts text messages, cancellation and function results. Messages steer active work or start a queued Turn. Retry keys identify the whole ordered batch. Function output accepts text or ordered text/image parts; message images are not supported yet.
// @Tags Sessions
// @Accept json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param Idempotency-Key header string false "Retry key, up to 128 bytes"
// @Param session_id path string true "Session ID"
// @Param body body v1.CreateEventsRequest true "Ordered input events"
// @Success 204
// @Failure 400,401,404,409,413,500,503 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id}/events [post]
func (h *Handler) createEvents(w http.ResponseWriter, r *http.Request) {
	if h.inputs == nil {
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Execution is not enabled on this service.")
		return
	}
	if len(r.URL.Query()) != 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Event submission does not accept query parameters.")
		return
	}
	var request struct {
		Events []json.RawMessage `json:"events"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request exceeds 1 MiB.")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request", "Invalid Session input event request.")
		}
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request must contain exactly one JSON object.")
		return
	}
	inputs, err := executionInputs(request.Events)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		key = uuid.NewString()
	}
	if _, err := h.inputs.SubmitInputs(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), key, inputs); err != nil {
		writeStoreError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func executionInputs(events []json.RawMessage) ([]store.Input, error) {
	if len(events) == 0 || len(events) > 64 {
		return nil, store.ErrInvalidInput
	}
	inputs := make([]store.Input, 0, len(events))
	for _, raw := range events {
		event, err := decodeInputEvent(raw)
		if err != nil {
			return nil, err
		}
		switch event.Type {
		case "agent.session.input.cancel":
			if event.Input != nil {
				return nil, store.ErrInvalidInput
			}
			inputs = append(inputs, store.Input{Kind: "cancel", Payload: json.RawMessage(`{}`)})
		case "agent.session.input.tool_result":
			input, err := functionResultInput(event)
			if err != nil {
				return nil, err
			}
			inputs = append(inputs, input)
		case "agent.session.input.message":
			if len(event.Input) == 0 {
				return nil, store.ErrInvalidInput
			}
			for _, message := range event.Input {
				if message.Role != "user" || (message.Type != "" && message.Type != "message") || len(message.Content) == 0 {
					return nil, store.ErrInvalidInput
				}
				var text strings.Builder
				for _, content := range message.Content {
					if content.Type != "input_text" {
						return nil, store.ErrInvalidInput
					}
					text.WriteString(content.Text)
				}
				if strings.TrimSpace(text.String()) == "" {
					return nil, store.ErrInvalidInput
				}
			}
			payload, err := json.Marshal(event)
			if err != nil {
				return nil, err
			}
			inputs = append(inputs, store.Input{Kind: "message", Payload: payload})
		default:
			return nil, store.ErrInvalidInput
		}
	}
	return inputs, nil
}
