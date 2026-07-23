// Package mcpdirectory exposes the repository-backed MCP Connector Directory.
// Directory items are imported as ordinary workspace MCP capabilities; this
// package does not execute servers or create agent bindings.
package mcpdirectory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth/mcpoauth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/mcpcatalog"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type catalogLoader interface {
	Load(ctx context.Context) (mcpcatalog.Snapshot, error)
}

type directoryStore interface {
	auth.RoleStore
	ListMCPDirectoryInstalls(ctx context.Context, workspaceID string) ([]store.MCPDirectoryInstall, error)
	ImportCapability(ctx context.Context, input store.ImportCapabilityInput) (store.ImportCapabilityResult, error)
}

type workspaceCredentialStore interface {
	ListSecrets(ctx context.Context, workspaceID string, limit int32) ([]store.SecretRead, error)
	CreateSecret(ctx context.Context, input store.CreateSecretInput, encryptedPayload []byte) (store.SecretRead, error)
	GetSecretPayload(ctx context.Context, workspaceID, secretID string) (store.SecretPayload, error)
	UpdateSecretPayload(ctx context.Context, workspaceID, secretID string, encryptedPayload []byte) (store.SecretPayload, error)
}

type Deps struct {
	Catalog              catalogLoader
	Store                directoryStore
	WorkspaceCredentials workspaceCredentialStore
	OAuth                *mcpoauth.Client
	Secrets              *secrets.Service
	PublicURL            string
	CookieSecure         bool
}

type handler struct {
	deps Deps
}

type itemResponse struct {
	ID                    string               `json:"id"`
	Name                  string               `json:"name"`
	Description           string               `json:"description"`
	Publisher             mcpcatalog.Publisher `json:"publisher"`
	IconURL               string               `json:"icon_url,omitempty"`
	HomepageURL           string               `json:"homepage_url,omitempty"`
	RepositoryURL         string               `json:"repository_url,omitempty"`
	Verified              bool                 `json:"verified"`
	Categories            []string             `json:"categories"`
	FeaturedRank          int                  `json:"featured_rank"`
	Version               string               `json:"version"`
	Transport             string               `json:"transport"`
	Authentication        string               `json:"authentication"`
	CredentialKind        string               `json:"credential_kind,omitempty"`
	ConnectionSupported   bool                 `json:"connection_supported"`
	Connected             bool                 `json:"connected"`
	ConnectionStatus      string               `json:"connection_status,omitempty"`
	ConnectionCheckedAt   *time.Time           `json:"connection_checked_at,omitempty"`
	ConnectionError       string               `json:"connection_error,omitempty"`
	ConnectionProtocol    string               `json:"connection_protocol_version,omitempty"`
	ConnectionServerName  string               `json:"connection_server_name,omitempty"`
	ConnectionServerVer   string               `json:"connection_server_version,omitempty"`
	ConnectionToolCount   *int                 `json:"connection_tool_count,omitempty"`
	URL                   string               `json:"url,omitempty"`
	Command               string               `json:"command,omitempty"`
	Args                  []string             `json:"args,omitempty"`
	Env                   []string             `json:"env,omitempty"`
	StartupTimeoutSec     int                  `json:"startup_timeout_sec,omitempty"`
	Installed             bool                 `json:"installed"`
	InstalledCapabilityID *string              `json:"installed_capability_id"`
}

type listResponse struct {
	Items     []itemResponse `json:"items"`
	UpdatedAt string         `json:"updated_at"`
	Source    string         `json:"source"`
}

type importResponse struct {
	Installed    bool                  `json:"installed"`
	CapabilityID string                `json:"capability_id"`
	Created      bool                  `json:"created"`
	Capability   *store.CapabilityRead `json:"capability,omitempty"`
}

type sourcePayload struct {
	SourceFormat   string `json:"source_format"`
	CatalogID      string `json:"catalog_id"`
	CatalogVersion string `json:"catalog_version"`
	CatalogSource  string `json:"catalog_source"`
}

