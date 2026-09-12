package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
)

// @Summary Update execution Session metadata
// @Description Omit metadata to leave it unchanged, send null or {} to clear it, or supply an object to replace all pairs. Up to 16 string pairs, with keys at most 64 characters and values at most 512 characters. Execution configuration and activity are unchanged.
// @Tags Sessions
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param session_id path string true "Session ID"
// @Param body body v1.UpdateSessionRequest true "Session metadata"
// @Success 200 {object} v1.Session
// @Failure 400,401,404,413,500 {object} v1.ErrorResponse
// @Router /agents/sessions/{session_id} [post]
func (h *Handler) updateSession(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Session updates do not accept query parameters.")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024*1024))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large", "Request exceeds 1 MiB.")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request", "Request must contain one JSON object.")
		}
		return
	}
	var request struct {
		Metadata json.RawMessage `json:"metadata"`
	}
	if err := decodeInputObject(raw, &request, "metadata"); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request must be a JSON object containing supported fields.")
		return
	}
	var session store.Session
	if len(request.Metadata) == 0 {
		session, err = h.store.GetSession(r.Context(), tenantID(r), chi.URLParam(r, "session_id"))
	} else {
		var values map[string]*string
		if err := json.Unmarshal(request.Metadata, &values); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "metadata must be null or an object with string values.")
			return
		}
		var metadata map[string]string
		metadata, err = stringMetadata(values)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "metadata values must be strings.")
			return
		}
		if err := validateMetadata(metadata); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		session, err = h.store.UpdateSessionMetadata(r.Context(), tenantID(r), chi.URLParam(r, "session_id"), metadata)
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.respondSession(w, r, session)
}

func stringMetadata(values map[string]*string) (map[string]string, error) {
	metadata := make(map[string]string, len(values))
	for key, value := range values {
		if value == nil {
			return nil, store.ErrInvalidInput
		}
		metadata[key] = *value
	}
	return metadata, nil
}

func validateMetadata(metadata map[string]string) error {
	if len(metadata) > 16 {
		return errors.New("metadata supports at most 16 pairs.")
	}
	for key, value := range metadata {
		if utf8.RuneCountInString(key) > 64 || utf8.RuneCountInString(value) > 512 {
			return errors.New("metadata keys must be at most 64 characters and values at most 512 characters.")
		}
	}
	return nil
}
