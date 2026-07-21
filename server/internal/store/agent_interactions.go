package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	AgentInteractionKindPermission = "permission"
	AgentInteractionKindUserChoice = "user_choice"

	AgentInteractionStatusPending   = "pending"
	AgentInteractionStatusResolving = "resolving"
	AgentInteractionStatusApproved  = "approved"
	AgentInteractionStatusDenied    = "denied"
	AgentInteractionStatusAnswered  = "answered"
	AgentInteractionStatusCancelled = "cancelled"
	AgentInteractionStatusExpired   = "expired"

	AgentInteractionSourceWeb           = "web"
	AgentInteractionSourceFeishu        = "feishu"
	AgentInteractionSourceSlack         = "slack"
	AgentInteractionSourceDiscord       = "discord"
	AgentInteractionSourceTeams         = "teams"
	AgentInteractionSourceSystemTimeout = "system_timeout"
	AgentInteractionSourceRuntime       = "runtime"

	// AgentInteractionTTL matches the daemon-side AskUserQuestion timeout.
	// Permission requests use the same bounded lifetime so a disconnected
	// browser cannot leave a run waiting forever.
	AgentInteractionTTL = 10 * time.Minute
)

var (
	ErrUnknownAgentInteraction    = errors.New("unknown agent interaction")
	ErrAgentInteractionNotPending = errors.New("agent interaction is no longer pending")
)

