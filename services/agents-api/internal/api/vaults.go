package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type VaultStore interface {
	CreateVault(context.Context, string, store.CreateVaultInput) (store.Vault, error)
	GetVault(context.Context, string, string) (store.Vault, error)
	ListVaults(context.Context, string, string, int, bool, []string) (store.VaultPage, error)
}

// @Summary Create a Vault
// @Description Creates a project-owned Vault independently of execution. Omitted name stays null; a supplied string is trimmed and must contain 1–256 UTF-8 bytes. Explicit null name is invalid. Omitted/null metadata becomes an empty object; values must be strings. Metadata has a local 64 KiB encoded storage bound. Credentials, Session binding and hosted error/retry parity remain incomplete.
// @Tags Vaults
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param body body v1.CreateVaultRequest true "Vault name and metadata"
// @Success 200 {object} v1.Vault
// @Failure 400,401,413,500 {object} v1.ErrorResponse
// @Router /vaults [post]
func (h *Handler) createVault(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Vault creation does not accept query parameters.")
		return
	}
	raw, ok := readJSONBody(w, r)
	if !ok {
		return
	}
	var request struct {
		Name     json.RawMessage    `json:"name"`
		Metadata map[string]*string `json:"metadata"`
	}
	if decodeInputObject(raw, &request, "name", "metadata") != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Request must be a JSON object containing supported fields.")
		return
	}
	input := store.CreateVaultInput{}
	if len(request.Name) > 0 {
		var name *string
		if json.Unmarshal(request.Name, &name) != nil || name == nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "name must be a string.")
			return
		}
		trimmed, err := normalizedVaultName(*name)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		input.Name = &trimmed
	}
	var err error
	input.Metadata, err = stringMetadata(request.Metadata)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "metadata values must be strings.")
		return
	}
	vault, err := h.store.CreateVault(r.Context(), tenantID(r), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, vaultResponse(vault))
}

// @Summary Retrieve a Vault
// @Description Reads a Vault owned by the authenticated project without resolving credentials, Sessions or execution devices. Missing and foreign IDs share the same not-found response; exact hosted error semantics remain unverified.
// @Tags Vaults
// @Produce json
// @Security BearerAuth
// @Param OpenAI-Beta header string true "agents=v1"
// @Param vault_id path string true "Vault ID"
// @Success 200 {object} v1.Vault
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Router /vaults/{vault_id} [get]
func (h *Handler) getVault(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) > 0 {
		writeError(w, http.StatusBadRequest, "unsupported_parameter", "Vault retrieval does not accept query parameters.")
		return
	}
	id := chi.URLParam(r, "vault_id")
	if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
		writeStoreError(w, r, store.ErrNotFound)
		return
	}
	vault, err := h.store.GetVault(r.Context(), tenantID(r), id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, vaultResponse(vault))
}

func vaultResponse(vault store.Vault) v1.Vault {
	return v1.Vault{ID: vault.ID, Object: "vault", CreatedAt: vault.CreatedAt.Unix(), Name: vault.Name, Metadata: vault.Metadata}
}

func normalizedVaultName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if len(name) == 0 || len(name) > 256 {
		return "", errors.New("name must contain 1 to 256 UTF-8 bytes after trimming.")
	}
	return name, nil
}
