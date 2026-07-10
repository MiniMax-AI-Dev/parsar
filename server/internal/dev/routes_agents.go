package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type configureAgentConnectorBody struct {
	ConnectorType string `json:"connector_type"`
	Endpoint      string `json:"endpoint"`
	SecretID      string `json:"secret_id"`
	Model         string `json:"model"`
	ModelID       string `json:"model_id"`
	Workdir       string `json:"workdir"`
	SystemPrompt  string `json:"system_prompt"`
}

type configureAgentProfileBody struct {
	ModelID      string         `json:"model_id"`
	Workdir      string         `json:"workdir"`
	SystemPrompt string         `json:"system_prompt"`
	Config       map[string]any `json:"config"`
}

// configureAgentConnector wires an agent to a channel connector.
//
//	@Summary		Configure an agent's connector
//	@Description	Attaches or updates an agent's outbound channel connector configuration. Owner/admin only.
//	@Tags			agents
//	@ID				configureDevAgentConnector
//	@Accept			json
//	@Produce		json
//	@Param			agentID	path	string							true	"Agent UUID"
//	@Param			body	body	configureAgentConnectorBody		true	"Connector config payload"
//	@Success		200 {object} map[string]interface{} "Updated agent"
//	@Failure		400 {object} map[string]string "Invalid request"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Unknown agent"
//	@Router			/api/v1/agents/{agentID}/connector [post]
func configureAgentConnector(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed connector config is disabled"})
			return
		}
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		workspaceID, ok := workspaceIDForAgent(w, r.Context(), runtimeStore, agentID)
		if !ok {
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}

		var req configureAgentConnectorBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(req.ConnectorType) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "connector_type is required"})
			return
		}
		if req.ConnectorType == "http" && !isSafeHTTPAgentEndpoint(req.Endpoint) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "http connector endpoint must be an http(s) URL"})
			return
		}

		result, err := runtimeStore.ConfigureDevAgentConnector(r.Context(), store.ConfigureDevAgentConnectorInput{
			AgentID:       agentID,
			ConnectorType: req.ConnectorType,
			Endpoint:      req.Endpoint,
			SecretID:      req.SecretID,
			Model:         req.Model,
			ModelID:       req.ModelID,
			Workdir:       req.Workdir,
			SystemPrompt:  req.SystemPrompt,
		})
		if err != nil {
			switch {
			case errors.Is(err, store.ErrInvalidConnectorType):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrUnknownAgent):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to configure agent connector"})
			}
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// configureAgentProfile updates the agent's public profile.
//
//	@Summary		Configure an agent's profile
//	@Description	Updates the agent's public profile fields (display name, avatar, description). Owner/admin only.
//	@Tags			agents
//	@ID				configureDevAgentProfile
//	@Accept			json
//	@Produce		json
//	@Param			agentID	path	string						true	"Agent UUID"
//	@Param			body	body	configureAgentProfileBody	true	"Profile payload"
//	@Success		200 {object} map[string]interface{} "Updated agent"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Agent not found"
//	@Router			/api/v1/agents/{agentID}/profile [post]
func configureAgentProfile(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed agent profile config is disabled"})
			return
		}
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		workspaceID, ok := workspaceIDForAgent(w, r.Context(), runtimeStore, agentID)
		if !ok {
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req configureAgentProfileBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		result, err := runtimeStore.ConfigureAgentProfile(r.Context(), store.ConfigureAgentProfileInput{AgentID: agentID, ModelID: req.ModelID, Workdir: req.Workdir, SystemPrompt: req.SystemPrompt, Config: req.Config})
		if err != nil {
			switch {
			case errors.Is(err, store.ErrUnknownAgent):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrUnknownModel):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrModelDisabled):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to configure agent profile"})
			}
			return
		}

		// Local-device binding: mirror createAgent so editing a
		// local-mode agent's bound device keeps the agent's runtime_id in
		// sync. Without this the admin list keeps reading "Runtime not bound".
		if result.AgentConfig != nil {
			if mode, _ := result.AgentConfig["daemon_mode"].(string); mode == "local" {
				if deviceID, _ := result.AgentConfig["device_id"].(string); strings.TrimSpace(deviceID) != "" {
					detail, detailErr := runtimeStore.GetAgentDetail(r.Context(), agentID)
					if detailErr != nil {
						log.Bg().Warn("configureAgentProfile: workspace lookup failed for runtime_id sync",
							"agent_id", agentID, "err", detailErr)
					} else if _, bindErr := runtimeStore.SetAgentRuntime(r.Context(), store.SetAgentRuntimeInput{
						WorkspaceID: detail.WorkspaceID,
						AgentID:     agentID,
						RuntimeID:   deviceID,
					}); bindErr != nil {
						log.Bg().Warn("configureAgentProfile: persist local device runtime_id failed",
							"agent_id", agentID,
							"device_id", deviceID,
							"err", bindErr)
					}
				}
			}
		}

		writeJSON(w, http.StatusOK, result)
	}
}

