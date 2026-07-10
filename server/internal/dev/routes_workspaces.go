package dev

import (
	"encoding/json"
	"errors"
	"io"

	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

// getWorkspaceSettings returns workspace-level settings.
//
//	@Summary		Get workspace settings
//	@Description	Returns workspace-level settings. Caller must be a workspace member.
//	@Tags			workspaces
//	@ID				getDevWorkspaceSettings
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Success		200 {object} map[string]interface{} "Workspace settings"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not a workspace member"
//	@Router			/api/v1/workspaces/{workspaceID}/settings [get]
func getWorkspaceSettings(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if runtimeStore == nil || !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		result, err := runtimeStore.GetWorkspaceSettings(r.Context(), workspaceID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// patchWorkspaceSettings applies a partial update to workspace settings.
//
//	@Summary		Update workspace settings
//	@Description	Partially updates workspace-level settings. Owner/admin only.
//	@Tags			workspaces
//	@ID				patchDevWorkspaceSettings
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string					true	"Workspace UUID"
//	@Param			body		body	map[string]interface{}	true	"Settings patch"
//	@Success		200 {object} map[string]interface{} "Updated settings"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/settings [patch]
func patchWorkspaceSettings(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if runtimeStore == nil || !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		if _, err := decodeJSONWithFields(r, &struct{}{}); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		result, err := runtimeStore.PatchWorkspaceSettings(r.Context(), workspaceID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// ============================================================
// Workspace-dimension IM connectors (feishu / slack / discord)
// ------------------------------------------------------------
// Bound to the workspace, NOT to an agent: the same dimension as the
// /list a user picks an agent from after @-summoning the shared bot.
// Credentials live in the vault; the table stores only *_ref UUIDs +
// non-secret fields. RBAC is enforced by the route gate middleware
// (member for GET, owner/admin for PATCH), so the handlers trust the
// URL workspaceID.
// ============================================================

type updateWorkspaceSlackConnectorBody struct {
	Enabled          bool   `json:"enabled"`
	AppID            string `json:"app_id"`
	BotTokenRef      string `json:"bot_token_ref"`
	AppTokenRef      string `json:"app_token_ref"`
	SigningSecretRef string `json:"signing_secret_ref"`
	EventMode        string `json:"event_mode"`
}

type updateWorkspaceDiscordConnectorBody struct {
	Enabled      bool   `json:"enabled"`
	AppID        string `json:"app_id"`
	BotTokenRef  string `json:"bot_token_ref"`
	PublicKeyRef string `json:"public_key_ref"`
	Intents      string `json:"intents"`
}

type updateWorkspaceTeamsConnectorBody struct {
	Enabled        bool   `json:"enabled"`
	AppID          string `json:"app_id"`
	AppPasswordRef string `json:"app_password_ref"`
	TenantID       string `json:"tenant_id"`
}

type updateWorkspaceFeishuConnectorBody struct {
	Enabled              bool   `json:"enabled"`
	AppID                string `json:"app_id"`
	AppSecretRef         string `json:"app_secret_ref"`
	VerificationTokenRef string `json:"verification_token_ref"`
	EncryptKeyRef        string `json:"encrypt_key_ref"`
	BotOpenID            string `json:"bot_open_id"`
	EventMode            string `json:"event_mode"`
}

// listWorkspaceConnectors returns all platforms' connector rows for the
// workspace, decoded (config jsonb → map). Backs the admin panel's
// initial state. Never exposes secret plaintext (only *_ref UUIDs).
// listWorkspaceConnectors lists the workspace's configured connectors.
//
//	@Summary		List workspace connectors
//	@Description	Returns the workspace's configured connectors. Owner/admin only.
//	@Tags			workspaces
//	@ID				listDevWorkspaceConnectors
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Success		200 {object} map[string]interface{} "Connector list"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/connectors [get]
func listWorkspaceConnectors(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		connectors, err := runtimeStore.GetWorkspaceIMConnectors(r.Context(), workspaceID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"connectors": connectors})
	}
}

// updateWorkspaceSlackConnector updates the workspace-level Slack connector.
//
//	@Summary		Update the workspace's Slack connector
//	@Description	Updates the workspace-level Slack connector credentials/config. Owner/admin only.
//	@Tags			workspaces
//	@ID				updateDevWorkspaceSlackConnector
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string								true	"Workspace UUID"
//	@Param			body		body	updateWorkspaceSlackConnectorBody	true	"Connector config"
//	@Success		200 {object} map[string]interface{} "Updated connector"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/connector/slack [patch]
func updateWorkspaceSlackConnector(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		var req updateWorkspaceSlackConnectorBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		change, err := runtimeStore.UpsertWorkspaceSlackConnector(r.Context(), store.UpsertWorkspaceSlackConnectorInput{
			WorkspaceID:      workspaceID,
			Enabled:          req.Enabled,
			AppID:            req.AppID,
			BotTokenRef:      req.BotTokenRef,
			AppTokenRef:      req.AppTokenRef,
			SigningSecretRef: req.SigningSecretRef,
			EventMode:        req.EventMode,
		}, actorIDFromRequest(r))
		if err != nil {
			switch {
			case errors.Is(err, store.ErrSlackConnectorIncomplete):
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "slack_connector_incomplete", "detail": err.Error()})
				return
			case errors.Is(err, store.ErrSlackAppIDInUse):
				writeJSON(w, http.StatusConflict, map[string]string{"error": "slack_app_id_in_use", "detail": err.Error()})
				return
			}
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"connector": change})
	}
}

