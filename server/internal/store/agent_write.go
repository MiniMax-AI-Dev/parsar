package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

func (s *Store) CreateAgent(ctx context.Context, input CreateAgentInput) (CreateAgentResult, error) {
	now := time.Now().UTC()
	workspaceUUID, err := uuid(input.WorkspaceID)
	if err != nil {
		return CreateAgentResult{}, err
	}
	createdBy, err := uuid(input.CreatedBy)
	if err != nil {
		return CreateAgentResult{}, err
	}
	name := strings.TrimSpace(input.Name)
	connectorType := strings.TrimSpace(input.ConnectorType)
	if name == "" || connectorType == "" {
		return CreateAgentResult{}, ErrInvalidInput
	}
	if !validConnectorType(connectorType) {
		return CreateAgentResult{}, ErrInvalidConnectorType
	}
	if len(input.Capabilities) > 0 {
		return CreateAgentResult{}, fmt.Errorf("%w: Core product capability bindings are not available", ErrInvalidInput)
	}
	slug := strings.TrimSpace(input.Slug)
	explicitSlug := slug != ""
	if !explicitSlug {
		slug = generateAutoSlug("agent")
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return CreateAgentResult{}, err
	}
	defer tx.Rollback(ctx)
	queries := sqlc.New(tx)
	_, err = queries.GetWorkspaceSettings(ctx, workspaceUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreateAgentResult{}, fmt.Errorf("%w: %s", ErrUnknownWorkspace, input.WorkspaceID)
		}
		return CreateAgentResult{}, err
	}
	capabilities := input.Capabilities
	if !input.CapabilitiesSet {
		capabilities = nil
	}
	if explicitSlug {
		if exists, err := queries.ActiveAgentSlugExists(ctx, sqlc.ActiveAgentSlugExistsParams{WorkspaceID: workspaceUUID, Slug: slug}); err != nil {
			return CreateAgentResult{}, err
		} else if exists {
			return CreateAgentResult{}, fmt.Errorf("%w: %s", ErrDuplicateAgentSlug, nextSlugSuggestion(ctx, queries, workspaceUUID, slug))
		}
	}
	config, err := agentConfigJSON(input.SystemPrompt, input.DefaultModelID, capabilities, input.Runtime, connectorType, input.AgentConfig)
	if err != nil {
		return CreateAgentResult{}, err
	}
	visibility := strings.TrimSpace(input.Visibility)
	if visibility == "" {
		visibility = "workspace"
	}
	if !isValidAgentVisibility(visibility) {
		return CreateAgentResult{}, fmt.Errorf("%w: %q", ErrInvalidAgentVisibility, visibility)
	}

	agentRow, err := createAgentWithSlugRetry(ctx, queries, sqlc.CreateAgentCRUDParams{ID: mustUUID(newID()), WorkspaceID: workspaceUUID, Name: name, Slug: slug, Description: strings.TrimSpace(input.Description), ConnectorType: connectorType, Visibility: visibility, Config: config, CreatedBy: createdBy, Now: timestamptz(now)}, explicitSlug)
	if err != nil {
		return CreateAgentResult{}, err
	}
	initialCapabilities, err := writeAgentResourceBindings(ctx, queries, agentRow.ID, input.WorkspaceID, input.InitialCapabilities, now)
	if err != nil {
		return CreateAgentResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateAgentResult{}, err
	}
	agent := agentSummaryFromRow(agentRow.ID, agentRow.WorkspaceID, agentRow.Name, agentRow.Slug, agentRow.Description, agentRow.ConnectorType, agentRow.Status, agentRow.Config, agentRow.CreatedAt, agentRow.UpdatedAt)
	s.emitAgentAudit(now, input.CreatedBy, auditAgentCreated, "agent", agent.ID, agent.WorkspaceID, map[string]any{"name": agent.Name, "slug": agent.Slug, "connector_type": agent.ConnectorType, "default_model_id": input.DefaultModelID, "visibility": visibility})
	return CreateAgentResult{Agent: agent, InitialCapabilities: initialCapabilities}, nil
}

func (s *Store) UpdateAgent(ctx context.Context, input UpdateAgentInput) (AgentSummary, []string, error) {
	now := time.Now().UTC()
	agentUUID, err := uuid(input.AgentID)
	if err != nil {
		return AgentSummary{}, nil, err
	}
	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return AgentSummary{}, nil, err
	}
	defer tx.Rollback(ctx)
	queries := sqlc.New(tx)
	current, err := queries.GetAgentForUpdate(ctx, agentUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentSummary{}, nil, fmt.Errorf("%w: %s", ErrUnknownAgent, input.AgentID)
		}
		return AgentSummary{}, nil, err
	}
	name, description, connectorType := current.Name, current.Description, current.ConnectorType
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
	}
	if input.Description != nil {
		description = strings.TrimSpace(*input.Description)
	}
	if input.ConnectorType != nil {
		connectorType = strings.TrimSpace(*input.ConnectorType)
	}
	if name == "" {
		return AgentSummary{}, nil, ErrInvalidInput
	}
	if !validConnectorType(connectorType) {
		return AgentSummary{}, nil, ErrInvalidConnectorType
	}
	config := decodeJSONMap(current.Config)
	if input.SystemPrompt != nil {
		config["system_prompt"] = strings.TrimSpace(*input.SystemPrompt)
	}
	if input.DefaultModelID != nil || len(input.Capabilities) != 0 {
		return AgentSummary{}, nil, fmt.Errorf("%w: legacy execution settings are not supported by Core Agents", ErrInvalidInput)
	}
	if input.ConfigSet {
		// Config is a replacement; omission preserves the entire previous config.
		config = map[string]any{"system_prompt": config["system_prompt"], "connectors": config["connectors"]}
		if err := ValidateCoreAgentConfig(input.Config, true); err != nil {
			return AgentSummary{}, nil, err
		}
		if err := foldCoreAgentConfig(config, input.Config); err != nil {
			return AgentSummary{}, nil, err
		}
	}
	encoded, err := json.Marshal(nonNilMap(config))
	if err != nil {
		return AgentSummary{}, nil, err
	}
	row, err := queries.UpdateAgentCRUD(ctx, sqlc.UpdateAgentCRUDParams{ID: agentUUID, Name: name, Description: description, ConnectorType: connectorType, Config: encoded, Now: timestamptz(now)})
	if err != nil {
		return AgentSummary{}, nil, err
	}
	if input.ResourceBindingsSet {
		if _, err := writeAgentResourceBindings(ctx, queries, input.AgentID, current.WorkspaceID, input.ResourceBindings, now); err != nil {
			return AgentSummary{}, nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentSummary{}, nil, err
	}
	agent := agentSummaryFromRow(row.ID, row.WorkspaceID, row.Name, row.Slug, row.Description, row.ConnectorType, row.Status, row.Config, row.CreatedAt, row.UpdatedAt)
	agent.Visibility = current.Visibility
	changed := changedAgentFields(current, agent, input)
	if input.ResourceBindingsSet {
		changed = append(changed, "resource_bindings")
	}
	s.emitAgentAudit(now, input.ActorID, auditAgentUpdated, "agent", agent.ID, agent.WorkspaceID, map[string]any{"changed_fields": changed})
	return agent, changed, nil
}

