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

	"github.com/go-chi/chi/v5"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
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

type uninstallMarketplaceBody struct {
	SourceCapabilityID string `json:"source_capability_id"`
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
		typeFilter := strings.TrimSpace(r.URL.Query().Get("type"))
		nameFilter := strings.TrimSpace(r.URL.Query().Get("name"))
		caps, err := runtimeStore.ListCapabilities(r.Context(), workspaceID, store.ListCapabilityFilter{Type: typeFilter, Visibility: visibility, Name: nameFilter})
		if err != nil {
			writeCapabilityError(w, err, "failed to list capabilities")
			return
		}
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
			OwnIndex     int // -1 when this row is a marketplace install
			MarketIndex  int // -1 when this row is an own capability
			Name         string
			CreatedAt    time.Time
			IsInstall    bool
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
		writeJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "capabilities": capabilities})
	}
}

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

func publishWorkspaceCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return marketplaceStateCapability(runtimeStore, "publish")
}

func unpublishWorkspaceCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return marketplaceStateCapability(runtimeStore, "unpublish")
}

func deprecateWorkspaceCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return marketplaceStateCapability(runtimeStore, "deprecate")
}

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

func deleteWorkspaceCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, capabilityID, ok := requireWorkspaceCapabilityByID(w, r, runtimeStore, true)
		if !ok {
			return
		}
		// 原子写:SoftDeleteCapability 的 SQL 里带 NOT EXISTS(agent_capabilities)
		// 守卫,所以"查空 binding → 别人插一条 → 我们删"这种 TOCTOU 窗口被关掉。
		// store 在 UPDATE 0 行时把"是否被引用"的判别也做了——有引用 → 返回
		// CapabilityHasBindingsError(带 Count),没有 → ErrUnknownCapability。
		deleted, err := runtimeStore.SoftDeleteCapability(r.Context(), workspaceID, capabilityID)
		if err != nil {
			var bound *store.CapabilityHasBindingsError
			if errors.As(err, &bound) {
				writeJSON(w, http.StatusConflict, map[string]any{
					"error":         "capability_in_use",
					"message":       fmt.Sprintf("该能力仍被 %d 个 agent 使用,请先在使用方解绑后再删除。", bound.Count),
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

func listProjectAgentCapabilities(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID, projectAgentID, agent, ok := requireProjectAgentMember(w, r, runtimeStore)
		if !ok {
			return
		}
		installed, err := runtimeStore.ListAgentCapabilities(r.Context(), projectAgentID)
		if err != nil {
			writeCapabilityError(w, err, "failed to list agent capabilities")
			return
		}
		enabledCapabilities, err := runtimeStore.GetEnabledMarketplaceCapabilitiesForAgent(r.Context(), projectAgentID)
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
				"project_agent_id":      binding.ProjectAgentID,
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
		writeJSON(w, http.StatusOK, map[string]any{"project_id": projectID, "project_agent_id": projectAgentID, "installed": installedAny, "available": availableAny, "marketplace_available": marketplace})
	}
}

func enableProjectAgentCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, projectAgentID, agent, ok := requireProjectAgentOwnerMember(w, r, runtimeStore)
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
		enabled, err := runtimeStore.EnableAgentCapability(r.Context(), projectAgentID, versionID, body.Configuration, body.PinningMode)
		if err != nil {
			writeCapabilityError(w, err, "failed to enable agent capability")
			return
		}
		writeJSON(w, http.StatusOK, enabled)
	}
}

func deleteProjectAgentCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, projectAgentID, _, ok := requireProjectAgentOwnerMember(w, r, runtimeStore)
		if !ok {
			return
		}
		versionID := strings.TrimSpace(chi.URLParam(r, "capabilityVersionID"))
		if !isUUID(versionID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "capability_version_id must be a valid uuid"})
			return
		}
		if err := runtimeStore.DeleteAgentCapability(r.Context(), projectAgentID, versionID); err != nil {
			writeCapabilityError(w, err, "failed to delete agent capability")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func upgradeProjectAgentCapability(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, projectAgentID, _, ok := requireProjectAgentOwnerMember(w, r, runtimeStore)
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
		upgraded, err := runtimeStore.UpgradeAgentCapability(r.Context(), projectAgentID, capabilityID, body.NewVersionID, body.PinningMode)
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

func requireProjectAgentMember(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore) (string, string, store.ProjectAgentStatusRead, bool) {
	if runtimeStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed capability APIs are disabled"})
		return "", "", store.ProjectAgentStatusRead{}, false
	}
	projectID, projectAgentID, ok := projectAgentParams(w, r)
	if !ok {
		return "", "", store.ProjectAgentStatusRead{}, false
	}
	agent, err := runtimeStore.GetProjectAgentDetail(r.Context(), projectAgentID)
	if err != nil {
		writeCapabilityError(w, err, "failed to get project agent")
		return "", "", store.ProjectAgentStatusRead{}, false
	}
	if agent.ProjectID != projectID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "project agent not found"})
		return "", "", store.ProjectAgentStatusRead{}, false
	}
	if err := requireWorkspaceMemberByProject(r, runtimeStore, projectID); err != nil {
		writeRBACError(w, err)
		return "", "", store.ProjectAgentStatusRead{}, false
	}
	return projectID, projectAgentID, agent, true
}

func requireProjectAgentOwnerMember(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore) (string, string, store.ProjectAgentStatusRead, bool) {
	projectID, projectAgentID, agent, ok := requireProjectAgentMember(w, r, runtimeStore)
	if !ok {
		return "", "", store.ProjectAgentStatusRead{}, false
	}
	userID := strings.TrimSpace(auth.UserIDFromContext(requestContextForRBAC(r)))
	if strings.TrimSpace(agent.CreatedBy) != "" && agent.CreatedBy != userID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return "", "", store.ProjectAgentStatusRead{}, false
	}
	return projectID, projectAgentID, agent, true
}

func projectAgentParams(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	projectID := strings.TrimSpace(chi.URLParam(r, "projectID"))
	if !isUUID(projectID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_id must be a valid uuid"})
		return "", "", false
	}
	projectAgentID := strings.TrimSpace(chi.URLParam(r, "projectAgentID"))
	if !isUUID(projectAgentID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "project_agent_id must be a valid uuid"})
		return "", "", false
	}
	return projectID, projectAgentID, true
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
	case errors.Is(err, store.ErrUnknownCapability), errors.Is(err, store.ErrUnknownCapabilityVersion), errors.Is(err, store.ErrUnknownUserCredential), errors.Is(err, store.ErrUnknownAgentCapability), errors.Is(err, store.ErrUnknownProjectAgent):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrImmutable):
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrMarketplaceCapabilityUnavailable):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrInvalidWorkspaceInput), errors.Is(err, store.ErrInvalidProjectInput), errors.Is(err, store.ErrInvalidProjectAgent), errors.Is(err, store.ErrInvalidCredentialKind):
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
