package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type capabilityBody struct {
	Type                string                     `json:"type"`
	Name                string                     `json:"name"`
	Description         string                     `json:"description"`
	Visibility          string                     `json:"visibility"`
	Scope               string                     `json:"scope"`
	RequiredCredentials []store.RequiredCredential `json:"required_credentials"`
	Version             string                     `json:"version"`
	GitRepoURL          string                     `json:"git_repo_url"`
	GitRef              string                     `json:"git_ref"`
	Path                string                     `json:"path"`
	Content             map[string]any             `json:"content"`
	SchemaVersion       int16                      `json:"schema_version"`
	CanonicalSpec       json.RawMessage            `json:"canonical_spec"`
}

type patchCapabilityBody struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Visibility  *string `json:"visibility"`
	Scope       *string `json:"scope"`
}

type capabilityVersionBody struct {
	Version             string                     `json:"version"`
	GitRepoURL          string                     `json:"git_repo_url"`
	GitRef              string                     `json:"git_ref"`
	Path                string                     `json:"path"`
	Content             map[string]any             `json:"content"`
	RequiredCredentials []store.RequiredCredential `json:"required_credentials"`
	SchemaVersion       int16                      `json:"schema_version"`
	CanonicalSpec       json.RawMessage            `json:"canonical_spec"`
}

type credentialBody struct {
	Kind           string  `json:"kind"`
	PlaintextValue *string `json:"plaintext_value"`
	DisplayName    *string `json:"display_name"`
}

type agentCapabilityBody struct {
	Configuration map[string]any `json:"configuration"`
	// PinningMode is "latest" or "pinned". Empty falls back to the
	// store-side default (pinned), but the create/edit dialogs always
	// send a value so the server doesn't have to guess.
	PinningMode string `json:"pinning_mode,omitempty"`
}

// builtinCapability describes a runtime-injected tool that every eligible agent
// gets automatically (no capability_version row). It is surfaced in the agent
// capability list as an "installed, default-ON" card the owner can toggle off.
type builtinCapability struct {
	Key         string
	Name        string
	Description string
	Type        string
}

// builtinCapabilities is the single source of truth for runtime-injected
// built-ins the frontend should display + toggle. Keys MUST match the server
// name the connector injects (agentdaemon.imHistoryServerName). Kept as a small
// local table to avoid importing the connector package (which would risk an
// import cycle); the value is a stable protocol constant.
var builtinCapabilities = []builtinCapability{
	{
		Key:         "parsar_chat_history",
		Name:        "Chat history query (fetch_chat_history)",
		Description: "Let the Agent pull historical messages from the current group chat on demand. Enabled by default; when disabled, this Agent will not be able to invoke this tool.",
		Type:        "mcp",
	},
}

// builtinCapabilityBody is the toggle payload for a built-in capability.
type builtinCapabilityBody struct {
	Enabled bool `json:"enabled"`
}

// lookupBuiltinCapability returns the registry entry for a key, or false.
func lookupBuiltinCapability(key string) (builtinCapability, bool) {
	for _, b := range builtinCapabilities {
		if b.Key == key {
			return b, true
		}
	}
	return builtinCapability{}, false
}

type uninstallMarketplaceBody struct {
	SourceCapabilityID string `json:"source_capability_id"`
}

type marketplaceCapabilityDetail struct {
	CapabilityID string                 `json:"capability_id"`
	Type         string                 `json:"type"`
	VersionID    string                 `json:"version_id"`
	Version      string                 `json:"version"`
	GitRepoURL   string                 `json:"git_repo_url,omitempty"`
	GitRef       string                 `json:"git_ref,omitempty"`
	Path         string                 `json:"path,omitempty"`
	Skill        *canonical.SkillSpec   `json:"skill,omitempty"`
	MCP          *marketplaceMCPPreview `json:"mcp,omitempty"`
}

type marketplaceMCPPreview struct {
	Servers []marketplaceMCPServerPreview `json:"servers"`
}

type marketplaceMCPServerPreview struct {
	Name              string                            `json:"name"`
	Command           string                            `json:"command"`
	Args              []string                          `json:"args,omitempty"`
	Env               map[string]marketplaceMCPEnvValue `json:"env,omitempty"`
	StartupTimeoutSec int                               `json:"startup_timeout_sec,omitempty"`
}

type marketplaceMCPEnvValue struct {
	Mode               canonical.EnvMode `json:"mode"`
	Value              string            `json:"value,omitempty"`
	CredentialKindCode string            `json:"credential_kind_code,omitempty"`
	Redacted           bool              `json:"redacted,omitempty"`
}

type upgradeAgentCapabilityBody struct {
	NewVersionID string `json:"new_version_id"`
	// PinningMode lets the upgrade endpoint set the mode atomically with
	// the version bump — e.g. user switches version from v1 to v3 and
	// pins it at the same time, or switches from pinned-v2 to latest.
	// Empty falls back to "pinned" (preserves prior behaviour).
	PinningMode string `json:"pinning_mode,omitempty"`
}

type userCredentialResponse struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	DisplayName string     `json:"display_name"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

var plaintextSecretPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	{name: "github personal access token", pattern: regexp.MustCompile(`(?i)(github_pat_[A-Za-z0-9_]{20,}|ghp_[A-Za-z0-9]{20,})`)},
	{name: "slack bot token", pattern: regexp.MustCompile(`xoxb-[A-Za-z0-9-]{20,}`)},
	{name: "aws access key", pattern: regexp.MustCompile(`AKIA[A-Z0-9]{16}`)},
	{name: "jwt", pattern: regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	{name: "postgres password url", pattern: regexp.MustCompile(`(?i)postgres(?:ql)?://[^\s:@/]+:[^\s:@/]+@`)},
	{name: "generic api key", pattern: regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|secret)["'\s:=]+[A-Za-z0-9_./+=-]{32,}`)},
}

