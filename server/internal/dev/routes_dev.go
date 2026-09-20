package dev

import (
	"encoding/json"

	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func getSeed(w http.ResponseWriter, r *http.Request) {
	// SeedData is the human-readable fixture (back-compat with
	// existing dev consumers). The `db` key carries the real DB UUIDs
	// used by the development database so the admin frontend can auto-bind.
	seed := DefaultSeed()
	ids := store.DefaultDevFixtureIDs()
	writeJSON(w, http.StatusOK, map[string]any{
		"workspace":     seed.Workspace,
		"users":         seed.Users,
		"agents":        seed.Agents,
		"conversations": seed.Conversations,
		// Deterministic DB UUIDs from store.DefaultDevFixtureIDs —
		// match exactly what `make seed-dev-db` inserts.
		"db": map[string]any{
			"workspace_id":    ids.WorkspaceID,
			"user_id":         ids.UserID,
			"conversation_id": ids.ConversationID,
			"agents": map[string]string{
				"product_agent_id": ids.ProductAgentID,
				"backend_agent_id": ids.BackendAgentID,
				"test_agent_id":    ids.TestAgentID,
			},
		},
	})
}

type verifyRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

// verifyDevAuth is POST /dev/auth/verify. Dev-only fake login: accepts a
// {email, code} body where code must equal DevVerificationCode, then hands
// back a dev bearer token + user shape mirroring the real auth flow so
// smoke tests can bypass Feishu OIDC entirely.
//
//	@Summary		Dev-only email + code login
//	@Description	Development-only login. Verifies the fixed dev code and returns a bearer token plus the default seed workspace + user. Never enable in production.
//	@Tags			dev
//	@ID				verifyDevAuth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		map[string]string		true	"{email, code}"
//	@Success		200		{object}	map[string]interface{}	"Bearer token + user + workspace"
//	@Failure		400		{object}	map[string]string		"Invalid json"
//	@Failure		401		{object}	map[string]string		"Invalid dev credentials"
//	@Router			/dev/auth/verify [post]
func verifyDevAuth(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if strings.TrimSpace(req.Email) == "" || req.Code != DevVerificationCode {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid dev credentials"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":        "dev-token",
		"token_type":   "Bearer",
		"workspace_id": DefaultSeed().Workspace.ID,
		"user": map[string]string{
			"id":    "dev_admin",
			"email": req.Email,
			"name":  "Dev Admin",
		},
	})
}