func RegisterRoutes(r chi.Router, deps Deps) {
	h := &handler{deps: deps}
	r.Get("/api/v1/workspaces/{workspaceID}/mcp-directory", h.list)
	r.Get("/api/v1/workspaces/{workspaceID}/mcp-directory/{catalogID}", h.get)
	r.Post("/api/v1/workspaces/{workspaceID}/mcp-directory/{catalogID}/import", h.importItem)
	r.Get("/api/v1/workspaces/{workspaceID}/mcp-directory/{catalogID}/oauth/start", h.oauthStart)
	r.Get("/api/v1/workspaces/{workspaceID}/mcp-directory/{catalogID}/oauth/callback", h.oauthCallback)
	r.Post("/api/v1/workspaces/{workspaceID}/mcp-directory/{catalogID}/oauth/test", h.oauthTest)
}

// list godoc
//
//	@Summary	List MCP Connector Directory items
//	@Tags		mcp-directory
//	@Produce	json
//	@Param		workspaceID path string true "workspace id"
//	@Success	200 {object} listResponse
//	@Failure	400 {object} map[string]string
//	@Failure	401 {object} map[string]string
//	@Failure	403 {object} map[string]string
//	@Failure	503 {object} map[string]string
//	@Router		/api/v1/workspaces/{workspaceID}/mcp-directory [get]
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.authorize(w, r, false)
	if !ok {
		return
	}
	snapshot, installs, ok := h.load(w, r, workspaceID)
	if !ok {
		return
	}
	byCatalog := installMap(installs)
	connections, ok := h.connectionStates(w, r, workspaceID)
	if !ok {
		return
	}
	items := make([]itemResponse, 0, len(snapshot.Catalog.Items))
	for _, item := range snapshot.Catalog.Items {
		items = append(items, summarizeItem(item, byCatalog[item.ID], connections))
	}
	writeJSON(w, http.StatusOK, listResponse{
		Items:     items,
		UpdatedAt: snapshot.Catalog.UpdatedAt,
		Source:    string(snapshot.Source),
	})
}

// get godoc
//
//	@Summary	Get an MCP Connector Directory item
//	@Tags		mcp-directory
//	@Produce	json
//	@Param		workspaceID path string true "workspace id"
//	@Param		catalogID path string true "catalog item id"
//	@Success	200 {object} itemResponse
//	@Failure	400 {object} map[string]string
//	@Failure	404 {object} map[string]string
//	@Router		/api/v1/workspaces/{workspaceID}/mcp-directory/{catalogID} [get]
func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.authorize(w, r, false)
	if !ok {
		return
	}
	snapshot, installs, ok := h.load(w, r, workspaceID)
	if !ok {
		return
	}
	item, found := snapshot.Find(chi.URLParam(r, "catalogID"))
	if !found {
		writeError(w, http.StatusNotFound, "connector_not_found")
		return
	}
	connections, ok := h.connectionStates(w, r, workspaceID)
	if !ok {
		return
	}
	response := summarizeItem(item, installMap(installs)[item.ID], connections)
	response.URL = item.Server.URL
	response.Command = item.Server.Command
	response.Args = append([]string(nil), item.Server.Args...)
	response.Env = sortedEnvNames(item.Server.Env)
	response.StartupTimeoutSec = item.Server.StartupTimeoutSec
	writeJSON(w, http.StatusOK, response)
}