// listWorkspaceCapabilities returns own capabilities plus marketplace installs
// and available marketplace items for the workspace.
//
//	@Summary		List workspace capabilities
//	@Description	Returns MCP and Skill capabilities from the workspace plus its marketplace installs and available marketplace items. Optional pagination via page + page_size opts into a paged shape.
//	@Tags			capabilities
//	@ID				listDevWorkspaceCapabilities
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Param			visibility	query	string	false	"Filter by capability visibility (private/public)"
//	@Param			scope		query	string	false	"Alias of visibility (legacy)"
//	@Param			type		query	string	false	"Filter by capability type (mcp/skill)"
//	@Param			name		query	string	false	"Case-insensitive substring match on name/description"
//	@Param			page		query	int		false	"1-based page number (opts into paged shape)"
//	@Param			page_size	query	int		false	"Page size, 1..100 (defaults to 20)"
//	@Success		200 {object} map[string]interface{} "Workspace capabilities and marketplace listings"
//	@Failure		400 {object} map[string]string "workspace_id must be a valid uuid"
//	@Failure		403 {object} map[string]string "Caller is not an active workspace member"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/capabilities [get]
func listWorkspaceCapabilities(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := requireWorkspaceCapabilityRead(w, r, runtimeStore)
		if !ok {
			return
		}
		visibility := r.URL.Query().Get("visibility")
		if visibility == "" {
			visibility = r.URL.Query().Get("scope")
		}
		typeFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
		if typeFilter != "" && !isListedCapabilityType(typeFilter) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "type must be mcp or skill"})
			return
		}
		nameFilter := strings.TrimSpace(r.URL.Query().Get("name"))
		caps, err := runtimeStore.ListCapabilities(r.Context(), workspaceID, store.ListCapabilityFilter{Type: typeFilter, Visibility: visibility, Name: nameFilter})
		if err != nil {
			writeCapabilityError(w, err, "failed to list capabilities")
			return
		}
		filteredCaps := caps[:0]
		for _, item := range caps {
			if isListedCapabilityType(item.Type) {
				filteredCaps = append(filteredCaps, item)
			}
		}
		caps = filteredCaps
		marketplaceInstalls, err := runtimeStore.ListWorkspaceMarketplaceInstalls(r.Context(), workspaceID)
		if err != nil {
			writeCapabilityError(w, err, "failed to list marketplace installs")
			return
		}
		marketplaceAvailable, err := runtimeStore.ListMarketplaceCapabilities(r.Context(), workspaceID)
		if err != nil {
			writeCapabilityError(w, err, "failed to list marketplace capabilities")
			return
		}
		// Apply the same type/name filter to marketplace installs so the
		// merged list behaves consistently across both sources.
		nameNeedle := strings.ToLower(nameFilter)
		filteredInstalls := marketplaceInstalls[:0]
		for _, item := range marketplaceInstalls {
			if !isListedCapabilityType(item.Type) {
				continue
			}
			if typeFilter != "" && item.Type != typeFilter {
				continue
			}
			if nameNeedle != "" && !strings.Contains(strings.ToLower(item.Name), nameNeedle) && !strings.Contains(strings.ToLower(item.Description), nameNeedle) {
				continue
			}
			filteredInstalls = append(filteredInstalls, item)
		}
		marketplaceInstalls = filteredInstalls
		// Picker only wants discoverable rows — drop self-published and
		// already-installed so they don't double up with the workspace section.
		filteredAvailable := marketplaceAvailable[:0]
		for _, item := range marketplaceAvailable {
			if !isListedCapabilityType(item.Type) {
				continue
			}
			if item.Installed || item.SelfPublished {
				continue
			}
			if typeFilter != "" && item.Type != typeFilter {
				continue
			}
			if nameNeedle != "" && !strings.Contains(strings.ToLower(item.Name), nameNeedle) && !strings.Contains(strings.ToLower(item.Description), nameNeedle) {
				continue
			}
			filteredAvailable = append(filteredAvailable, item)
		}
		marketplaceAvailable = filteredAvailable

		// Pagination: opt-in via ?page or ?page_size. When neither is set we
		// keep the legacy full-list shape so older clients keep working.
		_, hasPage := r.URL.Query()["page"]
		_, hasSize := r.URL.Query()["page_size"]
		if !hasPage && !hasSize {
			writeJSON(w, http.StatusOK, map[string]any{
				"workspace_id":          workspaceID,
				"capabilities":          caps,
				"marketplace_installs":  marketplaceInstalls,
				"marketplace_available": marketplaceAvailable,
			})
			return
		}

		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
		if pageSize < 1 {
			pageSize = 20
		}
		if pageSize > 100 {
			pageSize = 100
		}

		type mergedRow struct {
			OwnIndex    int // -1 when this row is a marketplace install
			MarketIndex int // -1 when this row is an own capability
			Name        string
			CreatedAt   time.Time
			IsInstall   bool
		}
		merged := make([]mergedRow, 0, len(caps)+len(marketplaceInstalls))
		for i, c := range caps {
			merged = append(merged, mergedRow{OwnIndex: i, MarketIndex: -1, Name: c.Name, CreatedAt: c.CreatedAt, IsInstall: false})
		}
		for i, m := range marketplaceInstalls {
			merged = append(merged, mergedRow{OwnIndex: -1, MarketIndex: i, Name: m.Name, CreatedAt: m.LatestVersionCreatedAt, IsInstall: true})
		}
		// Stable order: name asc (case-insensitive), tiebreak by created_at desc.
		sort.SliceStable(merged, func(i, j int) bool {
			ni, nj := strings.ToLower(merged[i].Name), strings.ToLower(merged[j].Name)
			if ni != nj {
				return ni < nj
			}
			return merged[i].CreatedAt.After(merged[j].CreatedAt)
		})

		total := len(merged)
		start := (page - 1) * pageSize
		if start > total {
			start = total
		}
		end := start + pageSize
		if end > total {
			end = total
		}
		pageRows := merged[start:end]
		pagedCaps := make([]store.CapabilityRead, 0, len(pageRows))
		pagedInstalls := make([]store.MarketplaceInstallRead, 0, len(pageRows))
		for _, row := range pageRows {
			if row.IsInstall {
				pagedInstalls = append(pagedInstalls, marketplaceInstalls[row.MarketIndex])
			} else {
				pagedCaps = append(pagedCaps, caps[row.OwnIndex])
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"workspace_id":          workspaceID,
			"capabilities":          pagedCaps,
			"marketplace_installs":  pagedInstalls,
			"marketplace_available": marketplaceAvailable,
			"page":                  page,
			"page_size":             pageSize,
			"total":                 total,
		})
	}
}

// listMarketplaceCapabilities lists all public capabilities offered on the
// marketplace, filtered to items the caller's workspace can install.
//
//	@Summary		List marketplace capabilities
//	@Description	Lists public MCP and Skill capabilities offered on the marketplace, filtered to items visible to the caller's workspace. Workspace is taken from ?workspace_id or the X-Parsar-Workspace-ID header.
//	@Tags			capabilities
//	@ID				listDevMarketplaceCapabilities
//	@Produce		json
//	@Param			workspace_id	query	string	false	"Workspace UUID (falls back to X-Parsar-Workspace-ID header)"
//	@Success		200 {object} map[string]interface{} "Marketplace capabilities visible to the workspace"
//	@Failure		400 {object} map[string]string "workspace_id must be a valid uuid"
//	@Failure		403 {object} map[string]string "Caller is not an active workspace member"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/capabilities/marketplace [get]
func listMarketplaceCapabilities(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
		if workspaceID == "" {
			workspaceID = strings.TrimSpace(r.Header.Get("X-Parsar-Workspace-ID"))
		}
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed capability APIs are disabled"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		capabilities, err := runtimeStore.ListMarketplaceCapabilities(r.Context(), workspaceID)
		if err != nil {
			writeCapabilityError(w, err, "failed to list marketplace capabilities")
			return
		}
		filtered := capabilities[:0]
		for _, capability := range capabilities {
			if isListedCapabilityType(capability.Type) {
				filtered = append(filtered, capability)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "capabilities": filtered})
	}
}

func isListedCapabilityType(capabilityType string) bool {
	switch strings.ToLower(strings.TrimSpace(capabilityType)) {
	case "mcp", "skill":
		return true
	default:
		return false
	}
}

// getMarketplaceCapabilityDetail returns the latest public MCP/Skill body on
// demand so list responses stay lightweight.
//
//	@Summary		Get marketplace capability detail
//	@Description	Returns the latest public MCP or Skill definition. Inline secret IDs are redacted.
//	@Tags			capabilities
//	@ID			getDevMarketplaceCapabilityDetail
//	@Produce		json
//	@Param			capabilityID	path	string	true	"Capability UUID"
//	@Param			workspace_id	query	string	false	"Workspace UUID (falls back to X-Parsar-Workspace-ID header)"
//	@Success		200 {object} map[string]interface{} "Marketplace capability detail"
//	@Failure		400 {object} map[string]string "workspace_id or capabilityID is invalid"
//	@Failure		403 {object} map[string]string "Caller is not an active workspace member"
//	@Failure		404 {object} map[string]string "Capability is not available in the Marketplace"
//	@Router			/api/v1/capabilities/marketplace/{capabilityID} [get]
func getMarketplaceCapabilityDetail(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
		if workspaceID == "" {
			workspaceID = strings.TrimSpace(r.Header.Get("X-Parsar-Workspace-ID"))
		}
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		capabilityID := strings.TrimSpace(chi.URLParam(r, "capabilityID"))
		if !isUUID(capabilityID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "capabilityID must be a valid uuid"})
			return
		}
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed capability APIs are disabled"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}

		capabilities, err := runtimeStore.ListMarketplaceCapabilities(r.Context(), workspaceID)
		if err != nil {
			writeCapabilityError(w, err, "failed to load marketplace capability")
			return
		}
		var capability *store.MarketplaceCapabilityRead
		for i := range capabilities {
			if capabilities[i].CapabilityID == capabilityID {
				capability = &capabilities[i]
				break
			}
		}
		if capability == nil || capability.Visibility != "public" || capability.Status != "active" || capability.DeprecatedAt != nil || !isPreviewableMarketplaceType(capability.Type) {
			writeCapabilityError(w, fmt.Errorf("%w: %s", store.ErrUnknownCapability, capabilityID), "failed to load marketplace capability")
			return
		}

		version, err := runtimeStore.GetCapabilityVersion(r.Context(), capability.LatestVersionID)
		if err != nil {
			writeCapabilityError(w, err, "failed to load marketplace capability version")
			return
		}
		detail, err := buildMarketplaceCapabilityDetail(*capability, version)
		if err != nil {
			writeCapabilityError(w, err, "marketplace capability detail is invalid")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"capability": detail})
	}
}

