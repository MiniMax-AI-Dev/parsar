// Package skilldirectory exposes the reviewed, repository-backed Skill
// Directory. Directory entries are imported as ordinary workspace Skill
// capabilities; this package does not create a second capability model.
package skilldirectory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/skillcatalog"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/blob"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type catalogLoader interface {
	Load() (skillcatalog.Catalog, error)
	LoadItem(id string) (skillcatalog.Item, skillcatalog.Package, error)
}

type directoryStore interface {
	auth.RoleStore
	ListCapabilityDirectoryInstalls(ctx context.Context, workspaceID, capabilityType, sourceFormat string) ([]store.CapabilityDirectoryInstall, error)
	ImportCapability(ctx context.Context, input store.ImportCapabilityInput) (store.ImportCapabilityResult, error)
}

type itemResponse struct {
	ID                    string                 `json:"id"`
	Name                  string                 `json:"name"`
	Description           string                 `json:"description"`
	Publisher             skillcatalog.Publisher `json:"publisher"`
	IconURL               string                 `json:"icon_url,omitempty"`
	HomepageURL           string                 `json:"homepage_url,omitempty"`
	RepositoryURL         string                 `json:"repository_url,omitempty"`
	Verified              bool                   `json:"verified"`
	Categories            []string               `json:"categories"`
	FeaturedRank          int                    `json:"featured_rank"`
	Version               string                 `json:"version"`
	License               string                 `json:"license"`
	SourceRef             string                 `json:"source_ref,omitempty"`
	SourcePath            string                 `json:"source_path,omitempty"`
	Slug                  string                 `json:"slug,omitempty"`
	Title                 string                 `json:"title,omitempty"`
	Instruction           string                 `json:"instruction,omitempty"`
	Trigger               string                 `json:"trigger,omitempty"`
	Files                 []canonical.SkillFile  `json:"files,omitempty"`
	Installed             bool                   `json:"installed"`
	InstalledCapabilityID *string                `json:"installed_capability_id"`
}

type listResponse struct {
	Items []itemResponse `json:"items"`
}

type importResponse struct {
	Installed    bool   `json:"installed"`
	CapabilityID string `json:"capability_id"`
}

type errorResponse struct {
	Error string `json:"error"`
}

type sourcePayload struct {
	SourceFormat   string `json:"source_format"`
	CatalogID      string `json:"catalog_id"`
	CatalogVersion string `json:"catalog_version"`
}

const (
	sourceFormat   = "skill_catalog"
	capabilityType = "skill"
)

type Deps struct {
	Catalog catalogLoader
	Store   directoryStore
	Blobs   blob.Store
}

type handler struct {
	deps Deps
}

func RegisterRoutes(r chi.Router, deps Deps) {
	h := &handler{deps: deps}
	r.Get("/api/v1/workspaces/{workspaceID}/skill-directory", h.list)
	r.Get("/api/v1/workspaces/{workspaceID}/skill-directory/{catalogID}", h.get)
	r.Post("/api/v1/workspaces/{workspaceID}/skill-directory/{catalogID}/import", h.importItem)
}

// list godoc
//
//	@Summary	List Skill Directory items
//	@Tags		skill-directory
//	@Produce	json
//	@Param		workspaceID path string true "workspace id"
//	@Success	200 {object} listResponse
//	@Failure	400 {object} errorResponse
//	@Failure	401 {object} errorResponse
//	@Failure	403 {object} errorResponse
//	@Failure	503 {object} errorResponse
//	@Router		/api/v1/workspaces/{workspaceID}/skill-directory [get]
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.authorize(w, r, "owner", "admin", "member", "viewer")
	if !ok {
		return
	}
	catalog, installs, ok := h.load(w, r, workspaceID)
	if !ok {
		return
	}
	byCatalog := installMap(installs)
	items := make([]itemResponse, 0, len(catalog.Items))
	for _, item := range catalog.Items {
		items = append(items, summarizeItem(item, byCatalog[item.ID]))
	}
	writeJSON(w, http.StatusOK, listResponse{Items: items})
}

