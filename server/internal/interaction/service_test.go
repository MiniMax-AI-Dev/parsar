package interaction

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type fakeStore struct {
	mu sync.Mutex

	interaction   store.AgentInteractionRead
	claim         store.AgentInteractionClaim
	claimCount    int
	releaseCount  int
	staleReleases int64
	events        []store.RecordAgentRunEventInput
	audits        []store.AgentInteractionAuditInput
	cleared       []store.InflightSlotKind
}

func newFakeStore(kind string, request map[string]any, now time.Time) *fakeStore {
	return &fakeStore{
		interaction: store.AgentInteractionRead{
			ID: "interaction-1", WorkspaceID: "workspace-1", ConversationID: "conversation-1",
			AgentRunID: "run-1", RequestID: "request-1", Kind: kind,
			Status: store.AgentInteractionStatusPending, Request: request,
			CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute), UpdatedAt: now.Add(-time.Minute),
		},
		claim: store.AgentInteractionClaim{
			ID: "interaction-1", WorkspaceID: "workspace-1", ConversationID: "conversation-1",
			AgentRunID: "run-1", RequestID: "request-1", Kind: kind,
			DeviceID: "device-1", ClaimToken: "claim-1", Request: request,
		},
	}
}

func (s *fakeStore) GetAgentInteraction(_ context.Context, id string) (store.AgentInteractionRead, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.interaction.ID {
		return store.AgentInteractionRead{}, store.ErrUnknownAgentInteraction
	}
	return s.interaction, nil
}

func (s *fakeStore) GetAgentInteractionByRequestID(_ context.Context, kind, requestID, runID string) (store.AgentInteractionRead, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind != s.interaction.Kind || requestID != s.interaction.RequestID || runID != s.interaction.AgentRunID {
		return store.AgentInteractionRead{}, store.ErrUnknownAgentInteraction
	}
	return s.interaction, nil
}

func (s *fakeStore) ClaimAgentInteraction(_ context.Context, workspaceID, interactionID, userID, actorID, source string, now time.Time) (store.AgentInteractionClaim, error) {
	if workspaceID != s.interaction.WorkspaceID || interactionID != s.interaction.ID {
		return store.AgentInteractionClaim{}, store.ErrAgentInteractionNotPending
	}
	return s.claimPending(userID, actorID, source, now, false)
}

func (s *fakeStore) ClaimAgentInteractionByRequestID(_ context.Context, kind, requestID, runID, userID, actorID, source string, now time.Time) (store.AgentInteractionClaim, error) {
	if kind != s.interaction.Kind || requestID != s.interaction.RequestID || runID != s.interaction.AgentRunID {
		return store.AgentInteractionClaim{}, store.ErrAgentInteractionNotPending
	}
	return s.claimPending(userID, actorID, source, now, false)
}

func (s *fakeStore) ClaimExpiredAgentInteraction(_ context.Context, id string, now time.Time) (store.AgentInteractionClaim, error) {
	if id != s.interaction.ID {
		return store.AgentInteractionClaim{}, store.ErrAgentInteractionNotPending
	}
	return s.claimPending("", store.AgentInteractionSourceSystemTimeout, store.AgentInteractionSourceSystemTimeout, now, true)
}

func (s *fakeStore) claimPending(userID, actorID, source string, now time.Time, expired bool) (store.AgentInteractionClaim, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.interaction.Status != store.AgentInteractionStatusPending {
		return store.AgentInteractionClaim{}, store.ErrAgentInteractionNotPending
	}
	if expired != !s.interaction.ExpiresAt.After(now) {
		return store.AgentInteractionClaim{}, store.ErrAgentInteractionNotPending
	}
	s.interaction.Status = store.AgentInteractionStatusResolving
	s.interaction.ResolvedBy = userID
	s.interaction.ResolvedActor = actorID
	s.interaction.ResolutionSource = source
	s.claimCount++
	return s.claim, nil
}