// disableAgent disables an active agent.
//
//	@Summary		Disable an agent
//	@Description	Marks the agent as disabled so it is no longer scheduled. Owner/admin only.
//	@Tags			agents
//	@ID				disableDevAgent
//	@Produce		json
//	@Param			agentID	path	string	true	"Agent UUID"
//	@Success		200 {object} map[string]interface{} "Updated agent"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Agent not found"
//	@Router			/api/v1/agents/{agentID}/disable [post]
func disableAgent(runtimeStore RuntimeStore) http.HandlerFunc {
	return agentStatusHandler(runtimeStore, "disable")
}

// enableAgent enables a disabled agent.
//
//	@Summary		Enable an agent
//	@Description	Marks a disabled agent as enabled so it can be scheduled again. Owner/admin only.
//	@Tags			agents
//	@ID				enableDevAgent
//	@Produce		json
//	@Param			agentID	path	string	true	"Agent UUID"
//	@Success		200 {object} map[string]interface{} "Updated agent"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Agent not found"
//	@Router			/api/v1/agents/{agentID}/enable [post]
func enableAgent(runtimeStore RuntimeStore) http.HandlerFunc {
	return agentStatusHandler(runtimeStore, "enable")
}

type createAgentCapabilityBody struct {
	CapabilityVersionID string         `json:"capability_version_id"`
	Configuration       map[string]any `json:"configuration"`
	// PinningMode is "latest" or "pinned". Empty falls back to store's
	// default (pinned); create-agent dialog sends "latest" for new
	// bindings unless the user picks a specific version.
	PinningMode string `json:"pinning_mode,omitempty"`
}

// createAgentInlineSecretBody describes one new shared secret the user
// asked to materialise during agent creation. The handler creates the
// secret via store.CreateSecret, then patches its id into
// req.Config.credential_bindings[Kind] (or model_credential_binding when
// IsModel=true) before delegating to runtimeStore.CreateAgent.
type createAgentInlineSecretBody struct {
	Kind        string `json:"kind"`
	IsModel     bool   `json:"is_model"`
	DisplayName string `json:"display_name"`
	Plaintext   string `json:"plaintext"`
}

type createAgentBody struct {
	Name                string                        `json:"name"`
	Description         string                        `json:"description"`
	ConnectorType       string                        `json:"connector_type"`
	SystemPrompt        string                        `json:"system_prompt"`
	DefaultModelID      string                        `json:"default_model_id"`
	Capabilities        []string                      `json:"capabilities"`
	InitialCapabilities []createAgentCapabilityBody   `json:"initial_capabilities"`
	Visibility          string                        `json:"visibility"`
	Runtime             string                        `json:"runtime"`
	Config              map[string]any                `json:"config"`
	InlineNewSecrets    []createAgentInlineSecretBody `json:"inline_new_secrets"`
	Slug                string                        `json:"slug"`
}

type updateAgentBody struct {
	Name             *string                       `json:"name"`
	Description      *string                       `json:"description"`
	ConnectorType    *string                       `json:"connector_type"`
	SystemPrompt     *string                       `json:"system_prompt"`
	DefaultModelID   *string                       `json:"default_model_id"`
	Capabilities     []string                      `json:"capabilities"`
	Config           map[string]any                `json:"config"`
	InlineNewSecrets []createAgentInlineSecretBody `json:"inline_new_secrets"`
	Slug             *string                       `json:"slug"`
	WorkspaceID      *string                       `json:"workspace_id"`
}