// updateWorkspaceDiscordConnector updates the workspace-level Discord connector.
//
//	@Summary		Update the workspace's Discord connector
//	@Description	Updates the workspace-level Discord connector credentials/config. Owner/admin only.
//	@Tags			workspaces
//	@ID				updateDevWorkspaceDiscordConnector
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string								true	"Workspace UUID"
//	@Param			body		body	updateWorkspaceDiscordConnectorBody	true	"Connector config"
//	@Success		200 {object} map[string]interface{} "Updated connector"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/connector/discord [patch]
func updateWorkspaceDiscordConnector(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		var req updateWorkspaceDiscordConnectorBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		change, err := runtimeStore.UpsertWorkspaceDiscordConnector(r.Context(), store.UpsertWorkspaceDiscordConnectorInput{
			WorkspaceID:  workspaceID,
			Enabled:      req.Enabled,
			AppID:        req.AppID,
			BotTokenRef:  req.BotTokenRef,
			PublicKeyRef: req.PublicKeyRef,
			Intents:      req.Intents,
		}, actorIDFromRequest(r))
		if err != nil {
			switch {
			case errors.Is(err, store.ErrDiscordConnectorIncomplete):
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "discord_connector_incomplete", "detail": err.Error()})
				return
			case errors.Is(err, store.ErrDiscordAppIDInUse):
				writeJSON(w, http.StatusConflict, map[string]string{"error": "discord_app_id_in_use", "detail": err.Error()})
				return
			}
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"connector": change})
	}
}

// updateWorkspaceTeamsConnector updates the workspace-level Teams connector.
//
//	@Summary		Update the workspace's Microsoft Teams connector
//	@Description	Updates the workspace-level Teams connector credentials/config. Owner/admin only.
//	@Tags			workspaces
//	@ID				updateDevWorkspaceTeamsConnector
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string								true	"Workspace UUID"
//	@Param			body		body	updateWorkspaceTeamsConnectorBody	true	"Connector config"
//	@Success		200 {object} map[string]interface{} "Updated connector"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/connector/teams [patch]
func updateWorkspaceTeamsConnector(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		var req updateWorkspaceTeamsConnectorBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		change, err := runtimeStore.UpsertWorkspaceTeamsConnector(r.Context(), store.UpsertWorkspaceTeamsConnectorInput{
			WorkspaceID:    workspaceID,
			Enabled:        req.Enabled,
			AppID:          req.AppID,
			AppPasswordRef: req.AppPasswordRef,
			TenantID:       req.TenantID,
		}, actorIDFromRequest(r))
		if err != nil {
			switch {
			case errors.Is(err, store.ErrTeamsConnectorIncomplete):
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "teams_connector_incomplete", "detail": err.Error()})
				return
			case errors.Is(err, store.ErrTeamsAppIDInUse):
				writeJSON(w, http.StatusConflict, map[string]string{"error": "teams_app_id_in_use", "detail": err.Error()})
				return
			}
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"connector": change})
	}
}

