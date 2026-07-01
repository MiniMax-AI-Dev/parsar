package dev

import (
	"errors"
	"net/http"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func writeRBACError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
	case errors.Is(err, auth.ErrForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
	case errors.Is(err, auth.ErrNotMember):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not a member"})
	case errors.Is(err, store.ErrUnknownWorkspace):
		// Surfaces when the workspace id is invalid or soft-deleted;
		// same 404 shape as ErrNotMember
		// so existence isn't leaked.
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not a member"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check role"})
	}
}
