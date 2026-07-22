package inbound

import (
	"context"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/interaction"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type stubInteractionResolver struct {
	requests []interaction.ResolveRequest
	result   interaction.ResolveResult
	err      error
}

func (s *stubInteractionResolver) Resolve(_ context.Context, request interaction.ResolveRequest) (interaction.ResolveResult, error) {
	s.requests = append(s.requests, request)
	return s.result, s.err
}

func TestIMPermissionUsesCanonicalResolver(t *testing.T) {
	fs := newInboundFakeStore()
	fs.cardsByPermReq["perm-canonical"] = store.ConversationInflightCards{
		ConversationID: "conv-1", HasPermission: true,
		Permission: store.PermissionInflightSlot{PermissionRequestID: "perm-canonical", AgentRunID: "run-1"},
	}
	legacy := &stubPermissionRouter{}
	resolver := &stubInteractionResolver{result: interaction.ResolveResult{
		Applied: true, Interaction: store.AgentInteractionRead{Status: store.AgentInteractionStatusApproved},
	}}
	mgr, err := NewManager(Options{
		Store: fs, Secrets: inboundFakeDecrypter{}, PermissionRouter: legacy, InteractionResolver: resolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := mgr.cardActionRouter().RouteAction(context.Background(), channel.CardAction{
		Kind: channel.CardActionPermissionAllow, Platform: channel.PlatformSlack, OperatorID: "U_operator",
		Values: map[string]string{"permission_request_id": "perm-canonical"},
	})
	if err != nil {
		t.Fatalf("RouteAction: %v", err)
	}
	if len(resolver.requests) != 1 {
		t.Fatalf("resolver requests = %d, want 1", len(resolver.requests))
	}
	req := resolver.requests[0]
	if req.RequestID != "perm-canonical" || req.AgentRunID != "run-1" || req.Actor.ActorID != "U_operator" || req.Actor.Source != store.AgentInteractionSourceSlack {
		t.Fatalf("resolver request = %+v", req)
	}
	if req.Decision.Approved == nil || !*req.Decision.Approved {
		t.Fatalf("decision = %+v", req.Decision)
	}
	if len(legacy.calls) != 0 {
		t.Fatalf("legacy router calls = %+v", legacy.calls)
	}
	if len(fs.permissionClears) != 0 {
		t.Fatalf("IM route duplicated canonical slot clear: %+v", fs.permissionClears)
	}
	if ack.ToastKind != "success" || ack.Result == nil || !ack.Result.Approved {
		t.Fatalf("ack = %+v", ack)
	}
}

func TestIMUserChoiceUsesStableQuestionIDsAndArrays(t *testing.T) {
	fs := newInboundFakeStore()
	fs.cardsByPromptForUserChoiceReq["ask-canonical"] = store.ConversationInflightCards{
		ConversationID: "conv-1", HasPromptForUserChoice: true,
		PromptForUserChoice: store.PromptForUserChoiceInflightSlot{
			RequestID: "ask-canonical", AgentRunID: "run-1",
			Questions: []store.PromptForUserChoiceQuestion{
				{ID: "environment", Header: "Environment", MultiSelect: false},
				{ID: "checks", Header: "Checks", MultiSelect: true},
			},
		},
	}
	legacy := &stubPromptForUserChoiceRouter{}
	resolver := &stubInteractionResolver{result: interaction.ResolveResult{
		Applied: true, Interaction: store.AgentInteractionRead{Status: store.AgentInteractionStatusAnswered},
	}}
	mgr, err := NewManager(Options{
		Store: fs, Secrets: inboundFakeDecrypter{}, PromptForUserChoiceRouter: legacy, InteractionResolver: resolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := mgr.cardActionRouter().RouteAction(context.Background(), channel.CardAction{
		Kind: channel.CardActionUserChoiceSubmit, Platform: channel.PlatformDiscord, OperatorID: "discord-user",
		Values:     map[string]string{"request_id": "ask-canonical"},
		FormValues: map[string]any{"q0": "staging", "q1": "unit, integration"},
	})
	if err != nil {
		t.Fatalf("RouteAction: %v", err)
	}
	if len(resolver.requests) != 1 {
		t.Fatalf("resolver requests = %d, want 1", len(resolver.requests))
	}
	req := resolver.requests[0]
	if req.AgentRunID != "run-1" || req.Actor.Source != store.AgentInteractionSourceDiscord {
		t.Fatalf("resolver request = %+v", req)
	}
	answers := map[string][]string{}
	for _, answer := range req.Decision.QuestionAnswers {
		answers[answer.QuestionID] = answer.Answers
	}
	if len(answers["environment"]) != 1 || answers["environment"][0] != "staging" {
		t.Fatalf("environment answer = %+v", answers["environment"])
	}
	if len(answers["checks"]) != 2 || answers["checks"][0] != "unit" || answers["checks"][1] != "integration" {
		t.Fatalf("checks answers = %+v", answers["checks"])
	}
	if len(legacy.calls) != 0 || len(fs.permissionClears) != 0 {
		t.Fatalf("legacy side effects = calls:%+v clears:%+v", legacy.calls, fs.permissionClears)
	}
	if ack.ToastKind != "success" || ack.Result == nil {
		t.Fatalf("ack = %+v", ack)
	}
}

func TestIMResolverRaceLoserDoesNotPatchOrClear(t *testing.T) {
	fs := newInboundFakeStore()
	fs.cardsByPermReq["perm-race"] = store.ConversationInflightCards{
		ConversationID: "conv-1", HasPermission: true,
		Permission: store.PermissionInflightSlot{PermissionRequestID: "perm-race", AgentRunID: "run-1"},
	}
	resolver := &stubInteractionResolver{err: interaction.ErrAlreadyResolving}
	mgr, err := NewManager(Options{Store: fs, Secrets: inboundFakeDecrypter{}, InteractionResolver: resolver})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := mgr.cardActionRouter().RouteAction(context.Background(), channel.CardAction{
		Kind: channel.CardActionPermissionAllow, Platform: channel.PlatformSlack,
		Values: map[string]string{"permission_request_id": "perm-race"},
	})
	if err != nil {
		t.Fatalf("RouteAction: %v", err)
	}
	if ack.ToastKind != "info" || len(fs.permissionClears) != 0 {
		t.Fatalf("ack/clears = %+v/%+v", ack, fs.permissionClears)
	}
}