// get godoc
//
//	@Summary	Get a Skill Directory item
//	@Tags		skill-directory
//	@Produce	json
//	@Param		workspaceID path string true "workspace id"
//	@Param		catalogID path string true "skill catalog item id"
//	@Success	200 {object} itemResponse
//	@Failure	400 {object} errorResponse
//	@Failure	403 {object} errorResponse
//	@Failure	404 {object} errorResponse
//	@Failure	503 {object} errorResponse
//	@Router		/api/v1/workspaces/{workspaceID}/skill-directory/{catalogID} [get]
func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.authorize(w, r, "owner", "admin", "member", "viewer")
	if !ok {
		return
	}
	installs, ok := h.loadInstalls(w, r, workspaceID)
	if !ok {
		return
	}
	item, pkg, err := h.loadItem(chi.URLParam(r, "catalogID"))
	if err != nil {
		h.writeCatalogError(w, err)
		return
	}
	response := summarizeItem(item, installMap(installs)[item.ID])
	if pkg.Spec.Skill != nil {
		response.Slug = pkg.Spec.Skill.Slug
		response.Title = pkg.Spec.Skill.Title
		response.Instruction = pkg.Spec.Skill.Instruction
		response.Trigger = pkg.Spec.Skill.Trigger
		response.Files = append([]canonical.SkillFile(nil), pkg.Spec.Skill.Files...)
	}
	writeJSON(w, http.StatusOK, response)
}

// importItem godoc
//
//	@Summary	Import a Skill Directory item
//	@Description	Stores the reviewed Skill package as a private workspace Skill capability. The package is not executed or bound to an Agent.
//	@Tags		skill-directory
//	@Produce	json
//	@Param		workspaceID path string true "workspace id"
//	@Param		catalogID path string true "skill catalog item id"
//	@Success	200 {object} importResponse "already installed"
//	@Success	201 {object} importResponse "imported"
//	@Failure	400 {object} errorResponse
//	@Failure	403 {object} errorResponse
//	@Failure	404 {object} errorResponse
//	@Failure	409 {object} errorResponse
//	@Failure	503 {object} errorResponse
//	@Router		/api/v1/workspaces/{workspaceID}/skill-directory/{catalogID}/import [post]
func (h *handler) importItem(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.authorize(w, r, "owner", "admin", "member")
	if !ok {
		return
	}
	installs, ok := h.loadInstalls(w, r, workspaceID)
	if !ok {
		return
	}
	catalogID := chi.URLParam(r, "catalogID")
	item, pkg, err := h.loadItem(catalogID)
	if err != nil {
		h.writeCatalogError(w, err)
		return
	}
	if existing, installed := installMap(installs)[catalogID]; installed {
		writeJSON(w, http.StatusOK, importResponse{Installed: true, CapabilityID: existing.CapabilityID})
		return
	}
	if h.deps.Blobs == nil {
		writeError(w, http.StatusServiceUnavailable, "skill_directory_storage_unavailable")
		return
	}
	ref, err := h.deps.Blobs.NewRef("skill", workspaceID, item.ID+".zip")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "skill_package_reference_failed")
		return
	}
	if err := h.deps.Blobs.PutBytes(r.Context(), ref, workspaceID, pkg.Zip); err != nil {
		writeError(w, http.StatusInternalServerError, "skill_package_storage_failed")
		return
	}
	payload, err := json.Marshal(sourcePayload{
		SourceFormat:   sourceFormat,
		CatalogID:      item.ID,
		CatalogVersion: item.Version,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "skill_catalog_provenance_failed")
		return
	}
	result, err := h.deps.Store.ImportCapability(r.Context(), store.ImportCapabilityInput{
		WorkspaceID:   workspaceID,
		Name:          item.Name,
		Description:   item.Description,
		Visibility:    "workspace",
		Type:          capabilityType,
		CreatorID:     auth.UserIDFromContext(r.Context()),
		Version:       item.Version,
		SourcePayload: payload,
		Spec:          pkg.Spec,
		OssKey:        ref,
		SHA256:        pkg.SHA256,
	})
	if err != nil {
		if errors.Is(err, store.ErrCapabilityNameTaken) {
			if current, listErr := h.deps.Store.ListCapabilityDirectoryInstalls(r.Context(), workspaceID, capabilityType, sourceFormat); listErr == nil {
				if existing, installed := installMap(current)[item.ID]; installed {
					writeJSON(w, http.StatusOK, importResponse{Installed: true, CapabilityID: existing.CapabilityID})
					return
				}
			}
			writeError(w, http.StatusConflict, "capability_name_conflict")
			return
		}
		writeError(w, http.StatusInternalServerError, "skill_import_failed")
		return
	}
	writeJSON(w, http.StatusCreated, importResponse{Installed: true, CapabilityID: result.Capability.ID})
}

