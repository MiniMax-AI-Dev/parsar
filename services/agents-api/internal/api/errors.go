package api

import (
	"encoding/json"
	"errors"
	"net/http"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	kind := "invalid_request_error"
	if status >= 500 {
		kind = "server_error"
	} else if status == http.StatusUnauthorized {
		kind = "authentication_error"
	}
	writeJSON(w, status, v1.ErrorResponse{Error: v1.APIError{Message: message, Type: kind, Code: code}})
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Resource not found.")
	case errors.Is(err, store.ErrTurnConflict):
		writeError(w, http.StatusConflict, "turn_conflict", "The Turn cannot accept this input in its current state.")
	case errors.Is(err, store.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict", "This idempotency key was used with different input.")
	case errors.Is(err, store.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid resource identifier or request limits.")
	default:
		// Driver errors can include submitted values; do not log the raw error.
		log.Ctx(r.Context()).Error("agents-api persistence operation failed")
		writeError(w, http.StatusInternalServerError, "internal_error", "The operation could not be completed.")
	}
}