func isPreviewableMarketplaceType(capabilityType string) bool {
	switch strings.ToLower(strings.TrimSpace(capabilityType)) {
	case string(canonical.KindMCP), string(canonical.KindSkill):
		return true
	default:
		return false
	}
}

func buildMarketplaceCapabilityDetail(capability store.MarketplaceCapabilityRead, version store.CapabilityVersionRead) (marketplaceCapabilityDetail, error) {
	detail := marketplaceCapabilityDetail{
		CapabilityID: capability.CapabilityID,
		Type:         capability.Type,
		VersionID:    version.ID,
		Version:      version.Version,
		GitRepoURL:   version.GitRepoURL,
		GitRef:       version.GitRef,
		Path:         version.Path,
	}
	if len(version.CanonicalSpec) == 0 {
		return detail, nil
	}
	var spec canonical.Spec
	if err := json.Unmarshal(version.CanonicalSpec, &spec); err != nil {
		return detail, fmt.Errorf("decode marketplace canonical spec: %w", err)
	}
	if err := spec.Validate(); err != nil {
		return detail, fmt.Errorf("validate marketplace canonical spec: %w", err)
	}
	if string(spec.Kind) != strings.ToLower(strings.TrimSpace(capability.Type)) {
		return detail, fmt.Errorf("marketplace capability type %q does not match canonical kind %q", capability.Type, spec.Kind)
	}

	switch spec.Kind {
	case canonical.KindSkill:
		detail.Skill = spec.Skill
	case canonical.KindMCP:
		detail.MCP = previewMarketplaceMCP(spec.MCP)
	default:
		return detail, fmt.Errorf("marketplace capability type %q is not previewable", spec.Kind)
	}
	return detail, nil
}

func previewMarketplaceMCP(spec *canonical.MCPSpec) *marketplaceMCPPreview {
	if spec == nil {
		return nil
	}
	preview := &marketplaceMCPPreview{Servers: make([]marketplaceMCPServerPreview, 0, len(spec.Servers))}
	for _, server := range spec.Servers {
		item := marketplaceMCPServerPreview{
			Name:              server.Name,
			Command:           server.Command,
			Args:              append([]string(nil), server.Args...),
			StartupTimeoutSec: server.StartupTimeoutSec,
		}
		if len(server.Env) > 0 {
			item.Env = make(map[string]marketplaceMCPEnvValue, len(server.Env))
			for name, value := range server.Env {
				previewValue := marketplaceMCPEnvValue{Mode: value.Mode}
				switch value.Mode {
				case canonical.EnvModeLiteral:
					previewValue.Value = value.Literal
				case canonical.EnvModeCredentialRef:
					previewValue.CredentialKindCode = value.CredentialKindCode
				case canonical.EnvModeInlineSecret:
					previewValue.Redacted = true
				}
				item.Env[name] = previewValue
			}
		}
		preview.Servers = append(preview.Servers, item)
	}
	return preview
}

// listWorkspaceMarketplaceInstalls returns the marketplace capabilities the
// workspace has installed (as opposed to authored).
//
//	@Summary		List workspace marketplace installs
//	@Description	Returns the marketplace capabilities the workspace has installed (as opposed to authored).
//	@Tags			capabilities
//	@ID				listDevWorkspaceMarketplaceInstalls
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Success		200 {object} map[string]interface{} "Marketplace installs for the workspace"
//	@Failure		400 {object} map[string]string "workspace_id must be a valid uuid"
//	@Failure		403 {object} map[string]string "Caller is not an active workspace member"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/capabilities/marketplace-installs [get]
func listWorkspaceMarketplaceInstalls(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := requireWorkspaceCapabilityRead(w, r, runtimeStore)
		if !ok {
			return
		}
		installs, err := runtimeStore.ListWorkspaceMarketplaceInstalls(r.Context(), workspaceID)
		if err != nil {
			writeCapabilityError(w, err, "failed to list marketplace installs")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "capabilities": installs})
	}
}

// getCapabilityInstallCount returns the marketplace install count for a
// capability the workspace authored.
//
//	@Summary		Get marketplace install count
//	@Description	Returns the marketplace install count for a capability the workspace has published.
//	@Tags			capabilities
//	@ID				getDevCapabilityInstallCount
//	@Produce		json
//	@Param			workspaceID		path	string	true	"Workspace UUID"
//	@Param			capabilityID	path	string	true	"Capability UUID"
//	@Success		200 {object} map[string]interface{} "capability_id and install_count"
//	@Failure		400 {object} map[string]string "workspace_id or capability_id is not a valid uuid"
//	@Failure		403 {object} map[string]string "Caller is not an active workspace member"
//	@Failure		404 {object} map[string]string "Capability not found in this workspace"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/capabilities/{capabilityID}/install-count [get]
func getCapabilityInstallCount(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, capabilityID, ok := requireWorkspaceCapabilityByID(w, r, runtimeStore, false)
		if !ok {
			return
		}
		count, err := runtimeStore.CountInstalls(r.Context(), capabilityID)
		if err != nil {
			writeCapabilityError(w, err, "failed to count marketplace installs")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"capability_id": capabilityID, "install_count": count})
	}
}

// listMarketplaceEnabledAgents lists agents in the target workspace that have
// this marketplace capability enabled.
//
//	@Summary		List agents using a marketplace capability
//	@Description	Lists agents in the workspace that have this marketplace capability enabled.
//	@Tags			capabilities
//	@ID				listDevMarketplaceEnabledAgents
//	@Produce		json
//	@Param			workspaceID		path	string	true	"Workspace UUID"
//	@Param			capabilityID	path	string	true	"Source marketplace capability UUID"
//	@Success		200 {object} map[string]interface{} "capability_id and enabled agents"
//	@Failure		400 {object} map[string]string "workspace_id or capability_id is not a valid uuid"
//	@Failure		403 {object} map[string]string "Caller is not an active workspace member"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/capabilities/{capabilityID}/enabled-agents [get]
func listMarketplaceEnabledAgents(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := requireWorkspaceCapabilityRead(w, r, runtimeStore)
		if !ok {
			return
		}
		capabilityID := chi.URLParam(r, "capabilityID")
		if !isUUID(capabilityID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "capability_id must be a valid uuid"})
			return
		}
		agents, err := runtimeStore.ListEnabledAgents(r.Context(), workspaceID, capabilityID)
		if err != nil {
			writeCapabilityError(w, err, "failed to list enabled marketplace agents")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"capability_id": capabilityID, "agents": agents})
	}
}