// createAgent creates a new agent in a workspace. Owner/admin only.
//
//	@Summary		Create an agent in a workspace
//	@Description	Creates an agent under the given workspace. Owner/admin only. inline_new_secrets are materialised into the shared secret vault before the agent is persisted; any binding entries in config are patched with the resolved ids.
//	@Tags			agents
//	@ID				createDevAgent
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string				true	"Workspace UUID"
//	@Param			body		body	createAgentBody		true	"Agent create payload"
//	@Success		201 {object} map[string]interface{} "Created agent"
//	@Failure		400 {object} map[string]string "workspace_id must be a valid uuid, or body invalid"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		422 {object} map[string]string "Immutable field or unknown capability"
//	@Router			/api/v1/workspaces/{workspaceID}/agents [post]
func createAgent(runtimeStore RuntimeStore, agentDaemonSandbox AgentDaemonSandboxAcquirer) http.HandlerFunc {
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
		var req createAgentBody
		hasCaps, err := decodeJSONWithField(r, &req, "capabilities")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.ConnectorType) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name and connector_type are required"})
			return
		}
		if strings.TrimSpace(req.Runtime) != "" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "runtime is no longer accepted; use config.daemon_mode, config.device_id, and config.agent_kind for agent_daemon agents"})
			return
		}

		// Materialise any inline_new_secrets the user pasted in step 3.
		// Each one becomes a capability_inline secret in the org-global
		// catalog; its id is then patched into the corresponding
		// credential_bindings entry (or model_credential_binding when
		// IsModel=true) inside req.Config so CreateAgent persists a
		// fully-resolved binding map. Failure here is fatal — the agent
		// is not created, the secrets that did succeed are left as
		// orphans (we explicitly chose not to clean them up).
		if cfg, ok := materialiseInlineSecrets(r.Context(), runtimeStore, req.Config, req.InlineNewSecrets, actorIDFromRequest(r)); ok {
			req.Config = cfg
		} else if len(req.InlineNewSecrets) > 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to materialise inline_new_secrets"})
			return
		}

		// Enforce visibility ⇄ binding consistency. Public agents may
		// not depend on any personal credential (no platform user_id
		// for lark guests); tenant agents are allowed but warned in UI.
		// 422 (not 400) so the FE can distinguish "semantically wrong"
		// from "malformed body" — same convention as updateAgent.
		if err := validateAgentVisibilityBindings(req.Visibility, req.Config); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		initialCapabilities := make([]store.InitialAgentCapabilityInput, 0, len(req.InitialCapabilities))
		for _, capability := range req.InitialCapabilities {
			initialCapabilities = append(initialCapabilities, store.InitialAgentCapabilityInput{CapabilityVersionID: capability.CapabilityVersionID, Configuration: capability.Configuration, PinningMode: capability.PinningMode})
		}
		result, err := runtimeStore.CreateAgent(r.Context(), store.CreateAgentInput{WorkspaceID: workspaceID, Name: req.Name, Description: req.Description, ConnectorType: req.ConnectorType, SystemPrompt: req.SystemPrompt, DefaultModelID: req.DefaultModelID, Capabilities: req.Capabilities, CapabilitiesSet: hasCaps, InitialCapabilities: initialCapabilities, Runtime: "", AgentConfig: req.Config, Visibility: req.Visibility, Slug: req.Slug, CreatedBy: actorIDFromRequest(r)})
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)

		// Sync capability checkboxes → agent_capabilities table so the
		// runtime's GetEnabledCapabilitiesForAgent sees them.
		if hasCaps && len(req.Capabilities) > 0 {
			if err := syncAgentCapabilities(r.Context(), runtimeStore, result.Agent.WorkspaceID, result.Agent.ID, req.Capabilities); err != nil {
				log.Bg().Warn("createAgent: capability sync failed", "agent_id", result.Agent.ID, "err", err)
			}
		}

		// Local-device binding: when the user picked a paired daemon
		// in the create form, the device_id sits in agents.config but
		// agents.runtime_id stays NULL. Mirror device_id → runtime_id so
		// the FK join lights up. device_id IS a runtime.id.
		if result.Agent.Config != nil {
			if mode, _ := result.Agent.Config["daemon_mode"].(string); mode == "local" {
				if deviceID, _ := result.Agent.Config["device_id"].(string); strings.TrimSpace(deviceID) != "" {
					if _, bindErr := runtimeStore.SetAgentRuntime(r.Context(), store.SetAgentRuntimeInput{
						WorkspaceID: workspaceID,
						AgentID:     result.Agent.ID,
						RuntimeID:   deviceID,
					}); bindErr != nil {
						// Non-fatal: row is created, the user can
						// re-save from the edit dialog to retry.
						log.Bg().Warn("createAgent: persist local device runtime_id failed",
							"agent_id", result.Agent.ID,
							"device_id", deviceID,
							"err", bindErr)
					}
				}
			}
		}

		// Eager sandbox provisioning: kick off Acquire so the sandbox
		// is ready before the user sends their first message. On
		// success, persist deviceID to agents.runtime_id —
		// without this write the connector's "user must bind a
		// runtime first" guard would reject the very first prompt.
		// Failure is non-fatal: the row is saved and SandboxPanel
		// (or a follow-up Rebuild) gives the admin a recovery surface.
		if agentDaemonSandbox != nil && result.Agent.Config != nil {
			if mode, _ := result.Agent.Config["daemon_mode"].(string); mode == "sandbox" {
				paID := result.Agent.ID
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()
					deviceID, err := agentDaemonSandbox.Acquire(ctx, connector.PromptInput{
						AgentID:     paID,
						WorkspaceID: workspaceID,
					})
					if err != nil {
						log.Bg().Warn("eager sandbox acquire failed",
							"agent_id", paID, "err", err)
						return
					}
					if _, bindErr := runtimeStore.SetAgentRuntime(ctx, store.SetAgentRuntimeInput{
						WorkspaceID: workspaceID,
						AgentID:     paID,
						RuntimeID:   deviceID,
					}); bindErr != nil {
						// Sandbox is alive but runtime_id write failed.
						// Dispatch shows "Runtime not bound" until a retry
						// succeeds or admin Rebuild rewrites.
						log.Bg().Error("eager sandbox acquired but runtime_id persist failed",
							"agent_id", paID,
							"device_id", deviceID,
							"err", bindErr)
						return
					}
					log.Bg().Info("eager sandbox acquired and runtime bound",
						"agent_id", paID, "device_id", deviceID)
				}()
			}
		}
	}
}

