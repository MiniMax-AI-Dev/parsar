package inbound

import (
	"context"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// N9 — the route* handlers gate the post-resolution card render on
// action.Platform: Feishu keeps its inline native ReplaceCard (byte-identical
// legacy path), every other platform gets a NEUTRAL ActionResultCard the
// channel renders itself. The business side-effects (SubmitPermission, claim,
// persist, clear slot) run identically for both platforms — only the render
// branch differs. These tests drive RouteAction directly with Platform=slack
// (no SDK transport) and assert the neutral Result is filled, no ReplaceCard
// is produced, and no Feishu PATCH is attempted (no OpenAPIBaseURL is wired, so
// a stray patch would fail loudly). The existing handleCardAction_* suite
// already guards the Platform=feishu bytes.

func TestRouteAction_SlackPermissionAllowFillsNeutralResult(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	fs.cardsByPermReq["perm-slack-1"] = store.ConversationInflightCards{
		ConversationID: "conv-slack",
		WorkspaceID:    "ws-1",
		HasPermission:  true,
		Permission: store.PermissionInflightSlot{
			ExternalMsgID:       "ts-1",
			PermissionRequestID: "perm-slack-1",
			AgentRunID:          "run-slack",
		},
	}
	fs.agentNameByConversation = map[string]string{"conv-slack": "Slack Agent"}
	router := &stubPermissionRouter{}
	mgr, err := NewManager(Options{
		Store:            fs,
		Secrets:          inboundFakeDecrypter{},
		PermissionRouter: router,
	})
	if err != nil {
		t.Fatal(err)
	}

	ack, err := mgr.cardActionRouter().RouteAction(context.Background(), channel.CardAction{
		Kind:       channel.CardActionPermissionAllow,
		Platform:   channel.PlatformSlack,
		OperatorID: "U_op",
		Values:     map[string]string{"permission_request_id": "perm-slack-1"},
	})
	if err != nil {
		t.Fatalf("RouteAction: %v", err)
	}
	// Business side-effects identical to Feishu.
	if len(router.calls) != 1 || !router.calls[0].Approved {
		t.Fatalf("router calls = %+v, want one Approved=true", router.calls)
	}
	if len(fs.permissionClears) != 1 || fs.permissionClears[0].Slot != store.InflightSlotPermission {
		t.Errorf("clears = %+v, want one Permission clear", fs.permissionClears)
	}
	// Neutral render, never the Feishu native card.
	if len(ack.ReplaceCard) != 0 {
		t.Errorf("ReplaceCard = %s, want empty on the Slack path", ack.ReplaceCard)
	}
	if ack.Result == nil {
		t.Fatal("ack.Result = nil, want a neutral ActionResultCard")
	}
	if ack.Result.Kind != channel.CardActionPermissionAllow || !ack.Result.Approved {
		t.Errorf("Result = %+v, want permission_allow / Approved=true", ack.Result)
	}
	if ack.Result.Title != "Slack Agent" {
		t.Errorf("Result.Title = %q, want Slack Agent", ack.Result.Title)
	}
}

func TestRouteAction_SlackPermissionDenyResultNotApproved(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	fs.cardsByPermReq["perm-slack-2"] = store.ConversationInflightCards{
		ConversationID: "conv-slack",
		HasPermission:  true,
		Permission: store.PermissionInflightSlot{
			ExternalMsgID:       "ts-2",
			PermissionRequestID: "perm-slack-2",
		},
	}
	router := &stubPermissionRouter{}
	mgr, _ := NewManager(Options{Store: fs, Secrets: inboundFakeDecrypter{}, PermissionRouter: router})

	ack, err := mgr.cardActionRouter().RouteAction(context.Background(), channel.CardAction{
		Kind:       channel.CardActionPermissionDeny,
		Platform:   channel.PlatformSlack,
		OperatorID: "U_op",
		Values:     map[string]string{"permission_request_id": "perm-slack-2"},
	})
	if err != nil {
		t.Fatalf("RouteAction: %v", err)
	}
	if len(router.calls) != 1 || router.calls[0].Approved {
		t.Fatalf("router calls = %+v, want one Approved=false", router.calls)
	}
	if ack.Result == nil || ack.Result.Approved {
		t.Errorf("Result = %+v, want Approved=false", ack.Result)
	}
	if len(ack.ReplaceCard) != 0 {
		t.Errorf("ReplaceCard = %s, want empty", ack.ReplaceCard)
	}
}

func TestRouteAction_SlackCredentialSubmitFillsNeutralResult(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	fs.pendingForms = map[string]store.ClaimedPendingCredentialForm{
		"qkey_slack": {
			Slot: store.PendingCredentialFormSlot{
				Qkey:            "qkey_slack",
				InitiatorOpenID: "U_bob",
				InitiatorUserID: "user-bob",
				AgentID:         "agt-1",
				RawQuery:        "list PRs",
				ExpiresAt:       time.Now().UTC().Add(time.Hour),
			},
			ConversationID: "conv-slack",
			WorkspaceID:    "ws-1",
			ExternalChatID: "C_chat",
			SourceAppID:    "slack_app",
		},
	}
	fs.agentNameByConversation = map[string]string{"conv-slack": "Slack Agent"}
	mgr, _ := NewManager(Options{Store: fs, Secrets: inboundFakeDecrypter{}})

	ack, err := mgr.cardActionRouter().RouteAction(context.Background(), channel.CardAction{
		Kind:           channel.CardActionCredentialSubmit,
		Platform:       channel.PlatformSlack,
		OperatorID:     "U_bob",
		ExternalChatID: "C_chat",
		Values:         map[string]string{"qkey": "qkey_slack"},
		FormValues:     map[string]any{"credential_github_pat": "ghp_real"},
	})
	if err != nil {
		t.Fatalf("RouteAction: %v", err)
	}
	if len(fs.userCredentialsCreated) != 1 {
		t.Fatalf("credentials persisted = %d, want 1", len(fs.userCredentialsCreated))
	}
	if len(fs.created) != 1 {
		t.Fatalf("re-enqueued inbound = %d, want 1", len(fs.created))
	}
	if len(ack.ReplaceCard) != 0 {
		t.Errorf("ReplaceCard = %s, want empty on the Slack path", ack.ReplaceCard)
	}
	if ack.Result == nil {
		t.Fatal("ack.Result = nil, want a neutral ActionResultCard")
	}
	if ack.Result.Rejected {
		t.Errorf("Result.Rejected = true, want false on a successful submit")
	}
	if ack.Result.Summary == "" {
		t.Errorf("Result.Summary empty, want the continue-session summary")
	}
}

func TestRouteAction_SlackCredentialOperatorMismatchRejects(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	fs.pendingForms = map[string]store.ClaimedPendingCredentialForm{
		"qkey_slack": {
			Slot: store.PendingCredentialFormSlot{
				Qkey:            "qkey_slack",
				InitiatorOpenID: "U_bob",
				InitiatorUserID: "user-bob",
				AgentID:         "agt-1",
				ExpiresAt:       time.Now().UTC().Add(time.Hour),
			},
			ConversationID: "conv-slack",
		},
	}
	mgr, _ := NewManager(Options{Store: fs, Secrets: inboundFakeDecrypter{}})

	ack, err := mgr.cardActionRouter().RouteAction(context.Background(), channel.CardAction{
		Kind:       channel.CardActionCredentialSubmit,
		Platform:   channel.PlatformSlack,
		OperatorID: "U_attacker", // != stash initiator
		Values:     map[string]string{"qkey": "qkey_slack"},
		FormValues: map[string]any{"credential_github_pat": "ghp_real"},
	})
	if err != nil {
		t.Fatalf("RouteAction: %v", err)
	}
	// Authorization gate ran: no credentials persisted, qkey consumed once.
	if len(fs.userCredentialsCreated) != 0 {
		t.Errorf("credentials persisted = %d, want 0 (rejected)", len(fs.userCredentialsCreated))
	}
	if len(ack.ReplaceCard) != 0 {
		t.Errorf("ReplaceCard = %s, want empty on the Slack path", ack.ReplaceCard)
	}
	if ack.Result == nil || !ack.Result.Rejected {
		t.Fatalf("Result = %+v, want Rejected=true", ack.Result)
	}
	if ack.Result.RejectReason == "" {
		t.Errorf("Result.RejectReason empty, want a user-facing reason")
	}
}

func TestRouteAction_SlackUserChoiceSubmitFillsNeutralResult(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	seedUserChoiceSlot(fs, "req-slack", "conv-slack", "run-uc")
	fs.agentNameByConversation = map[string]string{"conv-slack": "Slack Agent"}
	router := &stubPromptForUserChoiceRouter{}
	mgr, _ := NewManager(Options{Store: fs, Secrets: inboundFakeDecrypter{}, PromptForUserChoiceRouter: router})

	ack, err := mgr.cardActionRouter().RouteAction(context.Background(), channel.CardAction{
		Kind:       channel.CardActionUserChoiceSubmit,
		Platform:   channel.PlatformSlack,
		OperatorID: "U_picker",
		Values:     map[string]string{"request_id": "req-slack"},
		FormValues: map[string]any{"q0": "选项A"},
	})
	if err != nil {
		t.Fatalf("RouteAction: %v", err)
	}
	if len(router.calls) != 1 || router.calls[0].Cancelled {
		t.Fatalf("router calls = %+v, want one non-cancelled decision", router.calls)
	}
	if len(fs.permissionClears) != 1 || fs.permissionClears[0].Slot != store.InflightSlotPromptForUserChoice {
		t.Errorf("clears = %+v, want one prompt_for_user_choice clear", fs.permissionClears)
	}
	if len(ack.ReplaceCard) != 0 {
		t.Errorf("ReplaceCard = %s, want empty on the Slack path", ack.ReplaceCard)
	}
	if ack.Result == nil || ack.Result.Summary == "" {
		t.Fatalf("Result = %+v, want a non-empty Summary", ack.Result)
	}
}

// TestRouteAction_FeishuChoiceKeepsNativeReplaceCard is the explicit gating
// guard: the same RouteAction with Platform=feishu must take the legacy branch
// — native ReplaceCard set, neutral Result nil — so the Slack gating never
// bleeds into the Feishu render. (buildPromptForUserChoiceDoneCardMap is pure,
// so no network is involved.)
func TestRouteAction_FeishuChoiceKeepsNativeReplaceCard(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	seedUserChoiceSlot(fs, "req-feishu", "conv-feishu", "run-uc")
	router := &stubPromptForUserChoiceRouter{}
	mgr, _ := NewManager(Options{Store: fs, Secrets: inboundFakeDecrypter{}, PromptForUserChoiceRouter: router})

	ack, err := mgr.cardActionRouter().RouteAction(context.Background(), channel.CardAction{
		Kind:       channel.CardActionUserChoiceSubmit,
		Platform:   channel.PlatformFeishu,
		OperatorID: "ou_picker",
		Values:     map[string]string{"request_id": "req-feishu"},
		FormValues: map[string]any{"q0": "选项A"},
	})
	if err != nil {
		t.Fatalf("RouteAction: %v", err)
	}
	if len(ack.ReplaceCard) == 0 {
		t.Error("ReplaceCard empty on the Feishu path, want the native done card")
	}
	if ack.Result != nil {
		t.Errorf("Result = %+v, want nil on the Feishu path", ack.Result)
	}
}
