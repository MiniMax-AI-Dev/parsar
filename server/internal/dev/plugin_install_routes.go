package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type installPluginBody struct {
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Visibility    string          `json:"visibility"`
	Version       string          `json:"version"`
	CanonicalSpec json.RawMessage `json:"canonical_spec"`
}

// installPlugin handles POST /api/v1/workspaces/{workspaceID}/capabilities/plugins/install.
// Creates a KindBundle capability + version from the provided canonical_spec.
// The CLI (`parsar plugin add`) reads a local plugin directory, builds the
// canonical_spec with inline skill content, and POSTs it here.
//
//	@Summary		Install a plugin bundle
//	@Description	Creates a KindBundle capability and its first version. The canonical_spec must have kind=bundle with inline skills embedded. Owner/admin only.
//	@Tags			capabilities
//	@ID				installPluginBundle
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string				true	"Workspace UUID"
//	@Param			body		body	installPluginBody	true	"Plugin install payload"
//	@Success		201 {object} map[string]interface{} "Created capability and version"
//	@Failure		400 {object} map[string]string "Missing name/version, invalid canonical_spec"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/capabilities/plugins/install [post]
func installPlugin(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := requireWorkspaceCapabilityAdmin(w, r, runtimeStore)
		if !ok {
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		var body installPluginBody
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		if strings.TrimSpace(body.Version) == "" {
			body.Version = "1.0.0"
		}

		// Decode and validate the canonical_spec.
		if len(body.CanonicalSpec) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "canonical_spec is required"})
			return
		}
		var spec canonical.Spec
		if err := json.Unmarshal(body.CanonicalSpec, &spec); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("canonical_spec decode: %s", err)})
			return
		}
		if spec.Kind != canonical.KindBundle {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("canonical_spec.kind must be \"bundle\", got %q", spec.Kind)})
			return
		}
		if err := spec.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("canonical_spec validation: %s", err)})
			return
		}

		visibility := strings.TrimSpace(body.Visibility)
		if visibility == "" {
			visibility = "workspace"
		}

		sourcePayload := json.RawMessage(`{}`)

		result, err := runtimeStore.ImportCapability(r.Context(), store.ImportCapabilityInput{
			WorkspaceID:   workspaceID,
			Name:          strings.TrimSpace(body.Name),
			Description:   strings.TrimSpace(body.Description),
			Visibility:    visibility,
			Type:          "bundle",
			CreatorID:     actorID,
			Version:       strings.TrimSpace(body.Version),
			SourcePayload: sourcePayload,
			Spec:          spec,
		})
		if err != nil {
			if errors.Is(err, store.ErrCapabilityNameTaken) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "a plugin with this name already exists in the workspace"})
				return
			}
			writeCapabilityError(w, err, "failed to install plugin")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"id":                  result.Capability.ID,
			"name":                result.Capability.Name,
			"type":                result.Capability.Type,
			"capability_version":  result.CapabilityVersion.ID,
			"version":             result.CapabilityVersion.Version,
		})
	}
}