// updateAgent applies a partial update to an existing agent.
//
//	@Summary		Update mutable agent fields
//	@Description	Applies a partial update. All fields are optional; nil pointers mean "leave as-is". Slug and runtime are immutable and rejected with 422.
//	@Tags			agents
//	@ID				updateDevAgent
//	@Accept			json
//	@Produce		json
//	@Param			agentID	path	string			true	"Agent UUID"
//	@Param			body	body	updateAgentBody	true	"Partial agent update"
//	@Success		200 {object} map[string]interface{} "Updated agent"
//	@Failure		400 {object} map[string]string "Malformed request body or invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		422 {object} map[string]string "Immutable field (slug/runtime), or unknown capability"
//	@Router			/api/v1/agents/{agentID} [patch]
func updateAgent(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if runtimeStore == nil || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		agent, err := runtimeStore.GetAgent(r.Context(), agentID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, agent.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req updateAgentBody
		fields, err := decodeJSONWithFields(r, &req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		hasCaps := fields["capabilities"]
		if fields["runtime"] {
			// runtime is immutable post-create — recreate the agent to
			// change runtime (it determines whether the agent runs in
			// cloud sandbox or on local subprocess and is tied to
			// every previous conversation's execution environment).
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "runtime is immutable post-create; recreate the agent to change runtime"})
			return
		}
		if req.Slug != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "slug is immutable"})
			return
		}
		if req.WorkspaceID != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "workspace_id is immutable"})
			return
		}
		// Materialise inline secrets + validate visibility ⇄ binding consistency
		// when the FE sends new credential bindings. Without this, edits that
		// switch the shared secret (or introduce a new one) never reach
		// agents.agent_config and the runtime keeps resolving the old binding.
		//
		// Same failure trade-off as createAgent: materialiseInlineSecrets is
		// "commit-each-as-you-go", and a downstream failure (mid-list secret
		// create, or visibility validation below) leaves the earlier secrets
		// dangling in the workspace. Edit makes this slightly worse because
		// users typically retry after fixing the offending field, accruing one
		// orphan per retry. We accept it for symmetry with create; a future
		// pass could wrap the chain in a tx + rollback.
		configChanged := fields["config"] || len(req.InlineNewSecrets) > 0
		if configChanged {
			if cfg, ok := materialiseInlineSecrets(r.Context(), runtimeStore, req.Config, req.InlineNewSecrets, actorIDFromRequest(r)); ok {
				req.Config = cfg
			} else if len(req.InlineNewSecrets) > 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to materialise inline_new_secrets"})
				return
			}
			if err := validateAgentVisibilityBindings(agent.Visibility, req.Config); err != nil {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
				return
			}
		}
		updated, _, err := runtimeStore.UpdateAgent(r.Context(), store.UpdateAgentInput{AgentID: agentID, ActorID: actorIDFromRequest(r), Name: req.Name, Description: req.Description, ConnectorType: req.ConnectorType, SystemPrompt: req.SystemPrompt, DefaultModelID: req.DefaultModelID, Capabilities: req.Capabilities, CapabilitiesSet: hasCaps, Config: req.Config, ConfigSet: configChanged})
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agent": updated})

		// Sync capability checkboxes → agent_capabilities table.
		if hasCaps {
			if err := syncAgentCapabilities(r.Context(), runtimeStore, updated.WorkspaceID, agentID, req.Capabilities); err != nil {
				log.Bg().Warn("updateAgent: capability sync failed", "agent_id", agentID, "err", err)
			}
		}
	}
}