// createWorkspaceCapability creates a capability (optionally with an initial
// version) in the given workspace. Owner/admin only.
//
//	@Summary		Create a workspace capability
//	@Description	Creates an MCP or Skill capability in the workspace. Owner/admin only. When body.version is set, an initial capability_version is created in the same call.
//	@Tags			capabilities
//	@ID				createDevWorkspaceCapability
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string			true	"Workspace UUID"
//	@Param			body		body	capabilityBody	true	"Capability create payload"
//	@Success		201 {object} map[string]interface{} "Created capability"
//	@Failure		400 {object} map[string]string "Malformed body, missing name/type, or bad canonical_spec"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/capabilities [post]
func createWorkspaceCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := requireWorkspaceCapabilityAdmin(w, r, runtimeStore)
		if !ok {
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		var body capabilityBody
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Type) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and type are required"})
			return
		}
		body.Type = strings.ToLower(strings.TrimSpace(body.Type))
		if !isListedCapabilityType(body.Type) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "type must be mcp or skill"})
			return
		}
		input := store.CreateCapabilityInput{WorkspaceID: workspaceID, Type: body.Type, Name: body.Name, Description: body.Description, Visibility: capabilityVisibility(body.Visibility, body.Scope), CreatorID: actorID}
		if strings.TrimSpace(body.Version) != "" {
			ver := store.CreateCapabilityVersionInput{Version: body.Version, GitRepoURL: body.GitRepoURL, GitRef: body.GitRef, Path: body.Path, Content: body.Content, RequiredCredentials: body.RequiredCredentials, SchemaVersion: body.SchemaVersion, CanonicalSpec: body.CanonicalSpec}
			if err := validateCanonicalSpecForType(body.Type, body.CanonicalSpec); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			input.InitialVersion = &ver
		}
		capability, err := runtimeStore.CreateCapability(r.Context(), input)
		if err != nil {
			writeCapabilityError(w, err, "failed to create capability")
			return
		}
		writeJSON(w, http.StatusCreated, capability)
	}
}

// getWorkspaceCapability returns a single capability. Public+active
// capabilities are visible cross-workspace; otherwise the row must live in
// the caller's workspace.
//
//	@Summary		Get a workspace capability
//	@Description	Returns a single capability. Public + active capabilities are visible cross-workspace; otherwise the row must belong to the caller's workspace.
//	@Tags			capabilities
//	@ID				getDevWorkspaceCapability
//	@Produce		json
//	@Param			workspaceID		path	string	true	"Workspace UUID"
//	@Param			capabilityID	path	string	true	"Capability UUID"
//	@Success		200 {object} map[string]interface{} "Capability row"
//	@Failure		400 {object} map[string]string "workspace_id or capability_id is not a valid uuid"
//	@Failure		403 {object} map[string]string "Caller is not an active workspace member"
//	@Failure		404 {object} map[string]string "Capability not visible to this workspace"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/capabilities/{capabilityID} [get]
func getWorkspaceCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, capabilityID, ok := requireWorkspaceCapabilityByID(w, r, runtimeStore, false)
		if !ok {
			return
		}
		capability, err := runtimeStore.GetCapability(r.Context(), capabilityID)
		if err != nil {
			writeCapabilityError(w, err, "failed to get capability")
			return
		}
		if capability.WorkspaceID != workspaceID && !(capability.Visibility == "public" && capability.Status == "active") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "capability not found"})
			return
		}
		writeJSON(w, http.StatusOK, capability)
	}
}

// patchWorkspaceCapability applies a partial update to a capability. Owner
// or admin of the owning workspace only.
//
//	@Summary		Patch a workspace capability
//	@Description	Applies a partial update to a capability (name/description/visibility). Owner/admin of the owning workspace only.
//	@Tags			capabilities
//	@ID				patchDevWorkspaceCapability
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID		path	string					true	"Workspace UUID"
//	@Param			capabilityID	path	string					true	"Capability UUID"
//	@Param			body			body	patchCapabilityBody		true	"Partial capability update"
//	@Success		200 {object} map[string]interface{} "Updated capability"
//	@Failure		400 {object} map[string]string "Malformed body or invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Capability not found in this workspace"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/capabilities/{capabilityID} [patch]
func patchWorkspaceCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, capabilityID, ok := requireWorkspaceCapabilityByID(w, r, runtimeStore, true)
		if !ok {
			return
		}
		var body patchCapabilityBody
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		updated, err := runtimeStore.UpdateCapability(r.Context(), store.UpdateCapabilityInput{CapabilityID: capabilityID, Name: body.Name, Description: body.Description, Visibility: capabilityVisibilityPtr(body.Visibility, body.Scope)})
		if err != nil {
			writeCapabilityError(w, err, "failed to update capability")
			return
		}
		if updated.WorkspaceID != workspaceID {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "capability not found"})
			return
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

// publishWorkspaceCapability promotes a capability to the marketplace.
//
//	@Summary		Publish a capability to the marketplace
//	@Description	Promotes an active capability to the marketplace. Owner/admin only. Rejects with 400 if any capability version contains a plaintext-looking secret.
//	@Tags			capabilities
//	@ID				publishDevWorkspaceCapability
//	@Produce		json
//	@Param			workspaceID		path	string	true	"Workspace UUID"
//	@Param			capabilityID	path	string	true	"Capability UUID"
//	@Success		200 {object} map[string]interface{} "Updated capability"
//	@Failure		400 {object} map[string]string "Plaintext secret detected or invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Capability not found in this workspace"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/capabilities/{capabilityID}/publish [post]
func publishWorkspaceCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return marketplaceStateCapability(runtimeStore, "publish")
}

// unpublishWorkspaceCapability retracts a capability from the marketplace.
//
//	@Summary		Unpublish a capability from the marketplace
//	@Description	Retracts a previously published capability from the marketplace. Owner/admin only.
//	@Tags			capabilities
//	@ID				unpublishDevWorkspaceCapability
//	@Produce		json
//	@Param			workspaceID		path	string	true	"Workspace UUID"
//	@Param			capabilityID	path	string	true	"Capability UUID"
//	@Success		200 {object} map[string]interface{} "Updated capability"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Capability not found in this workspace"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/capabilities/{capabilityID}/unpublish [post]
func unpublishWorkspaceCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return marketplaceStateCapability(runtimeStore, "unpublish")
}

// deprecateWorkspaceCapability marks a marketplace capability as deprecated.
//
//	@Summary		Deprecate a marketplace capability
//	@Description	Marks a published capability as deprecated so it stays reachable for existing installs but is hidden from new installs. Owner/admin only.
//	@Tags			capabilities
//	@ID				deprecateDevWorkspaceCapability
//	@Produce		json
//	@Param			workspaceID		path	string	true	"Workspace UUID"
//	@Param			capabilityID	path	string	true	"Capability UUID"
//	@Success		200 {object} map[string]interface{} "Updated capability"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Capability not found in this workspace"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/capabilities/{capabilityID}/deprecate [post]
func deprecateWorkspaceCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return marketplaceStateCapability(runtimeStore, "deprecate")
}

// undeprecateWorkspaceCapability clears the deprecated flag on a marketplace
// capability.
//
//	@Summary		Undeprecate a marketplace capability
//	@Description	Clears the deprecated flag on a previously deprecated marketplace capability. Owner/admin only.
//	@Tags			capabilities
//	@ID				undeprecateDevWorkspaceCapability
//	@Produce		json
//	@Param			workspaceID		path	string	true	"Workspace UUID"
//	@Param			capabilityID	path	string	true	"Capability UUID"
//	@Success		200 {object} map[string]interface{} "Updated capability"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Capability not found in this workspace"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/capabilities/{capabilityID}/undeprecate [post]
func undeprecateWorkspaceCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return marketplaceStateCapability(runtimeStore, "undeprecate")
}