// updateWorkspaceFeishuConnector updates the workspace-level Feishu connector.
//
//	@Summary		Update the workspace's Feishu connector
//	@Description	Updates the workspace-level Feishu connector credentials/config. Owner/admin only.
//	@Tags			workspaces
//	@ID				updateDevWorkspaceFeishuConnector
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string								true	"Workspace UUID"
//	@Param			body		body	updateWorkspaceFeishuConnectorBody	true	"Connector config"
//	@Success		200 {object} map[string]interface{} "Updated connector"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/connector/feishu [patch]
func updateWorkspaceFeishuConnector(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		var req updateWorkspaceFeishuConnectorBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		botOpenID, err := resolveFeishuBotOpenID(r.Context(), runtimeStore, workspaceID, req.AppID, req.AppSecretRef, req.BotOpenID)
		if err != nil {
			log.Bg().Warn("feishu bot open_id auto-resolve failed", "workspace_id", workspaceID, "app_id", req.AppID, "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_bot_open_id_resolve_failed", "detail": err.Error()})
			return
		}
		change, err := runtimeStore.UpsertWorkspaceFeishuConnector(r.Context(), store.UpsertWorkspaceFeishuConnectorInput{
			WorkspaceID:          workspaceID,
			Enabled:              req.Enabled,
			AppID:                req.AppID,
			AppSecretRef:         req.AppSecretRef,
			VerificationTokenRef: req.VerificationTokenRef,
			EncryptKeyRef:        req.EncryptKeyRef,
			BotOpenID:            botOpenID,
			EventMode:            req.EventMode,
		}, actorIDFromRequest(r))
		if err != nil {
			switch {
			case errors.Is(err, store.ErrFeishuConnectorIncomplete):
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "feishu_connector_incomplete", "detail": err.Error()})
				return
			case errors.Is(err, store.ErrFeishuAppIDInUse):
				writeJSON(w, http.StatusConflict, map[string]string{"error": "feishu_app_id_in_use", "detail": err.Error()})
				return
			}
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"connector": change})
	}
}

// listWorkspaceAuditRecords serves /workspaces/{workspaceID}/audit-records.
// It reads the unified audit_records table (5-category source taxonomy,
// jsonb payload). Optional query filters: source, event_type,
// target_type, target_id, actor_id.
// listWorkspaceAuditRecords lists audit-log entries for a workspace.
//
//	@Summary		List workspace audit records
//	@Description	Returns audit-log entries for the workspace. Owner/admin only.
//	@Tags			workspaces
//	@ID				listDevWorkspaceAuditRecords
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Success		200 {object} map[string]interface{} "Audit records"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/audit-records [get]
func listWorkspaceAuditRecords(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}

		q := r.URL.Query()
		filter := store.ListAuditRecordsFilter{
			WorkspaceID: workspaceID,
			Source:      strings.TrimSpace(q.Get("source")),
			EventType:   strings.TrimSpace(q.Get("event_type")),
			ActorID:     strings.TrimSpace(q.Get("actor_id")),
			TargetType:  strings.TrimSpace(q.Get("target_type")),
			TargetID:    strings.TrimSpace(q.Get("target_id")),
		}
		records, err := runtimeStore.ListAuditRecords(r.Context(), filter, parseLimit(r, 100))
		if err != nil {
			writeReadError(w, err, "failed to list workspace audit records")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"workspace_id":  workspaceID,
			"source":        filter.Source,
			"event_type":    filter.EventType,
			"target_type":   filter.TargetType,
			"audit_records": records,
		})
	}
}