func (s *fakeStore) CompleteAgentInteraction(_ context.Context, claim store.AgentInteractionClaim, status string, response map[string]any, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if claim.ClaimToken != s.claim.ClaimToken || s.interaction.Status != store.AgentInteractionStatusResolving {
		return store.ErrAgentInteractionNotPending
	}
	s.interaction.Status = status
	s.interaction.Response = response
	s.interaction.UpdatedAt = now
	s.interaction.ResolvedAt = &now
	return nil
}

func (s *fakeStore) ReleaseAgentInteractionClaim(_ context.Context, claim store.AgentInteractionClaim, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if claim.ClaimToken != s.claim.ClaimToken || s.interaction.Status != store.AgentInteractionStatusResolving {
		return nil
	}
	s.interaction.Status = store.AgentInteractionStatusPending
	s.interaction.ResolvedBy = ""
	s.interaction.ResolvedActor = ""
	s.interaction.ResolutionSource = ""
	s.releaseCount++
	return nil
}

func (s *fakeStore) ListExpiredPendingAgentInteractionIDs(_ context.Context, now time.Time, _ int32) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.interaction.Status == store.AgentInteractionStatusPending && !s.interaction.ExpiresAt.After(now) {
		return []string{s.interaction.ID}, nil
	}
	return nil, nil
}

func (s *fakeStore) ReleaseStaleAgentInteractionClaims(_ context.Context, _, _ time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.staleReleases > 0 && s.interaction.Status == store.AgentInteractionStatusResolving {
		s.interaction.Status = store.AgentInteractionStatusPending
		s.interaction.ResolvedBy = ""
		s.interaction.ResolvedActor = ""
		s.interaction.ResolutionSource = ""
	}
	return s.staleReleases, nil
}

func (s *fakeStore) ClearConversationInflightSlot(_ context.Context, _ string, slot store.InflightSlotKind, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleared = append(s.cleared, slot)
	return nil
}

func (s *fakeStore) RecordAgentRunEvent(_ context.Context, input store.RecordAgentRunEventInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, input)
	return nil
}

func (s *fakeStore) RecordAgentInteractionResolutionAudit(input store.AgentInteractionAuditInput) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audits = append(s.audits, input)
}

type fakeDelivery struct {
	mu          sync.Mutex
	permissions []connector.PermissionDecision
	choices     []connector.PromptForUserChoiceDecision
	err         error
	entered     chan struct{}
	release     chan struct{}
}

func (d *fakeDelivery) SubmitPermission(_ context.Context, _ string, decision connector.PermissionDecision) error {
	d.wait()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.permissions = append(d.permissions, decision)
	return d.err
}

func (d *fakeDelivery) SubmitPromptForUserChoice(_ context.Context, _ string, decision connector.PromptForUserChoiceDecision) error {
	d.wait()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.choices = append(d.choices, decision)
	return d.err
}

func (d *fakeDelivery) wait() {
	if d.entered != nil {
		select {
		case d.entered <- struct{}{}:
		default:
		}
	}
	if d.release != nil {
		<-d.release
	}
}

func TestResolvePermissionIsDurableAndIdempotent(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	fs := newFakeStore(store.AgentInteractionKindPermission, map[string]any{"title": "Run tests"}, now)
	delivery := &fakeDelivery{}
	svc := NewService(fs, delivery, nil)
	svc.now = func() time.Time { return now }
	approved := true
	req := ResolveRequest{
		WorkspaceID: "workspace-1", InteractionID: "interaction-1",
		Actor:    Actor{UserID: "user-1", ActorID: "user-1", Source: store.AgentInteractionSourceWeb},
		Decision: Decision{Approved: &approved, Note: "reviewed"},
	}
	result, err := svc.Resolve(context.Background(), req)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !result.Applied || result.Interaction.Status != store.AgentInteractionStatusApproved {
		t.Fatalf("result = %+v", result)
	}
	second, err := svc.Resolve(context.Background(), req)
	if err != nil || !second.AlreadyResolved || second.Applied {
		t.Fatalf("second Resolve = %+v, %v", second, err)
	}
	if len(delivery.permissions) != 1 || !delivery.permissions[0].Approved || delivery.permissions[0].By != "user-1" || delivery.permissions[0].DeviceID != "device-1" {
		t.Fatalf("permission deliveries = %+v", delivery.permissions)
	}
	if fs.claimCount != 1 || len(fs.events) != 1 || len(fs.audits) != 1 || len(fs.cleared) != 1 {
		t.Fatalf("side effects: claims=%d events=%d audits=%d cleared=%d", fs.claimCount, len(fs.events), len(fs.audits), len(fs.cleared))
	}
	if fs.audits[0].Source != store.AgentInteractionSourceWeb || fs.audits[0].ActorID != "user-1" || fs.audits[0].Status != store.AgentInteractionStatusApproved {
		t.Fatalf("audit = %+v", fs.audits[0])
	}
}