// importItem godoc
//
//	@Summary	Import an MCP Connector Directory item
//	@Description	Saves the catalog entry as a private workspace MCP capability. It does not execute the MCP server or bind it to an agent.
//	@Tags		mcp-directory
//	@Produce	json
//	@Param		workspaceID path string true "workspace id"
//	@Param		catalogID path string true "catalog item id"
//	@Success	200 {object} importResponse "already installed"
//	@Success	201 {object} importResponse "imported"
//	@Failure	400 {object} map[string]string
//	@Failure	403 {object} map[string]string
//	@Failure	404 {object} map[string]string
//	@Failure	409 {object} map[string]string
//	@Router		/api/v1/workspaces/{workspaceID}/mcp-directory/{catalogID}/import [post]
func (h *handler) importItem(w http.ResponseWriter, r *http.Request) {
	workspaceID, ok := h.authorize(w, r, true)
	if !ok {
		return
	}
	snapshot, installs, ok := h.load(w, r, workspaceID)
	if !ok {
		return
	}
	item, found := snapshot.Find(chi.URLParam(r, "catalogID"))
	if !found {
		writeError(w, http.StatusNotFound, "connector_not_found")
		return
	}
	if existing, installed := installMap(installs)[item.ID]; installed {
		writeJSON(w, http.StatusOK, importResponse{Installed: true, CapabilityID: existing.CapabilityID})
		return
	}
	if !item.Authentication.ConnectionSupported() {
		writeError(w, http.StatusConflict, "connector_connection_unavailable")
		return
	}

	payload, err := json.Marshal(sourcePayload{
		SourceFormat:   "mcp_catalog",
		CatalogID:      item.ID,
		CatalogVersion: item.Version,
		CatalogSource:  string(snapshot.Source),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "catalog_source_encode_failed")
		return
	}
	result, err := h.deps.Store.ImportCapability(r.Context(), store.ImportCapabilityInput{
		WorkspaceID:   workspaceID,
		Name:          item.Name,
		Description:   item.Description,
		Visibility:    "workspace",
		Type:          "mcp",
		CreatorID:     auth.UserIDFromContext(r.Context()),
		Version:       item.Version,
		SourcePayload: payload,
		Spec:          item.CanonicalSpec(),
	})
	if err != nil {
		if errors.Is(err, store.ErrCapabilityNameTaken) {
			// A concurrent identical import can lose the capability name race.
			// Re-read provenance before reporting a real name conflict.
			if current, listErr := h.deps.Store.ListMCPDirectoryInstalls(r.Context(), workspaceID); listErr == nil {
				if existing, installed := installMap(current)[item.ID]; installed {
					writeJSON(w, http.StatusOK, importResponse{Installed: true, CapabilityID: existing.CapabilityID})
					return
				}
			}
			writeError(w, http.StatusConflict, "capability_name_conflict")
			return
		}
		writeError(w, http.StatusInternalServerError, "connector_import_failed")
		return
	}
	writeJSON(w, http.StatusCreated, importResponse{
		Installed:    true,
		CapabilityID: result.Capability.ID,
		Created:      true,
		Capability:   &result.Capability,
	})
}

func (h *handler) authorize(w http.ResponseWriter, r *http.Request, admin bool) (string, bool) {
	allowed := []string{"owner", "admin", "member", "viewer"}
	if admin {
		allowed = []string{"owner", "admin"}
	}
	return h.authorizeRoles(w, r, allowed...)
}

func (h *handler) authorizeRoles(w http.ResponseWriter, r *http.Request, allowed ...string) (string, bool) {
	if h.deps.Catalog == nil || h.deps.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "mcp_directory_unavailable")
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

func (h *handler) load(w http.ResponseWriter, r *http.Request, workspaceID string) (mcpcatalog.Snapshot, []store.MCPDirectoryInstall, bool) {
	snapshot, err := h.deps.Catalog.Load(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "mcp_catalog_unavailable")
		return mcpcatalog.Snapshot{}, nil, false
	}
	installs, err := h.deps.Store.ListMCPDirectoryInstalls(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "directory_install_state_failed")
		return mcpcatalog.Snapshot{}, nil, false
	}
	return snapshot, installs, true
}

func installMap(installs []store.MCPDirectoryInstall) map[string]store.MCPDirectoryInstall {
	result := make(map[string]store.MCPDirectoryInstall, len(installs))
	for _, install := range installs {
		result[install.CatalogID] = install
	}
	return result
}