// syncAgentCapabilities reconciles the agent_capabilities table with
// the capability name list from the agent edit form. Errors on
// individual capabilities are logged and skipped.
func syncAgentCapabilities(
	ctx context.Context,
	rs RuntimeStore,
	workspaceID string,
	agentID string,
	capabilityNames []string,
) error {
	// 1. Current state on this agent.
	existing, err := rs.ListAgentCapabilities(ctx, agentID)
	if err != nil {
		return fmt.Errorf("syncAgentCapabilities: list existing: %w", err)
	}
	existingByCapID := make(map[string]store.AgentCapabilityRead, len(existing))
	for _, ac := range existing {
		existingByCapID[ac.CapabilityID] = ac
	}

	// 2. Resolve desired names. A name can come from this workspace's own
	// capabilities, OR from the marketplace (a public capability published
	// by another workspace and surfaced in the agent picker's marketplace
	// section). Local capabilities win on name collision: a user shadowing
	// a marketplace name with a private one should keep using their own.
	allCaps, err := rs.ListCapabilities(ctx, workspaceID, store.ListCapabilityFilter{})
	if err != nil {
		return fmt.Errorf("syncAgentCapabilities: list capabilities: %w", err)
	}
	marketplaceCaps, err := rs.ListMarketplaceCapabilities(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("syncAgentCapabilities: list marketplace capabilities: %w", err)
	}
	type resolved struct {
		capabilityID    string
		latestVersionID string
		fromMarketplace bool
	}
	capByName := make(map[string]resolved, len(allCaps)+len(marketplaceCaps))
	for _, c := range allCaps {
		capByName[c.Name] = resolved{capabilityID: c.ID}
	}
	for _, m := range marketplaceCaps {
		if m.SelfPublished {
			continue
		}
		if _, exists := capByName[m.Name]; exists {
			continue
		}
		capByName[m.Name] = resolved{capabilityID: m.CapabilityID, latestVersionID: m.LatestVersionID, fromMarketplace: true}
	}

	desiredCapIDs := make(map[string]bool, len(capabilityNames))
	for _, name := range capabilityNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		cap, ok := capByName[name]
		if !ok {
			log.Bg().Warn("syncAgentCapabilities: capability not found, skipping",
				"name", name, "workspace_id", workspaceID, "agent_id", agentID)
			continue
		}
		desiredCapIDs[cap.capabilityID] = true

		// Already enabled — don't auto-upgrade version.
		if _, exists := existingByCapID[cap.capabilityID]; exists {
			continue
		}

		latestVersionID := cap.latestVersionID
		if latestVersionID == "" {
			// Local capability: marketplace row already carries the version,
			// but ListCapabilities does not, so we fetch lazily here.
			versions, err := rs.ListCapabilityVersions(ctx, cap.capabilityID)
			if err != nil {
				log.Bg().Warn("syncAgentCapabilities: list versions failed, skipping",
					"capability_id", cap.capabilityID, "name", name, "err", err)
				continue
			}
			if len(versions) == 0 {
				log.Bg().Warn("syncAgentCapabilities: no versions found, skipping",
					"capability_id", cap.capabilityID, "name", name)
				continue
			}
			latestVersionID = versions[0].ID // sorted created_at desc
		}

		// Default pinning_mode depends on the source:
		//   * Local capability: user's expectation is "check it and follow the latest". After reupload,
		//     no need to re-edit the agent, and skill iteration in the local workshop has no
		//     breaking-change risk (owned by the same team).
		//   * Marketplace: the publisher's new version may carry breaking changes,
		//     keep pinned so the UpgradeCapabilityDialog explicit-confirm path remains
		//     valid; the user must explicitly pick latest from the picker to auto-follow.
		mode := store.PinningModeLatest
		if cap.fromMarketplace {
			mode = store.PinningModePinned
		}
		if _, err := rs.EnableAgentCapability(ctx, agentID, latestVersionID, nil, mode); err != nil {
			log.Bg().Warn("syncAgentCapabilities: enable failed, skipping",
				"capability_id", cap.capabilityID, "name", name, "version_id", latestVersionID, "err", err)
			continue
		}
		log.Bg().Info("syncAgentCapabilities: enabled capability",
			"agent_id", agentID, "capability_id", cap.capabilityID, "name", name, "version_id", latestVersionID, "from_marketplace", cap.fromMarketplace, "pinning_mode", mode)
	}

	// 3. Remove capabilities no longer in the desired list.
	for capID, ac := range existingByCapID {
		if desiredCapIDs[capID] {
			continue
		}
		if err := rs.DeleteAgentCapability(ctx, agentID, ac.CapabilityVersionID); err != nil {
			log.Bg().Warn("syncAgentCapabilities: delete failed, skipping",
				"capability_id", capID, "capability_version_id", ac.CapabilityVersionID, "err", err)
			continue
		}
		log.Bg().Info("syncAgentCapabilities: removed capability",
			"agent_id", agentID, "capability_id", capID, "capability_version_id", ac.CapabilityVersionID)
	}
	return nil
}

