package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAgentInteractionLifecycleIsDurableAndSingleWinner(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := mustSeedDevFixture(t, ctx, store)

	sendResult, err := store.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
		ConversationID:    ids.ConversationID,
		UserID:            ids.UserID,
		Content:           "@product-agent interaction lifecycle",
		MentionedAgentIDs: []string{ids.ProductAgentID},
	})
	if err != nil {
		t.Fatalf("send user message: %v", err)
	}
	if len(sendResult.RunIDs) == 0 {
		t.Fatal("expected an agent run")
	}
	runID := sendResult.RunIDs[0]
	occurredAt := time.Now().UTC()
	payload := map[string]any{
		"request_id": "perm-lifecycle-test",
		"device_id":  "device-web-owner",
		"action":     "command_execution",
		"resource":   "go test ./...",
	}
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID: runID, EventKind: "permission.asked", Payload: payload, OccurredAt: occurredAt,
	}); err != nil {
		t.Fatalf("record permission request: %v", err)
	}
	// A retried stream event may be recorded twice, but the canonical request
	// remains unique by kind and request_id.
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID: runID, EventKind: "permission.asked", Payload: payload, OccurredAt: occurredAt.Add(time.Millisecond),
	}); err != nil {
		t.Fatalf("record duplicate permission request: %v", err)
	}

	pending, err := store.ListWorkspaceAgentInteractions(ctx, ids.WorkspaceID, "pending", 100)
	if err != nil {
		t.Fatalf("list pending interactions: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending interactions = %d, want 1", len(pending))
	}
	interaction := pending[0]
	if interaction.AgentRunID != runID || interaction.RequestID != "perm-lifecycle-test" || interaction.Kind != AgentInteractionKindPermission {
		t.Fatalf("interaction = %+v", interaction)
	}
	if interaction.ExpiresAt.Sub(interaction.CreatedAt) != AgentInteractionTTL {
		t.Fatalf("interaction lifetime = %s, want %s", interaction.ExpiresAt.Sub(interaction.CreatedAt), AgentInteractionTTL)
	}
	deviceID, err := store.DeviceIDForPermissionRequest(ctx, interaction.RequestID)
	if err != nil {
		t.Fatalf("resolve canonical device: %v", err)
	}
	if deviceID != "device-web-owner" {
		t.Fatalf("device id = %q", deviceID)
	}

	claim, err := store.ClaimAgentInteraction(ctx, ids.WorkspaceID, interaction.ID, ids.UserID, "web-user", AgentInteractionSourceWeb, time.Now().UTC())
	if err != nil {
		t.Fatalf("claim interaction: %v", err)
	}
	if claim.RequestID != interaction.RequestID || claim.AgentRunID != runID {
		t.Fatalf("claim = %+v", claim)
	}
	if _, err := store.ClaimAgentInteraction(ctx, ids.WorkspaceID, interaction.ID, ids.UserID, "web-user", AgentInteractionSourceWeb, time.Now().UTC()); !errors.Is(err, ErrAgentInteractionNotPending) {
		t.Fatalf("second claim error = %v, want ErrAgentInteractionNotPending", err)
	}

	response := map[string]any{"approved": true, "note": "approved in Web"}
	if err := store.CompleteAgentInteraction(ctx, claim, AgentInteractionStatusApproved, response, time.Now().UTC()); err != nil {
		t.Fatalf("complete interaction: %v", err)
	}
	decided, err := store.ListWorkspaceAgentInteractions(ctx, ids.WorkspaceID, "decided", 100)
	if err != nil {
		t.Fatalf("list decided interactions: %v", err)
	}
	if len(decided) != 1 || decided[0].Status != AgentInteractionStatusApproved || decided[0].ResolvedBy != ids.UserID {
		t.Fatalf("decided interactions = %+v", decided)
	}
	if decided[0].ResolutionSource != AgentInteractionSourceWeb || decided[0].ResolvedActor != "web-user" {
		t.Fatalf("resolution provenance = %q/%q", decided[0].ResolutionSource, decided[0].ResolvedActor)
	}
	if approved, _ := decided[0].Response["approved"].(bool); !approved {
		t.Fatalf("response = %+v", decided[0].Response)
	}

	askPayload := map[string]any{
		"request_id": "ask-terminal-test", "device_id": "device-web-owner",
		"auto_resolution_ms": uint64(120_000),
		"questions":          []any{map[string]any{"id": "environment", "question": "Where?"}},
	}
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID: runID, EventKind: "prompt_for_user_choice.asked", Payload: askPayload, OccurredAt: occurredAt.Add(time.Second),
	}); err != nil {
		t.Fatalf("record user question: %v", err)
	}
	ask, err := store.GetAgentInteractionByRequestID(ctx, AgentInteractionKindUserChoice, "ask-terminal-test", runID)
	if err != nil {
		t.Fatalf("get question by run/request: %v", err)
	}
	questions, _ := ask.Request["questions"].([]any)
	if len(questions) != 1 || questions[0].(map[string]any)["id"] != "environment" {
		t.Fatalf("durable question snapshot = %+v", ask.Request)
	}
	if lifetime := ask.ExpiresAt.Sub(ask.CreatedAt); lifetime != 2*time.Minute {
		t.Fatalf("question lifetime = %s, want 2m", lifetime)
	}
	if _, err := store.CancelAgentRun(ctx, runID, "operator_cancelled"); err != nil {
		t.Fatalf("cancel run row: %v", err)
	}
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID: runID, EventKind: "run.cancelled", Payload: map[string]any{"reason": "operator_cancelled"}, OccurredAt: occurredAt.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("record terminal event: %v", err)
	}
	closed, err := store.ListWorkspaceAgentInteractions(ctx, ids.WorkspaceID, "expired", 100)
	if err != nil {
		t.Fatalf("list terminal interactions: %v", err)
	}
	if len(closed) != 1 || closed[0].ID != ask.ID || closed[0].Status != AgentInteractionStatusCancelled {
		t.Fatalf("terminal interactions = %+v", closed)
	}
	if closed[0].ResolutionSource != AgentInteractionSourceRuntime || closed[0].Response["reason"] != "operator_cancelled" {
		t.Fatalf("terminal provenance/response = %q/%+v", closed[0].ResolutionSource, closed[0].Response)
	}
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID: runID, EventKind: "prompt_for_user_choice.asked",
		Payload:    map[string]any{"request_id": "ask-after-terminal", "device_id": "device-web-owner"},
		OccurredAt: occurredAt.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("record late question event: %v", err)
	}
	if _, err := store.GetAgentInteractionByRequestID(ctx, AgentInteractionKindUserChoice, "ask-after-terminal", runID); !errors.Is(err, ErrUnknownAgentInteraction) {
		t.Fatalf("late terminal interaction error = %v, want ErrUnknownAgentInteraction", err)
	}
}