// listWorkspaceConnectorUsage aggregates connector types in use by
// agents in a workspace. There is no `connectors` table — the
// connector identity lives on each agent's `connector_type` field.
// listWorkspaceConnectorUsage returns connector usage rows.
//
//	@Summary		List workspace connector usage
//	@Description	Returns per-connector usage rows for the workspace. Owner/admin only.
//	@Tags			workspaces
//	@ID				listDevWorkspaceConnectorUsage
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Success		200 {object} map[string]interface{} "Connector usage rows"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/connector-usage [get]
func listWorkspaceConnectorUsage(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}

		agents, err := runtimeStore.ListWorkspaceAgentsForAdmin(r.Context(), workspaceID)
		if err != nil {
			writeReadError(w, err, "failed to list workspace connectors")
			return
		}

		type connectorRow struct {
			ConnectorType string   `json:"connector_type"`
			Label         string   `json:"label"`
			Status        string   `json:"status"`
			AgentCount    int      `json:"agent_count"`
			AgentSlugs    []string `json:"agent_slugs"`
		}
		bucket := map[string]*connectorRow{}
		for _, a := range agents {
			ct := strings.TrimSpace(a.ConnectorType)
			if ct == "" {
				ct = "unknown"
			}
			row, ok := bucket[ct]
			if !ok {
				row = &connectorRow{
					ConnectorType: ct,
					Label:         connectorLabel(ct),
					Status:        connectorStatus(ct),
					AgentSlugs:    []string{},
				}
				bucket[ct] = row
			}
			row.AgentCount++
			row.AgentSlugs = append(row.AgentSlugs, a.Slug)
		}

		out := make([]connectorRow, 0, len(bucket))
		for _, row := range bucket {
			out = append(out, *row)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].AgentCount != out[j].AgentCount {
				return out[i].AgentCount > out[j].AgentCount
			}
			return out[i].ConnectorType < out[j].ConnectorType
		})

		writeJSON(w, http.StatusOK, map[string]any{
			"workspace_id": workspaceID,
			"connectors":   out,
		})
	}
}

// connectorLabel maps the raw connector_type to a UI-friendly label.
func connectorLabel(t string) string {
	switch t {
	case "agent_daemon":
		return "Agent Daemon"
	case "http-agent", "http":
		return "HTTP Agent"
	default:
		return t
	}
}

// connectorStatus is a coarse health hint. Per-run failures show up
// in Run Detail. Future connectors needing external setup can return
// "needs_config" / "offline".
func connectorStatus(t string) string {
	switch t {
	case "agent_daemon", "http-agent", "http":
		return "ready"
	default:
		return "unknown"
	}
}

// listWorkspaceGateways returns a static registry of known gateway types.
// No DB schema — connectors don't have one either.
// listWorkspaceGateways lists inbound/outbound gateway rows.
//
//	@Summary		List workspace gateways
//	@Description	Returns gateway inbound/outbound rows for the workspace. Owner/admin only.
//	@Tags			workspaces
//	@ID				listDevWorkspaceGateways
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Success		200 {object} map[string]interface{} "Gateway rows"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID}/gateways [get]
func listWorkspaceGateways() http.HandlerFunc {
	type gatewayRow struct {
		Type        string `json:"type"`
		Label       string `json:"label"`
		Status      string `json:"status"`
		Phase       string `json:"phase"`
		Description string `json:"description"`
	}
	registry := []gatewayRow{
		{
			Type:        "dev",
			Label:       "Dev Gateway",
			Status:      "active",
			Phase:       "phase_1",
			Description: "Built-in dev gateway/inbound entry point used by the devgateway tool and E2E tests.",
		},
		{
			Type:        "feishu",
			Label:       "Feishu",
			Status:      "not_configured",
			Phase:       "phase_3",
			Description: "Feishu group / thread gateway with real webhook signature + OAuth.",
		},
		{
			Type:        "slack",
			Label:       "Slack",
			Status:      "not_configured",
			Phase:       "phase_3",
			Description: "Slack channel + thread gateway.",
		},
		{
			Type:        "web",
			Label:       "Web",
			Status:      "active",
			Phase:       "phase_1",
			Description: "Built-in web entrypoint for conversations created from the admin UI.",
		},
	}

	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"workspace_id": workspaceID,
			"gateways":     registry,
		})
	}
}