type connectionState struct {
	Authorized      bool
	Status          string
	CheckedAt       *time.Time
	ErrorCode       string
	ProtocolVersion string
	ServerName      string
	ServerVersion   string
	ToolCount       *int
}

func summarizeItem(
	item mcpcatalog.Item,
	install store.MCPDirectoryInstall,
	connections map[string]connectionState,
) itemResponse {
	var installedCapabilityID *string
	if install.CapabilityID != "" {
		id := install.CapabilityID
		installedCapabilityID = &id
	}
	connection := connections[item.Authentication.CredentialKind]
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
		Transport:             item.Transport,
		Authentication:        item.Authentication.EffectiveType(),
		CredentialKind:        item.Authentication.CredentialKind,
		ConnectionSupported:   item.Authentication.ConnectionSupported(),
		Connected:             connection.Authorized,
		ConnectionStatus:      connection.Status,
		ConnectionCheckedAt:   connection.CheckedAt,
		ConnectionError:       connection.ErrorCode,
		ConnectionProtocol:    connection.ProtocolVersion,
		ConnectionServerName:  connection.ServerName,
		ConnectionServerVer:   connection.ServerVersion,
		ConnectionToolCount:   connection.ToolCount,
		Installed:             install.CapabilityID != "",
		InstalledCapabilityID: installedCapabilityID,
	}
}

func (h *handler) connectionStates(w http.ResponseWriter, r *http.Request, workspaceID string) (map[string]connectionState, bool) {
	if h.deps.WorkspaceCredentials == nil {
		return map[string]connectionState{}, true
	}
	workspaceSecrets, err := h.deps.WorkspaceCredentials.ListSecrets(r.Context(), workspaceID, 1000)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "connector_workspace_connection_state_failed")
		return nil, false
	}
	result := make(map[string]connectionState)
	for _, workspaceSecret := range workspaceSecrets {
		if workspaceSecret.Kind != "capability_inline" ||
			workspaceSecret.Status != "active" ||
			workspaceSecret.AuthType != "oauth2" ||
			metadataString(workspaceSecret.Metadata, "workspace_id") != workspaceID {
			continue
		}
		credentialKind := metadataString(workspaceSecret.Metadata, "credential_kind_code")
		if credentialKind == "" {
			continue
		}
		if _, exists := result[credentialKind]; exists {
			continue
		}
		state := connectionState{Authorized: true, Status: "authorized"}
		if h.deps.Secrets != nil {
			stored, err := h.deps.WorkspaceCredentials.GetSecretPayload(r.Context(), workspaceID, workspaceSecret.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "connector_workspace_connection_state_failed")
				return nil, false
			}
			payload, err := h.deps.Secrets.Decrypt(stored.EncryptedPayload)
			if err != nil {
				state.Status = mcpoauth.VerificationUnavailable
				state.ErrorCode = "connector_oauth_credential_unreadable"
			} else {
				applyVerificationState(&state, payload)
			}
		}
		result[credentialKind] = state
	}
	return result, true
}

func applyVerificationState(state *connectionState, payload map[string]any) {
	verification := mcpoauth.VerificationFromPayload(payload)
	if verification.Status == "" {
		return
	}
	state.Status = verification.Status
	state.ErrorCode = verification.ErrorCode
	state.ProtocolVersion = verification.ProtocolVersion
	state.ServerName = verification.ServerName
	state.ServerVersion = verification.ServerVersion
	if !verification.CheckedAt.IsZero() {
		checkedAt := verification.CheckedAt
		state.CheckedAt = &checkedAt
	}
	toolCount := verification.ToolCount
	state.ToolCount = &toolCount
}

func metadataString(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return strings.TrimSpace(value)
}

func sortedEnvNames(env map[string]string) []string {
	result := make([]string, 0, len(env))
	for name := range env {
		result = append(result, name)
	}
	slices.Sort(result)
	return result
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
