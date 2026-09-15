package api

import (
	"context"
	"net/http"
	"net/url"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type CredentialStore interface {
	CreateStaticCredential(context.Context, string, string, store.CreateStaticCredentialInput) (store.Credential, error)
	UpdateStaticCredential(context.Context, string, string, string, store.UpdateStaticCredentialInput) (store.Credential, error)
	GetCredential(context.Context, string, string, string) (store.Credential, error)
	ListCredentials(context.Context, string, string, string, int, bool, []string) (store.CredentialPage, error)
}

// @Summary Create a static-bearer Vault Credential
// @Description Stores the write-only token as execution-owned authenticated ciphertext. Required name is trimmed to 1–256 UTF-8 bytes; auth requires static_bearer, an HTTPS mcp_server_url and a string token. Token bytes are preserved, including empty strings; hosted token edge validation is unverified. The initial URL profile excludes userinfo and fragments, preserves queries and makes no network request. Public responses contain only safe metadata. Missing encryption configuration returns local 503. Session admission can bind static credentials from attached Vaults to exact HTTPS MCP destinations; secret decryption occurs only at dispatch. OAuth, storage-key rotation and exact hosted error/retry semantics remain gaps.
// @Tags Credentials
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param vault_id path string true "Vault ID"
// @Param body body v1.CreateCredentialRequest true "Write-only static bearer credential"
// @Success 200 {object} v1.Credential
// @Failure 400,401,404,413,500,503 {object} v1.ErrorResponse
// @Router /vaults/{vault_id}/credentials [post]
func (h *Handler) createCredential(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Credential creation does not accept query parameters.")
		return
	}
	vaultID, ok := credentialResourceID(w, r, "vault_id")
	if !ok {
		return
	}
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var request v1.CreateCredentialRequest
	if decodeInputObject(raw, &request, "name", "auth") != nil || request.Name == nil || request.Auth == nil || request.Auth.Type != "static_bearer" || request.Auth.Token == nil || request.Auth.MCPServerURL == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "name and static_bearer auth with string mcp_server_url and token are required.")
		return
	}
	name, err := normalizedVaultName(*request.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	u, err := url.Parse(*request.Auth.MCPServerURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "mcp_server_url must be an absolute HTTPS URL without userinfo or a fragment.")
		return
	}
	credential, err := h.store.CreateStaticCredential(r.Context(), tenantID(r), vaultID, store.CreateStaticCredentialInput{Name: name, MCPServerURL: *request.Auth.MCPServerURL, Token: *request.Auth.Token})
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, credentialResponse(credential))
}

// @Summary Retrieve safe Vault Credential metadata
// @Description Reads only non-secret metadata scoped to the authenticated project and owning Vault. No token decryption, network request or execution is performed. Unknown, foreign, wrong-Vault and malformed IDs use the same local not-found response; hosted error parity remains unverified.
// @Tags Credentials
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param vault_id path string true "Vault ID"
// @Param credential_id path string true "Credential ID"
// @Success 200 {object} v1.Credential
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /vaults/{vault_id}/credentials/{credential_id} [get]
func (h *Handler) getCredential(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Credential retrieval does not accept query parameters.")
		return
	}
	vaultID, ok := credentialResourceID(w, r, "vault_id")
	if !ok {
		return
	}
	id, ok := credentialResourceID(w, r, "credential_id")
	if !ok {
		return
	}
	credential, err := h.store.GetCredential(r.Context(), tenantID(r), vaultID, id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, credentialResponse(credential))
}

func credentialResourceID(w http.ResponseWriter, r *http.Request, param string) (string, bool) {
	id, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil || id == uuid.Nil {
		writeStoreError(w, r, store.ErrNotFound)
		return "", false
	}
	return id.String(), true
}

func credentialResponse(c store.Credential) v1.Credential {
	return v1.Credential{ID: c.ID, VaultID: c.VaultID, Name: c.Name, Object: "vault.credential", Auth: v1.StaticBearerCredentialAuth{Type: c.AuthType, MCPServerURL: c.MCPServerURL}, CreatedAt: c.CreatedAt.Unix(), UpdatedAt: c.UpdatedAt.Unix()}
}