func (h *handler) authorize(w http.ResponseWriter, r *http.Request, allowed ...string) (string, bool) {
	if h.deps.Catalog == nil || h.deps.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "skill_directory_unavailable")
		return "", false
	}
	workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
	if _, err := uuid.Parse(workspaceID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_workspace_id")
		return "", false
	}
	if err := auth.RequireWorkspaceRole(r.Context(), h.deps.Store, workspaceID, allowed...); err != nil {
		switch {
		case errors.Is(err, auth.ErrUnauthenticated):
			writeError(w, http.StatusUnauthorized, "unauthenticated")
		case errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrNotMember):
			writeError(w, http.StatusForbidden, "forbidden")
		default:
			writeError(w, http.StatusInternalServerError, "workspace_authorization_failed")
		}
		return "", false
	}
	return workspaceID, true
}

func (h *handler) load(w http.ResponseWriter, r *http.Request, workspaceID string) (skillcatalog.Catalog, []store.CapabilityDirectoryInstall, bool) {
	catalog, err := h.deps.Catalog.Load()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "skill_catalog_unavailable")
		return skillcatalog.Catalog{}, nil, false
	}
	installs, ok := h.loadInstalls(w, r, workspaceID)
	if !ok {
		return skillcatalog.Catalog{}, nil, false
	}
	return catalog, installs, true
}

func (h *handler) loadInstalls(w http.ResponseWriter, r *http.Request, workspaceID string) ([]store.CapabilityDirectoryInstall, bool) {
	installs, err := h.deps.Store.ListCapabilityDirectoryInstalls(r.Context(), workspaceID, capabilityType, sourceFormat)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "skill_directory_install_state_failed")
		return nil, false
	}
	return installs, true
}

func (h *handler) loadItem(id string) (skillcatalog.Item, skillcatalog.Package, error) {
	return h.deps.Catalog.LoadItem(strings.TrimSpace(id))
}

func (h *handler) writeCatalogError(w http.ResponseWriter, err error) {
	if errors.Is(err, skillcatalog.ErrItemNotFound) {
		writeError(w, http.StatusNotFound, "skill_not_found")
		return
	}
	writeError(w, http.StatusServiceUnavailable, "skill_catalog_item_unavailable")
}

func installMap(installs []store.CapabilityDirectoryInstall) map[string]store.CapabilityDirectoryInstall {
	result := make(map[string]store.CapabilityDirectoryInstall, len(installs))
	for _, install := range installs {
		result[install.CatalogID] = install
	}
	return result
}

func summarizeItem(item skillcatalog.Item, install store.CapabilityDirectoryInstall) itemResponse {
	var installedCapabilityID *string
	if install.CapabilityID != "" {
		id := install.CapabilityID
		installedCapabilityID = &id
	}
	return itemResponse{
		ID:                    item.ID,
		Name:                  item.Name,
		Description:           item.Description,
		Publisher:             item.Publisher,
		IconURL:               item.IconURL,
		HomepageURL:           item.HomepageURL,
		RepositoryURL:         item.RepositoryURL,
		Verified:              item.Verified,
		Categories:            append([]string(nil), item.Categories...),
		FeaturedRank:          item.FeaturedRank,
		Version:               item.Version,
		License:               item.License,
		SourceRef:             item.SourceRef,
		SourcePath:            item.SourcePath,
		Installed:             install.CapabilityID != "",
		InstalledCapabilityID: installedCapabilityID,
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, errorResponse{Error: code})
}