func marketplaceStateCapability(runtimeStore RuntimeStore, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, capabilityID, ok := requireWorkspaceCapabilityByID(w, r, runtimeStore, true)
		if !ok {
			return
		}
		if action == "publish" {
			versions, err := runtimeStore.ListCapabilityVersions(r.Context(), capabilityID)
			if err != nil {
				writeCapabilityError(w, err, "failed to list capability versions")
				return
			}
			if err := rejectPlaintextSecretsInCapabilityVersions(versions); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		var updated store.CapabilityRead
		var err error
		switch action {
		case "publish":
			updated, err = runtimeStore.PublishCapability(r.Context(), workspaceID, capabilityID)
		case "unpublish":
			updated, err = runtimeStore.UnpublishCapability(r.Context(), workspaceID, capabilityID)
		case "deprecate":
			updated, err = runtimeStore.DeprecateCapability(r.Context(), workspaceID, capabilityID)
		case "undeprecate":
			updated, err = runtimeStore.UndeprecateCapability(r.Context(), workspaceID, capabilityID)
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unknown marketplace action"})
			return
		}
		if err != nil {
			writeCapabilityError(w, err, "failed to update marketplace state")
			return
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

// uninstallWorkspaceMarketplaceCapability removes a marketplace install from
// the workspace, detaching any agents that had it enabled.
//
//	@Summary		Uninstall a marketplace capability
//	@Description	Removes a marketplace install from the workspace and detaches any agents that had it enabled. Owner/admin only.
//	@Tags			capabilities
//	@ID				uninstallDevWorkspaceMarketplaceCapability
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string						true	"Workspace UUID"
//	@Param			body		body	uninstallMarketplaceBody	true	"source_capability_id to uninstall"
//	@Success		200 {object} map[string]interface{} "source_capability_id and removed_agent_count"
//	@Failure		400 {object} map[string]string "Malformed body or invalid source_capability_id"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/capabilities/uninstall [post]
func uninstallWorkspaceMarketplaceCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := requireWorkspaceCapabilityAdmin(w, r, runtimeStore)
		if !ok {
			return
		}
		var body uninstallMarketplaceBody
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if !isUUID(body.SourceCapabilityID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "source_capability_id must be a valid uuid"})
			return
		}
		removed, err := runtimeStore.UninstallWorkspaceMarketplaceCapability(r.Context(), workspaceID, body.SourceCapabilityID)
		if err != nil {
			writeCapabilityError(w, err, "failed to uninstall marketplace capability")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"source_capability_id": body.SourceCapabilityID, "removed_agent_count": removed})
	}
}

// deleteWorkspaceCapability soft-deletes a capability. Refuses with 409 when
// any agent still has the capability bound.
//
//	@Summary		Soft-delete a capability
//	@Description	Soft-deletes a workspace capability. Refuses with 409 when any agent still has the capability bound. Owner/admin only.
//	@Tags			capabilities
//	@ID				deleteDevWorkspaceCapability
//	@Produce		json
//	@Param			workspaceID		path	string	true	"Workspace UUID"
//	@Param			capabilityID	path	string	true	"Capability UUID"
//	@Success		200 {object} map[string]interface{} "Deleted capability"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Capability not found in this workspace"
//	@Failure		409 {object} map[string]interface{} "Capability still bound to one or more agents"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/capabilities/{capabilityID} [delete]
func deleteWorkspaceCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, capabilityID, ok := requireWorkspaceCapabilityByID(w, r, runtimeStore, true)
		if !ok {
			return
		}
		// Atomic write: SoftDeleteCapability's SQL carries a NOT EXISTS(agent_capabilities)
		// guard, so the "check binding empty → someone inserts one → we delete" TOCTOU
		// window is closed. When UPDATE affects 0 rows, the store also determines
		// "is it referenced?" — referenced → returns CapabilityHasBindingsError (with Count),
		// unreferenced → ErrUnknownCapability.
		deleted, err := runtimeStore.SoftDeleteCapability(r.Context(), workspaceID, capabilityID)
		if err != nil {
			var bound *store.CapabilityHasBindingsError
			if errors.As(err, &bound) {
				writeJSON(w, http.StatusConflict, map[string]any{
					"error":         "capability_in_use",
					"message":       fmt.Sprintf("This capability is still used by %d agents; please unbind on the consumer side before deleting.", bound.Count),
					"binding_count": bound.Count,
				})
				return
			}
			writeCapabilityError(w, err, "failed to delete capability")
			return
		}
		if deleted.WorkspaceID != workspaceID {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "capability not found"})
			return
		}
		writeJSON(w, http.StatusOK, deleted)
	}
}

// listWorkspaceCapabilityVersions returns every version row for a capability.
//
//	@Summary		List capability versions
//	@Description	Returns every version row for a capability (store-side ordering).
//	@Tags			capabilities
//	@ID				listDevWorkspaceCapabilityVersions
//	@Produce		json
//	@Param			workspaceID		path	string	true	"Workspace UUID"
//	@Param			capabilityID	path	string	true	"Capability UUID"
//	@Success		200 {object} map[string]interface{} "capability_id and versions array"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not an active workspace member"
//	@Failure		404 {object} map[string]string "Capability not found in this workspace"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/capabilities/{capabilityID}/versions [get]
func listWorkspaceCapabilityVersions(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, capabilityID, ok := requireWorkspaceCapabilityByID(w, r, runtimeStore, false)
		if !ok {
			return
		}
		versions, err := runtimeStore.ListCapabilityVersions(r.Context(), capabilityID)
		if err != nil {
			writeCapabilityError(w, err, "failed to list capability versions")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"capability_id": capabilityID, "versions": versions})
	}
}

// createWorkspaceCapabilityVersion adds a new version row to an existing
// capability. Owner/admin only. Prefer /versions/import/commit for real edits.
//
//	@Summary		Create a capability version
//	@Description	Adds a new version row to an existing capability. Owner/admin only. Prefer POST .../versions/import/commit for the parsed import path.
//	@Tags			capabilities
//	@ID				createDevWorkspaceCapabilityVersion
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID		path	string					true	"Workspace UUID"
//	@Param			capabilityID	path	string					true	"Capability UUID"
//	@Param			body			body	capabilityVersionBody	true	"Version payload"
//	@Success		201 {object} map[string]interface{} "Created capability version"
//	@Failure		400 {object} map[string]string "Missing version or bad canonical_spec"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Capability not found in this workspace"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/capabilities/{capabilityID}/versions [post]
func createWorkspaceCapabilityVersion(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, capabilityID, ok := requireWorkspaceCapabilityByID(w, r, runtimeStore, true)
		if !ok {
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		var body capabilityVersionBody
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(body.Version) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "version is required"})
			return
		}
		if err := validateCanonicalSpecForType("", body.CanonicalSpec); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		version, err := runtimeStore.CreateCapabilityVersion(r.Context(), store.CreateCapabilityVersionInput{CapabilityID: capabilityID, Version: body.Version, GitRepoURL: body.GitRepoURL, GitRef: body.GitRef, Path: body.Path, Content: body.Content, RequiredCredentials: body.RequiredCredentials, SchemaVersion: body.SchemaVersion, CanonicalSpec: body.CanonicalSpec, CreatorID: actorID})
		if err != nil {
			writeCapabilityError(w, err, "failed to create capability version")
			return
		}
		writeJSON(w, http.StatusCreated, version)
	}
}

// listMyCredentials returns the authenticated user's stored credentials.
//
//	@Summary		List my credentials
//	@Description	Returns the authenticated user's stored personal credentials. Only metadata is exposed; the encrypted value stays server-side.
//	@Tags			me
//	@ID				listDevMyCredentials
//	@Produce		json
//	@Success		200 {object} map[string]interface{} "credentials array"
//	@Failure		401 {object} map[string]string "unauthenticated"
//	@Failure		503 {object} map[string]string "Database-backed credential APIs are disabled"
//	@Router			/api/v1/me/credentials [get]
func listMyCredentials(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := requireAuthenticatedUser(w, r, runtimeStore)
		if !ok {
			return
		}
		credentials, err := runtimeStore.ListUserCredentials(r.Context(), userID)
		if err != nil {
			writeCapabilityError(w, err, "failed to list user credentials")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"credentials": credentialResponses(credentials)})
	}
}