type AgentInteractionRead struct {
	ID                string         `json:"id"`
	WorkspaceID       string         `json:"workspace_id"`
	ConversationID    string         `json:"conversation_id"`
	AgentRunID        string         `json:"agent_run_id"`
	RequestID         string         `json:"request_id"`
	Kind              string         `json:"kind"`
	Status            string         `json:"status"`
	Request           map[string]any `json:"request"`
	Response          map[string]any `json:"response"`
	ResolutionSource  string         `json:"resolution_source,omitempty"`
	ResolvedActor     string         `json:"resolved_actor,omitempty"`
	ResolvedBy        string         `json:"resolved_by,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	ExpiresAt         time.Time      `json:"expires_at"`
	ResolvedAt        *time.Time     `json:"resolved_at,omitempty"`
	UpdatedAt         time.Time      `json:"updated_at"`
	AgentName         string         `json:"agent_name"`
	ConversationTitle string         `json:"conversation_title"`
}

type AgentInteractionClaim struct {
	ID             string
	WorkspaceID    string
	RequestID      string
	Kind           string
	AgentRunID     string
	ConversationID string
	DeviceID       string
	ClaimToken     string
	Request        map[string]any
}

type AgentInteractionAuditInput struct {
	InteractionID string
	WorkspaceID   string
	AgentRunID    string
	RequestID     string
	Kind          string
	Status        string
	Source        string
	ActorID       string
	ActorType     string
	Response      map[string]any
}

func (s *Store) ListWorkspaceAgentInteractions(ctx context.Context, workspaceID, statusGroup string, limit int32) ([]AgentInteractionRead, error) {
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return nil, err
	}
	statusGroup = strings.TrimSpace(statusGroup)
	if statusGroup != "" && statusGroup != "pending" && statusGroup != "decided" && statusGroup != "expired" {
		return nil, fmt.Errorf("invalid interaction status group %q", statusGroup)
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	queries := sqlc.New(s.db)
	rows, err := queries.ListWorkspaceAgentInteractions(ctx, sqlc.ListWorkspaceAgentInteractionsParams{
		WorkspaceID: workspaceUUID, StatusGroup: statusGroup, ItemLimit: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]AgentInteractionRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, interactionRead(
			row.AiID, row.AiWorkspaceID, row.AiConversationID, row.AiAgentRunID,
			row.RequestID, row.Kind, row.Status, row.Request, row.Response,
			row.ResolutionSource, row.ResolvedActor, row.ResolvedBy,
			row.CreatedAt, row.ExpiresAt, row.ResolvedAt, row.UpdatedAt,
			row.AgentName, row.ConversationTitle,
		))
	}
	return out, nil
}

func (s *Store) GetAgentInteraction(ctx context.Context, interactionID string) (AgentInteractionRead, error) {
	id, err := uuid(interactionID)
	if err != nil {
		return AgentInteractionRead{}, err
	}
	row, err := sqlc.New(s.db).GetAgentInteraction(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentInteractionRead{}, fmt.Errorf("%w: %s", ErrUnknownAgentInteraction, interactionID)
		}
		return AgentInteractionRead{}, err
	}
	return interactionRead(
		row.AiID, row.AiWorkspaceID, row.AiConversationID, row.AiAgentRunID,
		row.RequestID, row.Kind, row.Status, row.Request, row.Response,
		row.ResolutionSource, row.ResolvedActor, row.ResolvedBy,
		row.CreatedAt, row.ExpiresAt, row.ResolvedAt, row.UpdatedAt,
		row.AgentName, row.ConversationTitle,
	), nil
}

func (s *Store) GetAgentInteractionByRequestID(ctx context.Context, kind, requestID, agentRunID string) (AgentInteractionRead, error) {
	runID, err := uuid(agentRunID)
	if err != nil {
		return AgentInteractionRead{}, err
	}
	row, err := sqlc.New(s.db).GetAgentInteractionByRequestID(ctx, sqlc.GetAgentInteractionByRequestIDParams{
		Kind: strings.TrimSpace(kind), RequestID: strings.TrimSpace(requestID), AgentRunID: runID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentInteractionRead{}, fmt.Errorf("%w: %s", ErrUnknownAgentInteraction, requestID)
		}
		return AgentInteractionRead{}, err
	}
	return interactionRead(
		row.AiID, row.AiWorkspaceID, row.AiConversationID, row.AiAgentRunID,
		row.RequestID, row.Kind, row.Status, row.Request, row.Response,
		row.ResolutionSource, row.ResolvedActor, row.ResolvedBy,
		row.CreatedAt, row.ExpiresAt, row.ResolvedAt, row.UpdatedAt,
		row.AgentName, row.ConversationTitle,
	), nil
}

func (s *Store) ClaimAgentInteraction(ctx context.Context, workspaceID, interactionID, userID, actorID, source string, now time.Time) (AgentInteractionClaim, error) {
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return AgentInteractionClaim{}, err
	}
	interactionUUID, err := uuid(interactionID)
	if err != nil {
		return AgentInteractionClaim{}, err
	}
	row, err := sqlc.New(s.db).ClaimAgentInteraction(ctx, sqlc.ClaimAgentInteractionParams{
		ResolvedBy: strings.TrimSpace(userID), ResolvedActor: strings.TrimSpace(actorID),
		ResolutionSource: textOrNull(source), Now: timestamptz(now.UTC()),
		InteractionID: interactionUUID, WorkspaceID: workspaceUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentInteractionClaim{}, ErrAgentInteractionNotPending
		}
		return AgentInteractionClaim{}, err
	}
	return interactionClaim(row.ID, row.WorkspaceID, row.RequestID, row.Kind, row.AgentRunID,
		row.ConversationID, row.DeviceID, row.ClaimToken, row.Request), nil
}

func (s *Store) ClaimAgentInteractionByRequestID(ctx context.Context, kind, requestID, agentRunID, userID, actorID, source string, now time.Time) (AgentInteractionClaim, error) {
	runID, err := uuid(agentRunID)
	if err != nil {
		return AgentInteractionClaim{}, err
	}
	row, err := sqlc.New(s.db).ClaimAgentInteractionByRequestID(ctx, sqlc.ClaimAgentInteractionByRequestIDParams{
		Kind: strings.TrimSpace(kind), RequestID: strings.TrimSpace(requestID), AgentRunID: runID,
		ResolvedBy: strings.TrimSpace(userID), ResolvedActor: strings.TrimSpace(actorID),
		ResolutionSource: textOrNull(source), Now: timestamptz(now.UTC()),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentInteractionClaim{}, ErrAgentInteractionNotPending
		}
		return AgentInteractionClaim{}, err
	}
	return interactionClaim(row.ID, row.WorkspaceID, row.RequestID, row.Kind, row.AgentRunID,
		row.ConversationID, row.DeviceID, row.ClaimToken, row.Request), nil
}

func (s *Store) ClaimExpiredAgentInteraction(ctx context.Context, interactionID string, now time.Time) (AgentInteractionClaim, error) {
	id, err := uuid(interactionID)
	if err != nil {
		return AgentInteractionClaim{}, err
	}
	row, err := sqlc.New(s.db).ClaimExpiredAgentInteraction(ctx, sqlc.ClaimExpiredAgentInteractionParams{
		Now: timestamptz(now.UTC()), InteractionID: id,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentInteractionClaim{}, ErrAgentInteractionNotPending
		}
		return AgentInteractionClaim{}, err
	}
	return interactionClaim(row.ID, row.WorkspaceID, row.RequestID, row.Kind, row.AgentRunID,
		row.ConversationID, row.DeviceID, row.ClaimToken, row.Request), nil
}

func (s *Store) ListExpiredPendingAgentInteractionIDs(ctx context.Context, now time.Time, limit int32) ([]string, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	return sqlc.New(s.db).ListExpiredPendingAgentInteractionIDs(ctx, sqlc.ListExpiredPendingAgentInteractionIDsParams{
		Now: timestamptz(now.UTC()), ItemLimit: limit,
	})
}

func (s *Store) ReleaseStaleAgentInteractionClaims(ctx context.Context, staleBefore, now time.Time) (int64, error) {
	return sqlc.New(s.db).ReleaseStaleResolvingAgentInteractions(ctx, sqlc.ReleaseStaleResolvingAgentInteractionsParams{
		Now: timestamptz(now.UTC()), StaleBefore: timestamptz(staleBefore.UTC()),
	})
}

func (s *Store) CompleteAgentInteraction(ctx context.Context, claim AgentInteractionClaim, status string, response map[string]any, now time.Time) error {
	id, err := uuid(claim.ID)
	if err != nil {
		return err
	}
	claimToken, err := uuid(claim.ClaimToken)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = sqlc.New(s.db).CompleteAgentInteraction(ctx, sqlc.CompleteAgentInteractionParams{
		Status: status, Response: encoded, Now: timestamptz(now.UTC()), InteractionID: id,
		ClaimToken: claimToken,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAgentInteractionNotPending
	}
	return err
}

func (s *Store) ReleaseAgentInteractionClaim(ctx context.Context, claim AgentInteractionClaim, now time.Time) error {
	id, err := uuid(claim.ID)
	if err != nil {
		return err
	}
	claimToken, err := uuid(claim.ClaimToken)
	if err != nil {
		return err
	}
	return sqlc.New(s.db).ReleaseAgentInteractionClaim(ctx, sqlc.ReleaseAgentInteractionClaimParams{
		Now: timestamptz(now.UTC()), InteractionID: id, ClaimToken: claimToken,
	})
}

// ResolveAgentInteractionByRequestID lets existing IM callbacks close the
// canonical Web-visible record after the daemon accepts their response.
func (s *Store) ResolveAgentInteractionByRequestID(ctx context.Context, kind, requestID, status string, response map[string]any) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = sqlc.New(s.db).ResolveAgentInteractionByRequestID(ctx, sqlc.ResolveAgentInteractionByRequestIDParams{
		Status: status, Response: encoded, Now: timestamptz(time.Now().UTC()),
		Kind: kind, RequestID: strings.TrimSpace(requestID),
	})
	return err
}

func (s *Store) interactionDeviceID(ctx context.Context, kind, requestID string) (string, error) {
	return sqlc.New(s.db).GetAgentInteractionDeviceByRequestID(ctx, sqlc.GetAgentInteractionDeviceByRequestIDParams{
		Kind: kind, RequestID: strings.TrimSpace(requestID),
	})
}

func (s *Store) RecordAgentInteractionResolutionAudit(input AgentInteractionAuditInput) {
	actorType := strings.TrimSpace(input.ActorType)
	if actorType == "" {
		actorType = audit.ActorTypeExternal
	}
	eventType := AuditPermissionResolved
	if input.Kind == AgentInteractionKindUserChoice {
		eventType = AuditUserChoiceResolved
	}
	s.emitAuditEvent(audit.Event{
		OccurredAt: time.Now().UTC(), Source: audit.SourceApproval, EventType: eventType,
		ActorType: actorType, ActorID: input.ActorID, TargetType: "agent_interaction",
		TargetID: input.InteractionID, WorkspaceID: input.WorkspaceID,
		Payload: map[string]any{
			"request_id": input.RequestID, "agent_run_id": input.AgentRunID,
			"kind": input.Kind, "status": input.Status, "source": input.Source,
			"response": input.Response,
		},
	})
}

func interactionClaim(id, workspaceID, requestID, kind, runID, conversationID, deviceID, claimToken string, request []byte) AgentInteractionClaim {
	return AgentInteractionClaim{
		ID: id, WorkspaceID: workspaceID, RequestID: requestID, Kind: kind,
		AgentRunID: runID, ConversationID: conversationID, DeviceID: deviceID,
		ClaimToken: claimToken, Request: decodeJSONMap(request),
	}
}

func interactionRead(
	id, workspaceID, conversationID, agentRunID, requestID, kind, status string,
	request, response []byte, resolutionSource, resolvedActor, resolvedBy string,
	createdAt, expiresAt, resolvedAt, updatedAt pgtype.Timestamptz,
	agentName, conversationTitle string,
) AgentInteractionRead {
	var resolvedAtPtr *time.Time
	if resolvedAt.Valid {
		value := resolvedAt.Time.UTC()
		resolvedAtPtr = &value
	}
	return AgentInteractionRead{
		ID: id, WorkspaceID: workspaceID, ConversationID: conversationID,
		AgentRunID: agentRunID, RequestID: requestID, Kind: kind, Status: status,
		Request: decodeJSONMap(request), Response: decodeJSONMap(response),
		ResolutionSource: resolutionSource, ResolvedActor: resolvedActor, ResolvedBy: resolvedBy,
		CreatedAt: pgTime(createdAt), ExpiresAt: pgTime(expiresAt),
		ResolvedAt: resolvedAtPtr, UpdatedAt: pgTime(updatedAt), AgentName: agentName,
		ConversationTitle: conversationTitle,
	}
}
