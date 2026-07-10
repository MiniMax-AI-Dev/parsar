package dev

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

const (
	AuthProviderTypePassword = "password"
	AuthProviderTypeOAuth    = "oauth"
	AuthProviderTypeOIDC     = "oidc"
	AuthProviderTypeSAML     = "saml"
)

type AuthProvider struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Label       string   `json:"label"`
	Enabled     bool     `json:"enabled"`
	LoginURL    string   `json:"login_url,omitempty"`
	Configured  bool     `json:"configured"`
	CallbackURL string   `json:"callback_url,omitempty"`
	RequiredEnv []string `json:"required_env,omitempty"`
	MissingEnv  []string `json:"missing_env,omitempty"`
	DocsURL     string   `json:"docs_url,omitempty"`
}

type AuthProviderRegistry struct {
	Providers []AuthProvider
}

type publicAuthProvider struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Label    string `json:"label"`
	Enabled  bool   `json:"enabled"`
	LoginURL string `json:"login_url,omitempty"`
}

type authProvidersResponse struct {
	Providers []publicAuthProvider `json:"providers"`
}

type workspaceAuthProvidersResponse struct {
	WorkspaceID string         `json:"workspace_id"`
	Providers   []AuthProvider `json:"providers"`
}

func (r AuthProviderRegistry) publicProviders() []publicAuthProvider {
	providers := r.providersOrDefault()
	out := make([]publicAuthProvider, 0, len(providers))
	for _, p := range providers {
		entry := publicAuthProvider{
			ID:      p.ID,
			Type:    p.Type,
			Label:   p.Label,
			Enabled: p.Enabled,
		}
		if p.Enabled {
			entry.LoginURL = p.LoginURL
		}
		out = append(out, entry)
	}
	return out
}

func (r AuthProviderRegistry) providersOrDefault() []AuthProvider {
	if len(r.Providers) > 0 {
		return r.Providers
	}
	return []AuthProvider{{
		ID:         "password",
		Type:       AuthProviderTypePassword,
		Label:      "Email password",
		Enabled:    true,
		Configured: true,
		LoginURL:   "/login",
	}}
}

// listAuthProviders returns login methods visible before authentication.
//
//	@Summary		List auth providers
//	@Description	Returns the login providers the public login page may render. Sensitive diagnostics are omitted.
//	@Tags			auth
//	@ID				listAuthProviders
//	@Produce		json
//	@Success		200 {object} authProvidersResponse "Auth providers"
//	@Router			/api/v1/auth/providers [get]
func listAuthProviders(registry AuthProviderRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, authProvidersResponse{Providers: registry.publicProviders()})
	}
}

// listWorkspaceAuthProviders returns admin-visible auth provider diagnostics.
//
//	@Summary		List workspace auth provider diagnostics
//	@Description	Returns read-only auth provider status for workspace owners/admins. Secret values are never returned.
//	@Tags			auth
//	@ID				listWorkspaceAuthProviders
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Success		200 {object} workspaceAuthProvidersResponse "Auth provider diagnostics"
//	@Failure		400 {object} map[string]string "workspace_id must be a valid uuid"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/auth/providers [get]
func listWorkspaceAuthProviders(runtimeStore RuntimeStore, registry AuthProviderRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, workspaceAuthProvidersResponse{
			WorkspaceID: workspaceID,
			Providers:   registry.providersOrDefault(),
		})
	}
}
