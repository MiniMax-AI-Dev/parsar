package dev

// HTTP surface for credential_kinds.
//
// Built-in kinds (built_in=TRUE) are seeded and immutable; no update or
// delete endpoints. Deletion of user-created kinds requires a dependency
// check across capability_versions — deferred.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// createCredentialKindBody is the request shape for POST .../credential-kinds.
// Code is normalized lower-case and trimmed; uniqueness is enforced by
// uk_credential_kinds_code_active.
type createCredentialKindBody struct {
	Code        string         `json:"code"`
	DisplayName string         `json:"display_name"`
	Description string         `json:"description"`
	Source      string         `json:"source"`
	ValueSchema map[string]any `json:"value_schema"`
}

type listCredentialKindsResponse struct {
	Items []store.CredentialKindRead `json:"items"`
}

// listCredentialKinds returns every active credential_kinds row.
// Workspace-scoped auth keeps RBAC consistent with the import endpoints.
//
//	@Summary		List credential kinds
//	@Description	Returns every active credential_kinds row. Caller must be a workspace capability admin.
//	@Tags			workspaces
//	@ID				listDevCredentialKinds
//	@Produce		json
//	@Param			workspaceID	path		string							true	"Workspace UUID"
//	@Success		200			{object}	listCredentialKindsResponse		"Credential kinds"
//	@Failure		403			{object}	map[string]string				"Caller is not a workspace capability admin"
//	@Failure		500			{object}	map[string]string				"Failed to list credential kinds"
//	@Router			/api/v1/workspaces/{workspaceID}/credential-kinds [get]
func listCredentialKinds(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireWorkspaceCapabilityAdmin(w, r, runtimeStore); !ok {
			return
		}
		items, err := runtimeStore.ListCredentialKinds(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list credential kinds"})
			return
		}
		if items == nil {
			items = []store.CredentialKindRead{}
		}
		writeJSON(w, http.StatusOK, listCredentialKindsResponse{Items: items})
	}
}

// createCredentialKind inserts a new (non-built-in) credential_kinds row.
//
//	@Summary		Create a workspace credential kind
//	@Description	Inserts a new (non-built-in) credential_kinds row. Code is normalized and must be unique among active kinds.
//	@Tags			workspaces
//	@ID				createDevCredentialKind
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path		string						true	"Workspace UUID"
//	@Param			body		body		createCredentialKindBody	true	"Credential kind definition"
//	@Success		201			{object}	map[string]interface{}		"Created credential kind"
//	@Failure		400			{object}	map[string]string			"Invalid json or missing required fields"
//	@Failure		403			{object}	map[string]string			"Caller is not a workspace capability admin"
//	@Failure		409			{object}	map[string]string			"Credential kind code already exists"
//	@Failure		422			{object}	map[string]string			"Validation error"
//	@Failure		500			{object}	map[string]string			"Failed to create credential kind"
//	@Router			/api/v1/workspaces/{workspaceID}/credential-kinds [post]
func createCredentialKind(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireWorkspaceCapabilityAdmin(w, r, runtimeStore); !ok {
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		var body createCredentialKindBody
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(body.Code) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code is required"})
			return
		}
		if strings.TrimSpace(body.DisplayName) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "display_name is required"})
			return
		}
		created, err := runtimeStore.CreateCredentialKind(r.Context(), store.CreateCredentialKindInput{
			Code:        body.Code,
			DisplayName: body.DisplayName,
			Description: body.Description,
			Source:      body.Source,
			ValueSchema: body.ValueSchema,
			CreatorID:   actorID,
		})
		if err != nil {
			writeCredentialKindError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

// writeCredentialKindError maps store sentinels to HTTP statuses.
func writeCredentialKindError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrCredentialKindDuplicate):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrCredentialKindNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	default:
		// Validation errors come back as fmt.Errorf without a sentinel;
		// surface as 422 so the inline dialog can render them inline.
		msg := err.Error()
		if strings.Contains(msg, "is required") || strings.Contains(msg, "invalid") || strings.Contains(msg, "reserved") {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": msg})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
	}
}