// createMyCredential stores a new personal credential for the authenticated
// user. Plaintext is encrypted with the master key before it hits the DB.
//
//	@Summary		Create a personal credential
//	@Description	Stores a new personal credential for the authenticated user. Plaintext is encrypted server-side before persisting.
//	@Tags			me
//	@ID				createDevMyCredential
//	@Accept			json
//	@Produce		json
//	@Param			body	body	credentialBody	true	"Credential payload"
//	@Success		201 {object} userCredentialResponse "Created credential (metadata only)"
//	@Failure		400 {object} map[string]string "Missing kind or plaintext_value"
//	@Failure		401 {object} map[string]string "unauthenticated"
//	@Failure		500 {object} map[string]string "Secrets service unavailable"
//	@Failure		503 {object} map[string]string "Database-backed credential APIs are disabled"
//	@Router			/api/v1/me/credentials [post]
func createMyCredential(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := requireAuthenticatedUser(w, r, runtimeStore)
		if !ok {
			return
		}
		var body credentialBody
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(body.Kind) == "" || body.PlaintextValue == nil || strings.TrimSpace(*body.PlaintextValue) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kind and plaintext_value are required"})
			return
		}
		encrypted, ok := encryptCredentialValue(w, *body.PlaintextValue)
		if !ok {
			return
		}
		created, err := runtimeStore.CreateUserCredential(r.Context(), store.CreateUserCredentialInput{UserID: userID, Kind: body.Kind, DisplayName: optionalString(body.DisplayName), EncryptedValue: encrypted, KeyVersion: secrets.EnvelopeVersion})
		if err != nil {
			writeCapabilityError(w, err, "failed to create user credential")
			return
		}
		writeJSON(w, http.StatusCreated, credentialResponse(created))
	}
}

// patchMyCredential updates the display name and/or plaintext value of an
// owned credential. Empty plaintext leaves the encrypted value unchanged.
//
//	@Summary		Patch a personal credential
//	@Description	Updates the display name and/or plaintext value of a credential owned by the caller. Empty plaintext leaves the encrypted value unchanged.
//	@Tags			me
//	@ID				patchDevMyCredential
//	@Accept			json
//	@Produce		json
//	@Param			credentialID	path	string			true	"Credential UUID"
//	@Param			body			body	credentialBody	true	"Partial credential update"
//	@Success		200 {object} userCredentialResponse "Updated credential"
//	@Failure		400 {object} map[string]string "Malformed body or invalid credential_id"
//	@Failure		401 {object} map[string]string "unauthenticated"
//	@Failure		403 {object} map[string]string "Credential belongs to another user"
//	@Failure		503 {object} map[string]string "Database-backed credential APIs are disabled"
//	@Router			/api/v1/me/credentials/{credentialID} [patch]
func patchMyCredential(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := requireAuthenticatedUser(w, r, runtimeStore)
		if !ok {
			return
		}
		credentialID, ok := credentialIDParam(w, r)
		if !ok {
			return
		}
		existing, err := runtimeStore.GetUserCredential(r.Context(), credentialID)
		if err != nil {
			writeCapabilityError(w, err, "failed to get user credential")
			return
		}
		if existing.UserID != userID {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		var body credentialBody
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		var encrypted []byte
		if body.PlaintextValue != nil && strings.TrimSpace(*body.PlaintextValue) != "" {
			var encryptedOK bool
			encrypted, encryptedOK = encryptCredentialValue(w, *body.PlaintextValue)
			if !encryptedOK {
				return
			}
		}
		updated, err := runtimeStore.UpdateUserCredential(r.Context(), store.UpdateUserCredentialInput{CredentialID: credentialID, DisplayName: body.DisplayName, EncryptedValue: encrypted, KeyVersion: secrets.EnvelopeVersion})
		if err != nil {
			writeCapabilityError(w, err, "failed to update user credential")
			return
		}
		writeJSON(w, http.StatusOK, credentialResponse(updated))
	}
}

// deleteMyCredential soft-deletes a credential owned by the caller.
//
//	@Summary		Delete a personal credential
//	@Description	Soft-deletes a credential owned by the caller.
//	@Tags			me
//	@ID				deleteDevMyCredential
//	@Produce		json
//	@Param			credentialID	path	string	true	"Credential UUID"
//	@Success		200 {object} userCredentialResponse "Deleted credential"
//	@Failure		400 {object} map[string]string "Invalid credential_id"
//	@Failure		401 {object} map[string]string "unauthenticated"
//	@Failure		403 {object} map[string]string "Credential belongs to another user"
//	@Failure		503 {object} map[string]string "Database-backed credential APIs are disabled"
//	@Router			/api/v1/me/credentials/{credentialID} [delete]
func deleteMyCredential(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := requireAuthenticatedUser(w, r, runtimeStore)
		if !ok {
			return
		}
		credentialID, ok := credentialIDParam(w, r)
		if !ok {
			return
		}
		existing, err := runtimeStore.GetUserCredential(r.Context(), credentialID)
		if err != nil {
			writeCapabilityError(w, err, "failed to get user credential")
			return
		}
		if existing.UserID != userID {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		deleted, err := runtimeStore.SoftDeleteUserCredential(r.Context(), credentialID)
		if err != nil {
			writeCapabilityError(w, err, "failed to delete user credential")
			return
		}
		writeJSON(w, http.StatusOK, credentialResponse(deleted))
	}
}