// updateAgentVisibilityBody is the request body for
// PATCH /api/v1/agents/{agentID}/visibility.
type updateAgentVisibilityBody struct {
	Visibility string `json:"visibility"`
}

// updateAgentVisibility flips an Agent's visibility between
// workspace / tenant / public. Owner/admin only. Identical visibility
// is treated as a 200 noop so idempotent replays don't pollute audit.
// updateAgentVisibility updates an agent's visibility setting.
//
//	@Summary		Update an agent's visibility
//	@Description	Updates an agent's visibility (public / tenant / private). Owner/admin only. Rejected if bindings are inconsistent with the requested visibility.
//	@Tags			agents
//	@ID				updateDevAgentVisibility
//	@Accept			json
//	@Produce		json
//	@Param			agentID	path	string						true	"Agent UUID"
//	@Param			body	body	updateAgentVisibilityBody	true	"Visibility payload"
//	@Success		200 {object} map[string]interface{} "Updated agent"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		422 {object} map[string]string "Visibility conflicts with agent bindings"
//	@Router			/api/v1/agents/{agentID}/visibility [patch]
func updateAgentVisibility(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if runtimeStore == nil || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		agent, err := runtimeStore.GetAgent(r.Context(), agentID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, agent.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req updateAgentVisibilityBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		change, err := runtimeStore.UpdateAgentVisibility(r.Context(), agentID, req.Visibility, actorIDFromRequest(r))
		if err != nil {
			if errors.Is(err, store.ErrInvalidAgentVisibility) {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
				return
			}
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"visibility": change})
	}
}

