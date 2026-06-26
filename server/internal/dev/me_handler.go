package dev

import (
	"errors"
	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type meResponse struct {
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

func meHandler(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		userID := strings.TrimSpace(auth.UserIDFromContext(r.Context()))
		if userID == "" {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "authenticated user missing from request context"})
			return
		}
		if !isUUID(userID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id must be a valid uuid"})
			return
		}
		user, err := runtimeStore.GetUserByID(r.Context(), userID)
		if err != nil {
			if errors.Is(err, store.ErrUnknownUser) {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "resolved user does not exist"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to resolve current user"})
			return
		}
		writeJSON(w, http.StatusOK, meResponse{
			UserID:    user.ID,
			Email:     user.Email,
			Name:      user.Name,
			AvatarURL: user.AvatarURL,
		})
	}
}