// listAgentCapabilities returns the capabilities installed on the agent
// (including built-ins) plus what else is available to install.
//
//	@Summary		List agent capabilities
//	@Description	Returns the capabilities installed on the agent (own + marketplace + built-ins) and what else is available to install from the workspace and the marketplace.
//	@Tags			capabilities
//	@ID				listDevAgentCapabilities
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Param			agentID		path	string	true	"Agent UUID"
//	@Success		200 {object} map[string]interface{} "workspace_id, agent_id, installed, available, marketplace_available"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not an active workspace member"
//	@Failure		404 {object} map[string]string "Agent not found in this workspace"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/agents/{agentID}/capabilities [get]
func listAgentCapabilities(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, agentID, agent, ok := requireAgentMember(w, r, runtimeStore)
		if !ok {
			return
		}
		installed, err := runtimeStore.ListAgentCapabilities(r.Context(), agentID)
		if err != nil {
			writeCapabilityError(w, err, "failed to list agent capabilities")
			return
		}
		enabledCapabilities, err := runtimeStore.GetEnabledMarketplaceCapabilitiesForAgent(r.Context(), agentID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		capabilityByID := make(map[string]store.EnabledCapabilityRead, len(enabledCapabilities))
		for _, capability := range enabledCapabilities {
			capabilityByID[capability.CapabilityID] = capability
		}
		available, err := runtimeStore.ListCapabilities(r.Context(), agent.WorkspaceID, store.ListCapabilityFilter{})
		if err != nil {
			writeCapabilityError(w, err, "failed to list available capabilities")
			return
		}
		marketplace, err := runtimeStore.ListMarketplaceCapabilities(r.Context(), agent.WorkspaceID)
		if err != nil {
			writeCapabilityError(w, err, "failed to list marketplace capabilities")
			return
		}
		availableAny := make([]any, 0, len(available)+len(marketplace))
		for _, capability := range available {
			availableAny = append(availableAny, capability)
		}
		for _, capability := range marketplace {
			if capability.SelfPublished || capability.Installed {
				continue
			}
			availableAny = append(availableAny, capability)
		}
		installedAny := make([]any, 0, len(installed))
		for _, binding := range installed {
			capability, ok := capabilityByID[binding.CapabilityID]
			if !ok {
				installedAny = append(installedAny, binding)
				continue
			}
			installedAny = append(installedAny, map[string]any{
				"id":                    binding.ID,
				"agent_id":              binding.AgentID,
				"capability_id":         binding.CapabilityID,
				"capability_version_id": binding.CapabilityVersionID,
				"enabled":               binding.Enabled,
				"configuration":         binding.Configuration,
				"created_at":            binding.CreatedAt,
				"updated_at":            binding.UpdatedAt,
				"capability": map[string]any{
					"id":                        capability.CapabilityID,
					"workspace_id":              capability.WorkspaceID,
					"type":                      capability.Type,
					"name":                      capability.Name,
					"description":               capability.Description,
					"visibility":                capability.Visibility,
					"status":                    capability.Status,
					"required_credentials":      capability.RequiredCredentials,
					"deprecated_at":             capability.DeprecatedAt,
					"from_marketplace":          capability.WorkspaceID != agent.WorkspaceID,
					"source_workspace_id":       capability.WorkspaceID,
					"source_workspace_name":     capability.SourceWorkspaceName,
					"latest_version_id":         capability.LatestVersionID,
					"latest_version":            capability.LatestVersion,
					"latest_version_created_at": capability.LatestVersionCreatedAt,
					"pinned_version_id":         binding.CapabilityVersionID,
					"pinned_version":            capability.Version,
					"created_at":                capability.LatestVersionCreatedAt,
					"updated_at":                capability.LatestVersionCreatedAt,
				},
			})
		}
		// Surface runtime-injected built-ins as installed, default-ON cards.
		// These have no capability_version row; the enabled flag comes from
		// the per-agent override table (absent row = ON).
		for _, b := range builtinCapabilities {
			builtinEnabled, err := runtimeStore.IsBuiltinCapabilityEnabled(r.Context(), agentID, b.Key)
			if err != nil {
				writeCapabilityError(w, err, "failed to read builtin capability flag")
				return
			}
			installedAny = append(installedAny, map[string]any{
				"id":                    "builtin:" + b.Key,
				"agent_id":              agentID,
				"capability_id":         "builtin:" + b.Key,
				"capability_version_id": "",
				"enabled":               builtinEnabled,
				"built_in":              true,
				"builtin_key":           b.Key,
				"configuration":         map[string]any{},
				"capability": map[string]any{
					"id":          "builtin:" + b.Key,
					"type":        b.Type,
					"name":        b.Name,
					"description": b.Description,
					"built_in":    true,
					"builtin_key": b.Key,
				},
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "agent_id": agentID, "installed": installedAny, "available": availableAny, "marketplace_available": marketplace})
	}
}

// enableAgentCapability enables (installs) a capability version on the agent.
// Cross-workspace requires the source capability to be public + non-deprecated.
//
//	@Summary		Enable a capability on an agent
//	@Description	Enables (installs) a capability version on the agent. Cross-workspace enable requires the source capability to be public and non-deprecated. Workspace owner, admin, or member only.
//	@Tags			capabilities
//	@ID				enableDevAgentCapability
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID			path	string					true	"Workspace UUID"
//	@Param			agentID				path	string					true	"Agent UUID"
//	@Param			capabilityVersionID	path	string					true	"Capability version UUID"
//	@Param			body				body	agentCapabilityBody		true	"Configuration + pinning mode"
//	@Success		200 {object} map[string]interface{} "Enabled agent_capability row"
//	@Failure		400 {object} map[string]string "Invalid UUID or malformed body"
//	@Failure		403 {object} map[string]string "Caller is a viewer/non-member, or marketplace capability is unavailable"
//	@Failure		404 {object} map[string]string "Agent, capability, or version not found"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/agents/{agentID}/capabilities/{capabilityVersionID}/enable [post]
func enableAgentCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, agentID, agent, ok := requireAgentWritableMember(w, r, runtimeStore)
		if !ok {
			return
		}
		versionID := strings.TrimSpace(chi.URLParam(r, "capabilityVersionID"))
		if !isUUID(versionID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "capability_version_id must be a valid uuid"})
			return
		}
		version, err := runtimeStore.GetCapabilityVersion(r.Context(), versionID)
		if err != nil {
			writeCapabilityError(w, err, "failed to get capability version")
			return
		}
		capability, err := runtimeStore.GetCapability(r.Context(), version.CapabilityID)
		if err != nil {
			writeCapabilityError(w, err, "failed to get capability")
			return
		}
		// Cross-workspace enable is only allowed for capabilities still
		// on offer in the marketplace: public visibility + not soft-
		// removed via deprecated_at.
		if capability.WorkspaceID != agent.WorkspaceID && (capability.Visibility != "public" || capability.DeprecatedAt != nil) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "marketplace capability is unavailable"})
			return
		}
		var body agentCapabilityBody
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		enabled, err := runtimeStore.EnableAgentCapability(r.Context(), agentID, versionID, body.Configuration, body.PinningMode)
		if err != nil {
			writeCapabilityError(w, err, "failed to enable agent capability")
			return
		}
		writeJSON(w, http.StatusOK, enabled)
	}
}

// deleteAgentCapability uninstalls a capability version from the agent.
//
//	@Summary		Uninstall a capability from an agent
//	@Description	Removes the given capability version from the agent. Workspace owner, admin, or member only.
//	@Tags			capabilities
//	@ID				deleteDevAgentCapability
//	@Produce		json
//	@Param			workspaceID			path	string	true	"Workspace UUID"
//	@Param			agentID				path	string	true	"Agent UUID"
//	@Param			capabilityVersionID	path	string	true	"Capability version UUID"
//	@Success		204 "Uninstalled"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is a viewer or non-member"
//	@Failure		404 {object} map[string]string "Agent capability not found"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/agents/{agentID}/capabilities/{capabilityVersionID} [delete]
func deleteAgentCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, agentID, _, ok := requireAgentWritableMember(w, r, runtimeStore)
		if !ok {
			return
		}
		versionID := strings.TrimSpace(chi.URLParam(r, "capabilityVersionID"))
		if !isUUID(versionID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "capability_version_id must be a valid uuid"})
			return
		}
		if err := runtimeStore.DeleteAgentCapability(r.Context(), agentID, versionID); err != nil {
			writeCapabilityError(w, err, "failed to delete agent capability")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// setBuiltinCapability toggles a runtime-injected built-in on/off for one agent.
// Any non-viewer workspace member may change it; the key is validated against the built-in
// registry so callers can't create arbitrary rows.
//
//	@Summary		Toggle a built-in capability on an agent
//	@Description	Toggles a runtime-injected built-in on/off for one agent. Workspace owners, admins, and members may change it. The key must be a known built-in.
//	@Tags			capabilities
//	@ID				setDevAgentBuiltinCapability
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string					true	"Workspace UUID"
//	@Param			agentID		path	string					true	"Agent UUID"
//	@Param			key			path	string					true	"Built-in capability key (e.g. parsar_chat_history)"
//	@Param			body		body	builtinCapabilityBody	true	"Enabled flag"
//	@Success		200 {object} map[string]interface{} "agent_id, builtin_key, enabled"
//	@Failure		400 {object} map[string]string "Invalid UUID or malformed body"
//	@Failure		403 {object} map[string]string "Caller is a viewer or non-member"
//	@Failure		404 {object} map[string]string "Unknown builtin key or agent not found"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/agents/{agentID}/builtin-capabilities/{key} [put]
func setBuiltinCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, agentID, _, ok := requireAgentWritableMember(w, r, runtimeStore)
		if !ok {
			return
		}
		key := strings.TrimSpace(chi.URLParam(r, "key"))
		if _, known := lookupBuiltinCapability(key); !known {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown builtin capability"})
			return
		}
		var body builtinCapabilityBody
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if err := runtimeStore.SetBuiltinCapabilityEnabled(r.Context(), agentID, key, body.Enabled); err != nil {
			writeCapabilityError(w, err, "failed to set builtin capability")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agent_id": agentID, "builtin_key": key, "enabled": body.Enabled})
	}
}