type createWorkspaceRequest struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility,omitempty"` // "public" / "private"; empty → server defaults to "private"
}

// createWorkspace creates a new workspace with the caller as owner.
//
//	@Summary		Create a workspace
//	@Description	Creates a new workspace and adds the caller as the initial owner.
//	@Tags			workspaces
//	@ID				createDevWorkspace
//	@Accept			json
//	@Produce		json
//	@Param			body	body	createWorkspaceRequest	true	"Workspace payload"
//	@Success		201 {object} map[string]interface{} "Created workspace"
//	@Failure		400 {object} map[string]string "Invalid body"
//	@Failure		401 {object} map[string]string "Caller is not authenticated"
//	@Router			/api/v1/workspaces [post]
func createWorkspace(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		var body createWorkspaceRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		result, err := runtimeStore.CreateWorkspace(r.Context(), store.CreateWorkspaceInput{
			Name:       body.Name,
			Visibility: strings.TrimSpace(body.Visibility),
			CreatedBy:  actorID,
			Now:        time.Now().UTC(),
		})
		if err != nil {
			writeReadError(w, err, "failed to create workspace")
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"workspace": result.Workspace,
			"member":    result.Member,
		})
	}
}

type updateWorkspaceRequest struct {
	Name       *string `json:"name,omitempty"`
	Visibility *string `json:"visibility,omitempty"` // "public" / "private"; nil → unchanged
}

// updateWorkspace applies a partial update to a workspace.
//
//	@Summary		Update a workspace
//	@Description	Partially updates a workspace's mutable fields. Owner/admin only.
//	@Tags			workspaces
//	@ID				updateDevWorkspace
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string					true	"Workspace UUID"
//	@Param			body		body	updateWorkspaceRequest	true	"Workspace update payload"
//	@Success		200 {object} map[string]interface{} "Updated workspace"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Router			/api/v1/workspaces/{workspaceID} [patch]
func updateWorkspace(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		var body updateWorkspaceRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		row, err := runtimeStore.UpdateWorkspace(r.Context(), store.UpdateWorkspaceInput{
			WorkspaceID: workspaceID,
			Name:        body.Name,
			Visibility:  body.Visibility,
			ActorID:     actorID,
			Now:         time.Now().UTC(),
		})
		if err != nil {
			writeReadError(w, err, "failed to update workspace")
			return
		}
		writeJSON(w, http.StatusOK, row)
	}
}

// archiveWorkspace marks a workspace as archived.
//
//	@Summary		Archive a workspace
//	@Description	Marks the workspace as archived. Owner only.
//	@Tags			workspaces
//	@ID				archiveDevWorkspace
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Success		200 {object} map[string]interface{} "Archived workspace"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner"
//	@Router			/api/v1/workspaces/{workspaceID}/archive [post]
func archiveWorkspace(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		row, err := runtimeStore.ArchiveWorkspace(r.Context(), store.ArchiveWorkspaceInput{
			WorkspaceID: workspaceID,
			ActorID:     actorID,
			Now:         time.Now().UTC(),
		})
		if err != nil {
			writeReadError(w, err, "failed to archive workspace")
			return
		}
		writeJSON(w, http.StatusOK, row)
	}
}

func listWorkspaceUsageLogs(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed read APIs are disabled"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		if !isUUID(workspaceID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		agentRunID := strings.TrimSpace(r.URL.Query().Get("agent_run_id"))
		if agentRunID != "" && !isUUID(agentRunID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_run_id must be a valid uuid"})
			return
		}

		usage, err := runtimeStore.ListWorkspaceUsageLogs(r.Context(), workspaceID, agentRunID, parseLimit(r, 100))
		if err != nil {
			writeReadError(w, err, "failed to list workspace usage")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "agent_run_id": agentRunID, "usage_logs": usage})
	}
}