func writeAgentResourceBindings(ctx context.Context, queries *sqlc.Queries, agentID, workspaceID string, requestedBindings []InitialAgentCapabilityInput, now time.Time) ([]AgentCapabilityRead, error) {
	existing, err := queries.ListAgentCapabilitiesByAgent(ctx, mustUUID(agentID))
	if err != nil {
		return nil, err
	}
	byCapability := map[string]sqlc.ListAgentCapabilitiesByAgentRow{}
	for _, binding := range existing {
		byCapability[binding.CapabilityID] = binding
	}
	initialCapabilities := make([]AgentCapabilityRead, 0, len(requestedBindings))
	seenInitialCapabilities := map[string]bool{}
	for _, requested := range requestedBindings {
		versionID := strings.TrimSpace(requested.CapabilityVersionID)
		if versionID == "" {
			return nil, fmt.Errorf("%w: empty capability_version_id", ErrInvalidInput)
		}
		versionUUID, err := uuid(versionID)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid capability_version_id", ErrInvalidInput)
		}
		version, err := queries.GetCapabilityVersion(ctx, versionUUID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("%w: %s", ErrUnknownCapabilityVersion, versionID)
			}
			return nil, err
		}
		capability, err := queries.GetCapability(ctx, mustUUID(version.CapabilityID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("%w: %s", ErrUnknownCapability, version.CapabilityID)
			}
			return nil, err
		}
		current := byCapability[version.CapabilityID]
		retained := current.Enabled && AgentResourceBindingUnchanged(current.CapabilityVersionID, current.PinningMode, requested)
		if !retained && (capability.Status != "active" || (capability.WorkspaceID != workspaceID && (capability.Visibility != "public" || capability.DeprecatedAt.Valid))) {
			return nil, fmt.Errorf("%w: %s", ErrMarketplaceCapabilityUnavailable, version.CapabilityID)
		}
		if seenInitialCapabilities[version.CapabilityID] {
			return nil, fmt.Errorf("%w: duplicate initial capability %s", ErrInvalidInput, version.CapabilityID)
		}
		seenInitialCapabilities[version.CapabilityID] = true
		if requested.PinningMode != "" && requested.PinningMode != "latest" && requested.PinningMode != "pinned" {
			return nil, fmt.Errorf("%w: invalid pinning mode", ErrInvalidInput)
		}
		configuration, err := json.Marshal(nonNilMap(requested.Configuration))
		if err != nil {
			return nil, err
		}
		if id := current.ID; id != "" {
			row, err := queries.UpdateAgentCapability(ctx, sqlc.UpdateAgentCapabilityParams{ID: mustUUID(id), CapabilityVersionID: versionUUID, Enabled: true, Configuration: configuration, PinningMode: normalizePinningMode(requested.PinningMode), Now: timestamptz(now)})
			if err != nil {
				return nil, err
			}
			initialCapabilities = append(initialCapabilities, agentCapabilityFromUpdateRow(row))
			continue
		}
		row, err := queries.CreateAgentCapability(ctx, sqlc.CreateAgentCapabilityParams{
			ID:                  mustUUID(newID()),
			AgentID:             mustUUID(agentID),
			CapabilityID:        mustUUID(version.CapabilityID),
			CapabilityVersionID: versionUUID,
			Enabled:             true,
			Configuration:       configuration,
			PinningMode:         normalizePinningMode(requested.PinningMode),
			Now:                 timestamptz(now),
		})
		if err != nil {
			return nil, err
		}
		initialCapabilities = append(initialCapabilities, agentCapabilityFromCreateRow(row))
	}
	for _, binding := range existing {
		if !seenInitialCapabilities[binding.CapabilityID] {
			if err := queries.DeleteAgentCapability(ctx, mustUUID(binding.ID)); err != nil {
				return nil, err
			}
		}
	}
	return initialCapabilities, nil
}

// AgentResourceBindingUnchanged permits retained installations without granting
// access to another revision or changing the version tracking policy.
func AgentResourceBindingUnchanged(versionID, pinningMode string, requested InitialAgentCapabilityInput) bool {
	return versionID == requested.CapabilityVersionID && normalizePinningMode(pinningMode) == normalizePinningMode(requested.PinningMode)
}
