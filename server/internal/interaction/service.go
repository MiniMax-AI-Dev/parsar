package interaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type Service struct {
	store    Store
	delivery Delivery
	logger   Logger
	now      func() time.Time
}

func NewService(store Store, delivery Delivery, logger Logger) *Service {
	return &Service{store: store, delivery: delivery, logger: logger, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Resolve(ctx context.Context, req ResolveRequest) (ResolveResult, error) {
	current, err := s.load(ctx, req)
	if err != nil {
		return ResolveResult{}, err
	}
	if req.WorkspaceID != "" && current.WorkspaceID != req.WorkspaceID {
		return ResolveResult{}, ErrNotFound
	}
	if result, done, err := classifyCurrent(current, s.now()); done {
		return result, err
	}
	if err := validateDecision(current, req.Decision); err != nil {
		return ResolveResult{}, err
	}

	claim, err := s.claim(ctx, req, s.now())
	if err != nil {
		if errors.Is(err, store.ErrAgentInteractionNotPending) {
			latest, loadErr := s.load(ctx, req)
			if loadErr != nil {
				return ResolveResult{}, loadErr
			}
			result, done, classifyErr := classifyCurrent(latest, s.now())
			if done {
				return result, classifyErr
			}
			// A competing resolver can release a temporary failure between
			// our failed claim and this reload. Do not return an empty success;
			// tell the caller to retry through the normal conflict path.
			return ResolveResult{Interaction: latest}, ErrAlreadyResolving
		}
		return ResolveResult{}, err
	}
	status, response, err := s.deliver(ctx, claim, req.Decision, req.Actor)
	if err != nil {
		if errors.Is(err, connector.ErrInteractionNoLongerPending) {
			response = map[string]any{"reason": "runtime_request_gone"}
			if completeErr := s.complete(ctx, claim, store.AgentInteractionStatusCancelled, response, req.Actor); completeErr != nil {
				return ResolveResult{}, completeErr
			}
			latest, _ := s.store.GetAgentInteraction(ctx, claim.ID)
			return ResolveResult{Interaction: latest, AlreadyResolved: true}, ErrRuntimeGone
		}
		s.releaseClaim(ctx, claim)
		return ResolveResult{}, fmt.Errorf("%w: %v", ErrRuntimeUnavailable, err)
	}
	if err := s.complete(ctx, claim, status, response, req.Actor); err != nil {
		return ResolveResult{}, err
	}
	latest, err := s.store.GetAgentInteraction(ctx, claim.ID)
	if err != nil {
		return ResolveResult{}, err
	}
	return ResolveResult{Interaction: latest, Applied: true}, nil
}

func (s *Service) Expire(ctx context.Context, interactionID string) error {
	claim, err := s.store.ClaimExpiredAgentInteraction(ctx, interactionID, s.now())
	if err != nil {
		if errors.Is(err, store.ErrAgentInteractionNotPending) {
			return nil
		}
		return err
	}
	decision := Decision{Cancelled: claim.Kind == store.AgentInteractionKindUserChoice}
	if claim.Kind == store.AgentInteractionKindPermission {
		approved := false
		decision.Approved = &approved
	}
	actor := Actor{ActorID: store.AgentInteractionSourceSystemTimeout, Source: store.AgentInteractionSourceSystemTimeout, ActorType: audit.ActorTypeSystem}
	_, response, deliveryErr := s.deliver(ctx, claim, decision, actor)
	if deliveryErr != nil && !errors.Is(deliveryErr, connector.ErrInteractionNoLongerPending) {
		s.releaseClaim(ctx, claim)
		return deliveryErr
	}
	response["expired"] = true
	return s.complete(ctx, claim, store.AgentInteractionStatusExpired, response, actor)
}

func (s *Service) releaseClaim(ctx context.Context, claim store.AgentInteractionClaim) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.store.ReleaseAgentInteractionClaim(writeCtx, claim, s.now()); err != nil {
		s.warn("release interaction claim failed", "request_id", claim.RequestID, "error", err)
	}
}

func (s *Service) load(ctx context.Context, req ResolveRequest) (store.AgentInteractionRead, error) {
	var current store.AgentInteractionRead
	var err error
	if strings.TrimSpace(req.InteractionID) != "" {
		current, err = s.store.GetAgentInteraction(ctx, req.InteractionID)
	} else {
		if strings.TrimSpace(req.AgentRunID) == "" {
			return store.AgentInteractionRead{}, ErrNotFound
		}
		current, err = s.store.GetAgentInteractionByRequestID(ctx, req.Kind, req.RequestID, req.AgentRunID)
	}
	if err != nil {
		if errors.Is(err, store.ErrUnknownAgentInteraction) {
			return store.AgentInteractionRead{}, ErrNotFound
		}
		return store.AgentInteractionRead{}, err
	}
	return current, nil
}

func (s *Service) claim(ctx context.Context, req ResolveRequest, now time.Time) (store.AgentInteractionClaim, error) {
	actorID := strings.TrimSpace(req.Actor.ActorID)
	if actorID == "" {
		actorID = strings.TrimSpace(req.Actor.UserID)
	}
	if strings.TrimSpace(req.InteractionID) != "" {
		return s.store.ClaimAgentInteraction(ctx, req.WorkspaceID, req.InteractionID, req.Actor.UserID, actorID, req.Actor.Source, now)
	}
	return s.store.ClaimAgentInteractionByRequestID(ctx, req.Kind, req.RequestID, req.AgentRunID, req.Actor.UserID, actorID, req.Actor.Source, now)
}

func (s *Service) deliver(ctx context.Context, claim store.AgentInteractionClaim, decision Decision, actor Actor) (string, map[string]any, error) {
	if s.delivery == nil {
		return "", nil, errors.New("interaction delivery is unavailable")
	}
	switch claim.Kind {
	case store.AgentInteractionKindPermission:
		approved := decision.Approved != nil && *decision.Approved
		response := map[string]any{"approved": approved, "note": strings.TrimSpace(decision.Note)}
		err := s.delivery.SubmitPermission(ctx, claim.AgentRunID, connector.PermissionDecision{
			RequestID: claim.RequestID, DeliveryID: claim.ID, DeviceID: claim.DeviceID,
			Approved: approved, Note: strings.TrimSpace(decision.Note), By: actor.ActorID,
		})
		status := store.AgentInteractionStatusDenied
		if approved {
			status = store.AgentInteractionStatusApproved
		}
		return status, response, err
	case store.AgentInteractionKindUserChoice:
		answers := normalizeAnswers(decision.QuestionAnswers)
		response := map[string]any{"answers": redactSecretAnswers(claim.Request, answers), "cancelled": decision.Cancelled, "reason": strings.TrimSpace(decision.Note)}
		connectorAnswers := make([]connector.PromptForUserChoiceQuestionAnswer, 0, len(decision.QuestionAnswers))
		for _, answer := range decision.QuestionAnswers {
			questionID := strings.TrimSpace(answer.QuestionID)
			values := answers[questionID]
			connectorAnswers = append(connectorAnswers, connector.PromptForUserChoiceQuestionAnswer{
				QuestionID: questionID, Answers: append([]string(nil), values...), Answer: strings.Join(values, ", "),
			})
		}
		err := s.delivery.SubmitPromptForUserChoice(ctx, claim.AgentRunID, connector.PromptForUserChoiceDecision{
			RequestID: claim.RequestID, DeliveryID: claim.ID, DeviceID: claim.DeviceID,
			QuestionAnswers: connectorAnswers, Cancelled: decision.Cancelled,
			Reason: strings.TrimSpace(decision.Note), By: actor.ActorID,
		})
		status := store.AgentInteractionStatusAnswered
		if decision.Cancelled {
			status = store.AgentInteractionStatusCancelled
		}
		return status, response, err
	default:
		return "", nil, fmt.Errorf("%w: unsupported interaction kind", ErrInvalidDecision)
	}
}

func (s *Service) complete(ctx context.Context, claim store.AgentInteractionClaim, status string, response map[string]any, actor Actor) error {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.store.CompleteAgentInteraction(writeCtx, claim, status, response, s.now()); err != nil {
		return err
	}
	slot := store.InflightSlotPermission
	eventKind := "permission.replied"
	if claim.Kind == store.AgentInteractionKindUserChoice {
		slot = store.InflightSlotPromptForUserChoice
		eventKind = "prompt_for_user_choice.replied"
	}
	if err := s.store.ClearConversationInflightSlot(writeCtx, claim.ConversationID, slot, claim.AgentRunID); err != nil {
		s.warn("clear interaction inflight slot failed", "request_id", claim.RequestID, "error", err)
	}
	if err := s.store.RecordAgentRunEvent(writeCtx, store.RecordAgentRunEventInput{
		RunID: claim.AgentRunID, EventKind: eventKind,
		Payload: map[string]any{"request_id": claim.RequestID, "status": status, "response": response}, OccurredAt: s.now(),
	}); err != nil {
		s.warn("record interaction resolution event failed", "request_id", claim.RequestID, "error", err)
	}
	actorID := strings.TrimSpace(actor.ActorID)
	if actorID == "" {
		actorID = strings.TrimSpace(actor.UserID)
	}
	s.store.RecordAgentInteractionResolutionAudit(store.AgentInteractionAuditInput{
		InteractionID: claim.ID, WorkspaceID: claim.WorkspaceID, AgentRunID: claim.AgentRunID,
		RequestID: claim.RequestID, Kind: claim.Kind, Status: status, Source: actor.Source,
		ActorID: actorID, ActorType: actor.ActorType, Response: response,
	})
	return nil
}

func (s *Service) warn(msg string, args ...any) {
	if s.logger != nil {
		s.logger.Warn(msg, args...)
	}
}

func classifyCurrent(current store.AgentInteractionRead, now time.Time) (ResolveResult, bool, error) {
	switch current.Status {
	case store.AgentInteractionStatusResolving:
		return ResolveResult{Interaction: current}, true, ErrAlreadyResolving
	case store.AgentInteractionStatusPending:
		if !current.ExpiresAt.After(now) {
			return ResolveResult{Interaction: current}, true, ErrExpired
		}
		return ResolveResult{}, false, nil
	default:
		return ResolveResult{Interaction: current, AlreadyResolved: true}, true, nil
	}
}

func validateDecision(current store.AgentInteractionRead, decision Decision) error {
	switch current.Kind {
	case store.AgentInteractionKindPermission:
		if decision.Approved == nil || decision.Cancelled || len(decision.QuestionAnswers) > 0 {
			return fmt.Errorf("%w: approved is required for a permission request", ErrInvalidDecision)
		}
		return nil
	case store.AgentInteractionKindUserChoice:
		if decision.Approved != nil {
			return fmt.Errorf("%w: approved is only valid for permission requests", ErrInvalidDecision)
		}
		if decision.Cancelled {
			if len(decision.QuestionAnswers) > 0 {
				return fmt.Errorf("%w: a cancelled request cannot include answers", ErrInvalidDecision)
			}
			return nil
		}
		questions := interactionQuestions(current.Request)
		if len(questions) == 0 || len(decision.QuestionAnswers) != len(questions) {
			return fmt.Errorf("%w: every question requires one answer", ErrInvalidDecision)
		}
		answers := make(map[string][]string, len(decision.QuestionAnswers))
		for _, answer := range decision.QuestionAnswers {
			id := strings.TrimSpace(answer.QuestionID)
			if id == "" || len(answer.Answers) == 0 {
				return fmt.Errorf("%w: every answer requires question_id and a non-empty answers array", ErrInvalidDecision)
			}
			if _, exists := answers[id]; exists {
				return fmt.Errorf("%w: duplicate question_id %q", ErrInvalidDecision, id)
			}
			values := cleanAnswers(answer.Answers)
			if len(values) == 0 {
				return fmt.Errorf("%w: question %q has no non-empty answer", ErrInvalidDecision, id)
			}
			answers[id] = values
		}
		for _, question := range questions {
			values, ok := answers[question.id]
			if !ok {
				return fmt.Errorf("%w: missing answer for question %q", ErrInvalidDecision, question.id)
			}
			if !question.multiSelect && len(values) != 1 {
				return fmt.Errorf("%w: question %q accepts exactly one answer", ErrInvalidDecision, question.id)
			}
			if !question.isOther {
				for _, value := range values {
					if _, allowed := question.options[value]; !allowed {
						return fmt.Errorf("%w: question %q does not allow a custom answer", ErrInvalidDecision, question.id)
					}
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported interaction kind", ErrInvalidDecision)
	}
}

type interactionQuestion struct {
	id          string
	multiSelect bool
	isOther     bool
	isSecret    bool
	options     map[string]struct{}
}

func interactionQuestions(request map[string]any) []interactionQuestion {
	raw, _ := request["questions"].([]any)
	out := make([]interactionQuestion, 0, len(raw))
	for index, value := range raw {
		question, _ := value.(map[string]any)
		id, _ := question["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			id = fmt.Sprintf("q%d", index)
		}
		multi, _ := question["multi_select"].(bool)
		// Older durable rows predate is_other and the Web card has always
		// treated an omitted flag as allowing free-form input. Only an
		// explicit false closes the option set.
		isOther := true
		if value, ok := question["is_other"].(bool); ok {
			isOther = value
		}
		isSecret, _ := question["is_secret"].(bool)
		options := make(map[string]struct{})
		if rawOptions, ok := question["options"].([]any); ok {
			for _, rawOption := range rawOptions {
				option, _ := rawOption.(map[string]any)
				label, _ := option["label"].(string)
				if label = strings.TrimSpace(label); label != "" {
					options[label] = struct{}{}
				}
			}
		}
		out = append(out, interactionQuestion{
			id: id, multiSelect: multi, isOther: isOther, isSecret: isSecret, options: options,
		})
	}
	return out
}

func cleanAnswers(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func normalizeAnswers(answers []QuestionAnswer) map[string][]string {
	out := make(map[string][]string, len(answers))
	for _, answer := range answers {
		out[strings.TrimSpace(answer.QuestionID)] = cleanAnswers(answer.Answers)
	}
	return out
}

func redactSecretAnswers(request map[string]any, answers map[string][]string) map[string][]string {
	secret := make(map[string]bool)
	for _, question := range interactionQuestions(request) {
		secret[question.id] = question.isSecret
	}
	out := make(map[string][]string, len(answers))
	for questionID, values := range answers {
		if secret[questionID] {
			out[questionID] = []string{"[REDACTED]"}
			continue
		}
		out[questionID] = append([]string(nil), values...)
	}
	return out
}
