package interaction

import (
	"context"
	"errors"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

var (
	ErrInvalidDecision    = errors.New("invalid interaction decision")
	ErrNotFound           = errors.New("interaction not found")
	ErrAlreadyResolving   = errors.New("interaction is being resolved")
	ErrExpired            = errors.New("interaction expired")
	ErrRuntimeUnavailable = errors.New("interaction runtime unavailable")
	ErrRuntimeGone        = errors.New("interaction runtime request is no longer pending")
)

type Actor struct {
	UserID    string
	ActorID   string
	Source    string
	ActorType string
}

type QuestionAnswer struct {
	QuestionID string   `json:"question_id"`
	Answers    []string `json:"answers"`
}

type Decision struct {
	Approved        *bool
	Note            string
	QuestionAnswers []QuestionAnswer
	Cancelled       bool
}

type ResolveRequest struct {
	WorkspaceID   string
	InteractionID string
	Kind          string
	RequestID     string
	AgentRunID    string
	Actor         Actor
	Decision      Decision
}

type ResolveResult struct {
	Interaction     store.AgentInteractionRead
	Applied         bool
	AlreadyResolved bool
}

type Store interface {
	GetAgentInteraction(ctx context.Context, interactionID string) (store.AgentInteractionRead, error)
	GetAgentInteractionByRequestID(ctx context.Context, kind, requestID, agentRunID string) (store.AgentInteractionRead, error)
	ClaimAgentInteraction(ctx context.Context, workspaceID, interactionID, userID, actorID, source string, now time.Time) (store.AgentInteractionClaim, error)
	ClaimAgentInteractionByRequestID(ctx context.Context, kind, requestID, agentRunID, userID, actorID, source string, now time.Time) (store.AgentInteractionClaim, error)
	ClaimExpiredAgentInteraction(ctx context.Context, interactionID string, now time.Time) (store.AgentInteractionClaim, error)
	CompleteAgentInteraction(ctx context.Context, claim store.AgentInteractionClaim, status string, response map[string]any, now time.Time) error
	ReleaseAgentInteractionClaim(ctx context.Context, claim store.AgentInteractionClaim, now time.Time) error
	ListExpiredPendingAgentInteractionIDs(ctx context.Context, now time.Time, limit int32) ([]string, error)
	ReleaseStaleAgentInteractionClaims(ctx context.Context, staleBefore, now time.Time) (int64, error)
	ClearConversationInflightSlot(ctx context.Context, conversationID string, slot store.InflightSlotKind, expectedAgentRunID string) error
	RecordAgentRunEvent(ctx context.Context, input store.RecordAgentRunEventInput) error
	RecordAgentInteractionResolutionAudit(input store.AgentInteractionAuditInput)
}

type Delivery interface {
	SubmitPermission(ctx context.Context, runID string, decision connector.PermissionDecision) error
	SubmitPromptForUserChoice(ctx context.Context, runID string, decision connector.PromptForUserChoiceDecision) error
}

type Logger interface {
	Warn(msg string, args ...any)
}