func TestResolveQuestionPreservesStableIDsAndAnswerArrays(t *testing.T) {
	now := time.Now().UTC()
	request := map[string]any{"questions": []any{
		map[string]any{"id": "environment", "multi_select": false},
		map[string]any{"id": "checks", "multi_select": true},
	}}
	fs := newFakeStore(store.AgentInteractionKindUserChoice, request, now)
	delivery := &fakeDelivery{}
	svc := NewService(fs, delivery, nil)
	svc.now = func() time.Time { return now }
	result, err := svc.Resolve(context.Background(), ResolveRequest{
		Kind: store.AgentInteractionKindUserChoice, RequestID: "request-1", AgentRunID: "run-1",
		Actor: Actor{ActorID: "ou_1", Source: store.AgentInteractionSourceFeishu},
		Decision: Decision{QuestionAnswers: []QuestionAnswer{
			{QuestionID: "checks", Answers: []string{"unit", "integration"}},
			{QuestionID: "environment", Answers: []string{"staging"}},
		}},
	})
	if err != nil || !result.Applied || result.Interaction.Status != store.AgentInteractionStatusAnswered {
		t.Fatalf("Resolve = %+v, %v", result, err)
	}
	if len(delivery.choices) != 1 {
		t.Fatalf("choices = %+v", delivery.choices)
	}
	got := delivery.choices[0].QuestionAnswers
	if delivery.choices[0].DeviceID != "device-1" {
		t.Fatalf("choice device = %q", delivery.choices[0].DeviceID)
	}
	want := []connector.PromptForUserChoiceQuestionAnswer{
		{QuestionID: "checks", Answers: []string{"unit", "integration"}, Answer: "unit, integration"},
		{QuestionID: "environment", Answers: []string{"staging"}, Answer: "staging"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("answers = %+v, want %+v", got, want)
	}
}

func TestResolveQuestionRejectsInvalidStructuredAnswersBeforeClaim(t *testing.T) {
	now := time.Now().UTC()
	request := map[string]any{"questions": []any{
		map[string]any{"id": "environment", "multi_select": false},
		map[string]any{"id": "checks", "multi_select": true},
	}}
	tests := []struct {
		name    string
		answers []QuestionAnswer
	}{
		{name: "missing question", answers: []QuestionAnswer{{QuestionID: "environment", Answers: []string{"staging"}}}},
		{name: "duplicate id", answers: []QuestionAnswer{{QuestionID: "environment", Answers: []string{"staging"}}, {QuestionID: "environment", Answers: []string{"prod"}}}},
		{name: "empty value", answers: []QuestionAnswer{{QuestionID: "environment", Answers: []string{" "}}, {QuestionID: "checks", Answers: []string{"unit"}}}},
		{name: "multiple single select", answers: []QuestionAnswer{{QuestionID: "environment", Answers: []string{"staging", "prod"}}, {QuestionID: "checks", Answers: []string{"unit"}}}},
		{name: "unknown id", answers: []QuestionAnswer{{QuestionID: "other", Answers: []string{"staging"}}, {QuestionID: "checks", Answers: []string{"unit"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := newFakeStore(store.AgentInteractionKindUserChoice, request, now)
			delivery := &fakeDelivery{}
			svc := NewService(fs, delivery, nil)
			svc.now = func() time.Time { return now }
			_, err := svc.Resolve(context.Background(), ResolveRequest{
				WorkspaceID: "workspace-1", InteractionID: "interaction-1",
				Decision: Decision{QuestionAnswers: tt.answers},
			})
			if !errors.Is(err, ErrInvalidDecision) {
				t.Fatalf("error = %v, want ErrInvalidDecision", err)
			}
			if fs.claimCount != 0 || len(delivery.choices) != 0 {
				t.Fatalf("invalid decision reached side effects: claims=%d choices=%d", fs.claimCount, len(delivery.choices))
			}
		})
	}
}

func TestResolveQuestionRejectsContradictoryDecision(t *testing.T) {
	now := time.Now().UTC()
	request := map[string]any{"questions": []any{map[string]any{"id": "environment"}}}
	approved := true
	for _, decision := range []Decision{
		{Approved: &approved, QuestionAnswers: []QuestionAnswer{{QuestionID: "environment", Answers: []string{"staging"}}}},
		{Cancelled: true, QuestionAnswers: []QuestionAnswer{{QuestionID: "environment", Answers: []string{"staging"}}}},
	} {
		fs := newFakeStore(store.AgentInteractionKindUserChoice, request, now)
		svc := NewService(fs, &fakeDelivery{}, nil)
		svc.now = func() time.Time { return now }
		if _, err := svc.Resolve(context.Background(), ResolveRequest{
			WorkspaceID: "workspace-1", InteractionID: "interaction-1", Decision: decision,
		}); !errors.Is(err, ErrInvalidDecision) {
			t.Fatalf("decision %+v error = %v, want ErrInvalidDecision", decision, err)
		}
		if fs.claimCount != 0 {
			t.Fatalf("contradictory decision reached claim: %+v", decision)
		}
	}
}

func TestConcurrentResolversHaveOneDeliveryWinner(t *testing.T) {
	now := time.Now().UTC()
	fs := newFakeStore(store.AgentInteractionKindPermission, nil, now)
	delivery := &fakeDelivery{entered: make(chan struct{}, 1), release: make(chan struct{})}
	svc := NewService(fs, delivery, nil)
	svc.now = func() time.Time { return now }
	approved := true
	req := ResolveRequest{WorkspaceID: "workspace-1", InteractionID: "interaction-1", Decision: Decision{Approved: &approved}}

	firstDone := make(chan error, 1)
	go func() {
		_, err := svc.Resolve(context.Background(), req)
		firstDone <- err
	}()
	<-delivery.entered
	_, secondErr := svc.Resolve(context.Background(), req)
	if !errors.Is(secondErr, ErrAlreadyResolving) {
		t.Fatalf("second error = %v, want ErrAlreadyResolving", secondErr)
	}
	close(delivery.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	if fs.claimCount != 1 || len(delivery.permissions) != 1 {
		t.Fatalf("claims/deliveries = %d/%d, want 1/1", fs.claimCount, len(delivery.permissions))
	}
}

func TestTemporaryDeliveryFailureReleasesClaimForRetry(t *testing.T) {
	now := time.Now().UTC()
	fs := newFakeStore(store.AgentInteractionKindPermission, nil, now)
	delivery := &fakeDelivery{err: errors.New("offline")}
	svc := NewService(fs, delivery, nil)
	svc.now = func() time.Time { return now }
	approved := false
	req := ResolveRequest{WorkspaceID: "workspace-1", InteractionID: "interaction-1", Decision: Decision{Approved: &approved}}
	if _, err := svc.Resolve(context.Background(), req); !errors.Is(err, ErrRuntimeUnavailable) {
		t.Fatalf("first error = %v, want ErrRuntimeUnavailable", err)
	}
	if fs.interaction.Status != store.AgentInteractionStatusPending || fs.releaseCount != 1 {
		t.Fatalf("released state = %q, releases=%d", fs.interaction.Status, fs.releaseCount)
	}
	delivery.err = nil
	if result, err := svc.Resolve(context.Background(), req); err != nil || !result.Applied {
		t.Fatalf("retry = %+v, %v", result, err)
	}
}

func TestRuntimeGoneClosesCanonicalInteraction(t *testing.T) {
	now := time.Now().UTC()
	fs := newFakeStore(store.AgentInteractionKindPermission, nil, now)
	delivery := &fakeDelivery{err: connector.ErrInteractionNoLongerPending}
	svc := NewService(fs, delivery, nil)
	svc.now = func() time.Time { return now }
	approved := true
	result, err := svc.Resolve(context.Background(), ResolveRequest{
		WorkspaceID: "workspace-1", InteractionID: "interaction-1", Decision: Decision{Approved: &approved},
	})
	if !errors.Is(err, ErrRuntimeGone) || !result.AlreadyResolved {
		t.Fatalf("Resolve = %+v, %v", result, err)
	}
	if fs.interaction.Status != store.AgentInteractionStatusCancelled || fs.releaseCount != 0 {
		t.Fatalf("state = %q, releases=%d", fs.interaction.Status, fs.releaseCount)
	}
}

func TestWorkerExpiresPendingInteractionThroughRuntime(t *testing.T) {
	now := time.Now().UTC()
	fs := newFakeStore(store.AgentInteractionKindUserChoice, map[string]any{"questions": []any{map[string]any{"id": "q0"}}}, now)
	fs.interaction.ExpiresAt = now.Add(-time.Second)
	delivery := &fakeDelivery{}
	svc := NewService(fs, delivery, nil)
	svc.now = func() time.Time { return now }
	worker := NewWorker(svc, fs, WorkerOptions{ClaimLease: time.Minute, BatchSize: 10})
	if err := worker.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if fs.interaction.Status != store.AgentInteractionStatusExpired {
		t.Fatalf("status = %q, want expired", fs.interaction.Status)
	}
	if len(delivery.choices) != 1 || !delivery.choices[0].Cancelled {
		t.Fatalf("expiry decisions = %+v", delivery.choices)
	}
	if expired, _ := fs.interaction.Response["expired"].(bool); !expired {
		t.Fatalf("response = %+v", fs.interaction.Response)
	}
}

func TestExpirePermissionExplicitlyDenies(t *testing.T) {
	now := time.Now().UTC()
	fs := newFakeStore(store.AgentInteractionKindPermission, nil, now)
	fs.interaction.ExpiresAt = now.Add(-time.Second)
	delivery := &fakeDelivery{}
	svc := NewService(fs, delivery, nil)
	svc.now = func() time.Time { return now }
	if err := svc.Expire(context.Background(), fs.interaction.ID); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if fs.interaction.Status != store.AgentInteractionStatusExpired || len(delivery.permissions) != 1 || delivery.permissions[0].Approved {
		t.Fatalf("status/decisions = %q/%+v", fs.interaction.Status, delivery.permissions)
	}
}

func TestWorkerRecoversStaleExpiredClaimAfterRestart(t *testing.T) {
	now := time.Now().UTC()
	fs := newFakeStore(store.AgentInteractionKindPermission, nil, now)
	fs.interaction.Status = store.AgentInteractionStatusResolving
	fs.interaction.ExpiresAt = now.Add(-time.Second)
	fs.staleReleases = 1
	delivery := &fakeDelivery{}
	svc := NewService(fs, delivery, nil)
	svc.now = func() time.Time { return now }
	worker := NewWorker(svc, fs, WorkerOptions{ClaimLease: time.Minute, BatchSize: 10})
	if err := worker.Sweep(context.Background()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if fs.interaction.Status != store.AgentInteractionStatusExpired || len(delivery.permissions) != 1 {
		t.Fatalf("recovered state = %q, deliveries=%+v", fs.interaction.Status, delivery.permissions)
	}
}