// deleteAgent soft-deletes an agent. Owner/admin only.
//
//	@Summary		Soft-delete an agent
//	@Description	Marks the agent as deleted; existing conversations are retained. Owner/admin only.
//	@Tags			agents
//	@ID				deleteDevAgent
//	@Produce		json
//	@Param			agentID	path	string	true	"Agent UUID"
//	@Success		200 {object} map[string]interface{} "Deleted agent"
//	@Failure		400 {object} map[string]string "agent_id must be a valid uuid"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Unknown agent"
//	@Router			/api/v1/agents/{agentID} [delete]
func deleteAgent(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if runtimeStore == nil || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		agent, err := runtimeStore.GetAgent(r.Context(), agentID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, agent.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		result, runCount, err := runtimeStore.DeleteAgent(r.Context(), agentID, actorIDFromRequest(r))
		if errors.Is(err, store.ErrInFlightAgentRuns) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "in_flight_runs", "run_count": runCount})
			return
		}
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func agentStatusHandler(runtimeStore RuntimeStore, action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed agent lifecycle is disabled"})
			return
		}
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		workspaceID, ok := workspaceIDForAgent(w, r.Context(), runtimeStore, agentID)
		if !ok {
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var (
			result store.AgentStatusRead
			err    error
		)
		switch action {
		case "disable":
			result, err = runtimeStore.DisableAgent(r.Context(), agentID)
		case "enable":
			result, err = runtimeStore.EnableAgent(r.Context(), agentID)
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unsupported agent action"})
			return
		}
		if err != nil {
			if errors.Is(err, store.ErrUnknownAgent) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to %s agent", action)})
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func isSafeHTTPAgentEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

// listWorkspaceEnabledAgents lists the agents visible to the caller in
// a workspace. By default only enabled/active agents are returned;
// `?include_disabled=true` widens the query to the admin view.
//
//	@Summary		List active agents in a workspace
//	@Description	Returns agents the caller can address in the given workspace. By default only enabled/active agents; pass include_disabled=true for the admin view.
//	@Tags			agents
//	@ID				listDevAgents
//	@Produce		json
//	@Param			workspaceID			path	string	true	"Workspace UUID"
//	@Param			include_disabled	query	bool	false	"Include disabled/archived agents in the response"
//	@Success		200 {object} map[string]interface{} "Active workspace agents with profile basics"
//	@Failure		400 {object} map[string]string "workspace_id must be a valid uuid"
//	@Failure		403 {object} map[string]string "Caller is not an active workspace member"
//	@Failure		503 {object} map[string]string "Database-backed read APIs are disabled"
//	@Router			/api/v1/workspaces/{workspaceID}/agents [get]
func listWorkspaceEnabledAgents(runtimeStore RuntimeStore) http.HandlerFunc {
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

		includeDisabled := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_disabled")), "true")
		var (
			agents []store.AgentRead
			err    error
		)
		if includeDisabled {
			agents, err = runtimeStore.ListWorkspaceAgentsForAdmin(r.Context(), workspaceID)
		} else {
			agents, err = runtimeStore.ListWorkspaceEnabledAgents(r.Context(), workspaceID)
		}
		if err != nil {
			writeReadError(w, err, "failed to list agents")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "agents": agents})
	}
}

// getAgentMetrics returns aggregated run-history counters for
// a single agent over a sliding window. Powers the agent-detail
// "Last N days performance" panel: completion count, success rate, average duration.
// `?days=` is optional and clamps to [1, 365]; default 30.
// getAgentMetrics returns runtime/usage metrics for an agent.
//
//	@Summary		Get agent metrics
//	@Description	Returns runtime/usage metrics for the agent within the workspace. Caller must be a workspace member.
//	@Tags			agents
//	@ID				getDevAgentMetrics
//	@Produce		json
//	@Param			workspaceID	path	string	true	"Workspace UUID"
//	@Param			agentID		path	string	true	"Agent UUID"
//	@Success		200 {object} map[string]interface{} "Agent metrics"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not a workspace member"
//	@Failure		404 {object} map[string]string "Agent not found"
//	@Router			/api/v1/workspaces/{workspaceID}/agents/{agentID}/metrics [get]
func getAgentMetrics(runtimeStore RuntimeStore) http.HandlerFunc {
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
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, workspaceID); err != nil {
			writeRBACError(w, err)
			return
		}

		days := int32(30)
		if raw := strings.TrimSpace(r.URL.Query().Get("days")); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil {
				if v < 1 {
					v = 1
				} else if v > 365 {
					v = 365
				}
				days = int32(v)
			}
		}

		metrics, err := runtimeStore.GetAgentMetrics(r.Context(), agentID, days)
		if err != nil {
			writeReadError(w, err, "failed to load agent metrics")
			return
		}
		writeJSON(w, http.StatusOK, metrics)
	}
}

func workspaceIDForAgent(w http.ResponseWriter, ctx context.Context, runtimeStore RuntimeStore, agentID string) (string, bool) {
	agent, err := runtimeStore.GetAgentDetail(ctx, agentID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownAgent) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return "", false
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load agent"})
		return "", false
	}
	return agent.WorkspaceID, true
}