// upgradeAgentCapability swaps the agent's binding to a new version of the
// same capability, optionally flipping the pinning mode at the same time.
//
//	@Summary		Upgrade an agent's capability version
//	@Description	Swaps the agent's binding to a new version of the same capability, optionally flipping the pinning mode at the same time. Workspace owner, admin, or member only.
//	@Tags			capabilities
//	@ID				upgradeDevAgentCapability
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID		path	string							true	"Workspace UUID"
//	@Param			agentID			path	string							true	"Agent UUID"
//	@Param			capabilityID	path	string							true	"Capability UUID"
//	@Param			body			body	upgradeAgentCapabilityBody		true	"new_version_id and optional pinning_mode"
//	@Success		200 {object} map[string]interface{} "Updated agent_capability row"
//	@Failure		400 {object} map[string]string "Invalid UUID or malformed body"
//	@Failure		403 {object} map[string]string "Caller is a viewer or non-member"
//	@Failure		404 {object} map[string]string "Agent, capability, or version not found"
//	@Failure		503 {object} map[string]string "Database-backed capability APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/agents/{agentID}/capabilities/{capabilityID}/upgrade [post]
func upgradeAgentCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, agentID, _, ok := requireAgentWritableMember(w, r, runtimeStore)
		if !ok {
			return
		}
		capabilityID := strings.TrimSpace(chi.URLParam(r, "capabilityID"))
		if !isUUID(capabilityID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "capability_id must be a valid uuid"})
			return
		}
		var body upgradeAgentCapabilityBody
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if !isUUID(body.NewVersionID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "new_version_id must be a valid uuid"})
			return
		}
		upgraded, err := runtimeStore.UpgradeAgentCapability(r.Context(), agentID, capabilityID, body.NewVersionID, body.PinningMode)
		if err != nil {
			writeCapabilityError(w, err, "failed to upgrade agent capability")
			return
		}
		writeJSON(w, http.StatusOK, upgraded)
	}
}

func requireWorkspaceCapabilityRead(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore) (string, bool) {
	if runtimeStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed capability APIs are disabled"})
		return "", false
	}
	workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
	if !isUUID(workspaceID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
		return "", false
	}
	if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
		writeRBACError(w, err)
		return "", false
	}
	return workspaceID, true
}

func requireWorkspaceCapabilityAdmin(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore) (string, bool) {
	if runtimeStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed capability APIs are disabled"})
		return "", false
	}
	workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
	if !isUUID(workspaceID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
		return "", false
	}
	if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
		writeRBACError(w, err)
		return "", false
	}
	return workspaceID, true
}

func requireWorkspaceCapabilityByID(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore, admin bool) (string, string, bool) {
	var workspaceID string
	var ok bool
	if admin {
		workspaceID, ok = requireWorkspaceCapabilityAdmin(w, r, runtimeStore)
	} else {
		workspaceID, ok = requireWorkspaceCapabilityRead(w, r, runtimeStore)
	}
	if !ok {
		return "", "", false
	}
	capabilityID := strings.TrimSpace(chi.URLParam(r, "capabilityID"))
	if !isUUID(capabilityID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "capability_id must be a valid uuid"})
		return "", "", false
	}
	capability, err := runtimeStore.GetCapability(r.Context(), capabilityID)
	if err != nil {
		writeCapabilityError(w, err, "failed to get capability")
		return "", "", false
	}
	if capability.WorkspaceID != workspaceID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "capability not found"})
		return "", "", false
	}
	return workspaceID, capabilityID, true
}

func requireAuthenticatedUser(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore) (string, bool) {
	if runtimeStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed credential APIs are disabled"})
		return "", false
	}
	userID := strings.TrimSpace(auth.UserIDFromContext(r.Context()))
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return "", false
	}
	if !isUUID(userID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id must be a valid uuid"})
		return "", false
	}
	return userID, true
}

func requireAgentMember(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore) (string, string, store.AgentStatusRead, bool) {
	return requireAgentRoles(w, r, runtimeStore, "owner", "admin", "member", "viewer")
}

func requireAgentWritableMember(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore) (string, string, store.AgentStatusRead, bool) {
	return requireAgentRoles(w, r, runtimeStore, "owner", "admin", "member")
}

func requireAgentRoles(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore, roles ...string) (string, string, store.AgentStatusRead, bool) {
	if runtimeStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed capability APIs are disabled"})
		return "", "", store.AgentStatusRead{}, false
	}
	workspaceID, agentID, ok := agentParams(w, r)
	if !ok {
		return "", "", store.AgentStatusRead{}, false
	}
	agent, err := runtimeStore.GetAgentDetail(r.Context(), agentID)
	if err != nil {
		writeCapabilityError(w, err, "failed to get agent")
		return "", "", store.AgentStatusRead{}, false
	}
	if agent.WorkspaceID != workspaceID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return "", "", store.AgentStatusRead{}, false
	}
	if err := auth.RequireWorkspaceRole(requestContextForRBAC(r), runtimeStore, workspaceID, roles...); err != nil {
		writeRBACError(w, err)
		return "", "", store.AgentStatusRead{}, false
	}
	return workspaceID, agentID, agent, true
}

func agentParams(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
	if !isUUID(workspaceID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
		return "", "", false
	}
	agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
	if !isUUID(agentID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
		return "", "", false
	}
	return workspaceID, agentID, true
}

func credentialIDParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	credentialID := strings.TrimSpace(chi.URLParam(r, "credentialID"))
	if !isUUID(credentialID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "credential_id must be a valid uuid"})
		return "", false
	}
	return credentialID, true
}

func decodeBody(r *http.Request, target any) error {
	if r.Body == nil {
		return nil
	}
	err := json.NewDecoder(r.Body).Decode(target)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func encryptCredentialValue(w http.ResponseWriter, value string) ([]byte, bool) {
	secretService, err := secrets.New(os.Getenv("PARSAR_MASTER_KEY"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "secrets service unavailable: " + err.Error()})
		return nil, false
	}
	encrypted, err := secretService.Encrypt(map[string]any{"value": strings.TrimSpace(value)})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to encrypt credential"})
		return nil, false
	}
	return encrypted, true
}

func credentialResponses(credentials []store.UserCredentialRead) []userCredentialResponse {
	out := make([]userCredentialResponse, 0, len(credentials))
	for _, credential := range credentials {
		out = append(out, credentialResponse(credential))
	}
	return out
}

func credentialResponse(credential store.UserCredentialRead) userCredentialResponse {
	return userCredentialResponse{ID: credential.ID, Kind: credential.Kind, DisplayName: credential.DisplayName, LastUsedAt: credential.LastUsedAt, CreatedAt: credential.CreatedAt, UpdatedAt: credential.UpdatedAt}
}

func rejectPlaintextSecretsInCapabilityVersions(versions []store.CapabilityVersionRead) error {
	for _, version := range versions {
		raw, err := json.Marshal(version.Content)
		if err != nil {
			return fmt.Errorf("capability version %s content is invalid", version.ID)
		}
		text := string(raw)
		for _, secretPattern := range plaintextSecretPatterns {
			if secretPattern.pattern.MatchString(text) {
				return fmt.Errorf("capability version %s contains plaintext secret pattern: %s", version.ID, secretPattern.name)
			}
		}
	}
	return nil
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func capabilityVisibility(visibility, scope string) string {
	if strings.TrimSpace(visibility) != "" {
		return visibility
	}
	if strings.TrimSpace(scope) == "public" {
		return "public"
	}
	return scope
}

func capabilityVisibilityPtr(visibility, scope *string) *string {
	if visibility != nil {
		return visibility
	}
	if scope == nil {
		return nil
	}
	value := capabilityVisibility("", *scope)
	return &value
}

func writeCapabilityError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, store.ErrUnknownCapability), errors.Is(err, store.ErrUnknownCapabilityVersion), errors.Is(err, store.ErrUnknownUserCredential), errors.Is(err, store.ErrUnknownAgentCapability), errors.Is(err, store.ErrUnknownAgent):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrImmutable):
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrMarketplaceCapabilityUnavailable):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrInvalidWorkspaceInput), errors.Is(err, store.ErrInvalidInput), errors.Is(err, store.ErrInvalidAgent), errors.Is(err, store.ErrInvalidCredentialKind):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fallback})
	}
}

// validateCanonicalSpecForType parses a canonical_spec body and runs its
// Validate. Empty input is allowed (legacy paths still use Content). When
// capabilityType is non-empty it must match canonical_spec.kind so a
// caller can't smuggle a skill spec into a system_prompt capability row.
func validateCanonicalSpecForType(capabilityType string, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var spec canonical.Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("canonical_spec decode: %w", err)
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	if t := strings.TrimSpace(capabilityType); t != "" && t != string(spec.Kind) {
		return fmt.Errorf("canonical_spec.kind=%q does not match capability type=%q", spec.Kind, t)
	}
	return nil
}
