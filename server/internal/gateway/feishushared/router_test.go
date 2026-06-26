package feishushared

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type fakeSharedStore struct {
	agents       []store.FeishuSharedBotAgent
	routes       map[string]store.FeishuAgentRoute
	userByUnion  map[string]string
	memberships  map[string]bool
	selections   map[string]string
	created      []store.CreateInboundIMMessageInput
	lastListUser string
	lastExclude  string
	// createErr forces CreateInboundIMMessage to return this error
	// instead of the success path. Tests covering the binding-loss
	// degrade branch set this to store.ErrUnknownMention.
	createErr error

	// conversationByExternalRef maps (chat_id|thread_key) → conversation_id
	// for the /cancel branch. An entry's absence makes
	// FindConversationByExternalRef return ErrUnknownConversation,
	// which the cancel handler renders as "no in-flight task" reply.
	conversationByExternalRef map[string]string
	// cancelledRunsByConversation seeds CancelAllInflightForConversation's
	// return value per conversation id, so tests can assert the bulk
	// cancel branch and the "no in-flight" branch independently.
	cancelledRunsByConversation map[string][]store.SupersededRun
	// cancelCalls records (conversation_id, reason) pairs for assertions.
	cancelCalls []struct{ ConversationID, Reason string }
}

func newFakeSharedStore() *fakeSharedStore {
	return &fakeSharedStore{
		agents: []store.FeishuSharedBotAgent{
			{
				AgentID:        "agent-product",
				WorkspaceID:    "workspace-1",
				WorkspaceName:  "Demo Workspace",
				WorkspaceSlug:  "demo",
				AgentName:      "Product Agent",
				AgentSlug:      "product-agent",
				Visibility:     "workspace",
				ProjectID:      "project-1",
				ProjectName:    "Demo Project",
				ProjectAgentID: "pa-product",
			},
			{
				AgentID:        "agent-backend",
				WorkspaceID:    "workspace-1",
				WorkspaceName:  "Demo Workspace",
				WorkspaceSlug:  "demo",
				AgentName:      "Backend Agent",
				AgentSlug:      "backend-agent",
				Visibility:     "workspace",
				ProjectID:      "project-1",
				ProjectName:    "Demo Project",
				ProjectAgentID: "pa-backend",
			},
		},
		routes: map[string]store.FeishuAgentRoute{
			"agent-host": {
				AgentID:       "agent-host",
				WorkspaceID:   "workspace-1",
				WorkspaceName: "Demo Workspace",
				AgentName:     "Shared Feishu Bot",
				AgentSlug:     "shared-feishu-bot",
				Visibility:    "workspace",
				Config:        []byte(`{}`),
			},
			"agent-product": {
				AgentID:       "agent-product",
				WorkspaceID:   "workspace-1",
				WorkspaceName: "Demo Workspace",
				AgentName:     "Product Agent",
				AgentSlug:     "product-agent",
				Visibility:    "workspace",
				Config:        []byte(`{}`),
			},
			"agent-backend": {
				AgentID:       "agent-backend",
				WorkspaceID:   "workspace-1",
				WorkspaceName: "Demo Workspace",
				AgentName:     "Backend Agent",
				AgentSlug:     "backend-agent",
				Visibility:    "workspace",
				Config:        []byte(`{}`),
			},
		},
		userByUnion: map[string]string{
			"ou_user": "user-1",
		},
		memberships: map[string]bool{
			"workspace-1:user-1": true,
		},
		selections: map[string]string{},
	}
}

func (f *fakeSharedStore) CreateInboundIMMessage(ctx context.Context, input store.CreateInboundIMMessageInput) (store.CreateInboundIMMessageResult, error) {
	if f.createErr != nil {
		return store.CreateInboundIMMessageResult{}, f.createErr
	}
	f.created = append(f.created, input)
	return store.CreateInboundIMMessageResult{MessageID: "message-1", RunIDs: []string{"run-1"}}, nil
}

func (f *fakeSharedStore) ListFeishuSharedBotAgents(ctx context.Context, senderUserID string, excludeAgentID string, limit int32) ([]store.FeishuSharedBotAgent, error) {
	f.lastListUser = senderUserID
	f.lastExclude = excludeAgentID
	out := make([]store.FeishuSharedBotAgent, 0, len(f.agents))
	for _, agent := range f.agents {
		if agent.AgentID == excludeAgentID {
			continue
		}
		out = append(out, agent)
	}
	return out, nil
}

func (f *fakeSharedStore) UpsertGatewaySessionSelection(ctx context.Context, input store.GatewaySessionSelectionInput) error {
	f.selections[selectionKey(input.Platform, input.ExternalID, input.ExternalThreadID)] = input.AgentID
	return nil
}

func (f *fakeSharedStore) GetGatewaySessionSelection(ctx context.Context, platform, externalID, externalThreadID string) (string, error) {
	agentID := f.selections[selectionKey(platform, externalID, externalThreadID)]
	if strings.TrimSpace(agentID) == "" {
		return "", store.ErrUnknownGatewaySessionSelection
	}
	return agentID, nil
}

func (f *fakeSharedStore) ClearGatewaySessionSelection(ctx context.Context, platform, externalID, externalThreadID string) error {
	delete(f.selections, selectionKey(platform, externalID, externalThreadID))
	return nil
}

func (f *fakeSharedStore) GetAgentByID(ctx context.Context, agentID string) (store.FeishuAgentRoute, error) {
	route, ok := f.routes[agentID]
	if !ok {
		return store.FeishuAgentRoute{}, store.ErrUnknownFeishuAgent
	}
	return route, nil
}

func (f *fakeSharedStore) GetAgentByFeishuAppID(ctx context.Context, appID string) (store.FeishuAgentRoute, error) {
	return f.routes["agent-host"], nil
}

func (f *fakeSharedStore) FindUserIDByFeishuUnionID(ctx context.Context, unionID string) (string, error) {
	userID := f.userByUnion[unionID]
	if strings.TrimSpace(userID) == "" {
		return "", store.ErrUnknownFeishuUser
	}
	return userID, nil
}

func (f *fakeSharedStore) IsActiveWorkspaceMember(ctx context.Context, workspaceID, userID string) (bool, error) {
	return f.memberships[workspaceID+":"+userID], nil
}

func (f *fakeSharedStore) GetWorkspaceVisibility(ctx context.Context, workspaceID string) (string, error) {
	return "private", nil
}

func (f *fakeSharedStore) ListActiveWorkspaceOwnerNames(ctx context.Context, workspaceID string, limit int32) ([]string, error) {
	return nil, nil
}

func (f *fakeSharedStore) FindConversationByExternalRef(ctx context.Context, _, externalChatID, externalThreadID string) (string, error) {
	if f.conversationByExternalRef == nil {
		return "", store.ErrUnknownConversation
	}
	key := externalChatID + "|" + externalThreadID
	if convID, ok := f.conversationByExternalRef[key]; ok {
		return convID, nil
	}
	return "", store.ErrUnknownConversation
}

func (f *fakeSharedStore) CancelAllInflightForConversation(ctx context.Context, conversationID, reason string) ([]store.SupersededRun, error) {
	f.cancelCalls = append(f.cancelCalls, struct{ ConversationID, Reason string }{ConversationID: conversationID, Reason: reason})
	if f.cancelledRunsByConversation == nil {
		return nil, nil
	}
	return f.cancelledRunsByConversation[conversationID], nil
}

func TestHandleInboundListRepliesWithSelectableAgents(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	var replies []string
	outcome, err := HandleInbound(ctx, st, hostAgent(), sharedEvent("/list"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		replies = append(replies, text)
		return nil
	}, nil, gateway.GateConfig{})
	if err != nil {
		t.Fatalf("HandleInbound /list: %v", err)
	}
	if !outcome.Handled || outcome.Accepted || !outcome.Replied || outcome.Reason != "list" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if st.lastListUser != "user-1" || st.lastExclude != "agent-host" {
		t.Fatalf("list did not use sender/host exclusion: user=%q exclude=%q", st.lastListUser, st.lastExclude)
	}
	if len(replies) != 1 {
		t.Fatalf("expected one /list reply, got %#v", replies)
	}
	reply := replies[0]
	if !strings.Contains(reply, "---- Demo Workspace ----") {
		t.Fatalf("/list reply missing workspace header: %q", reply)
	}
	if !strings.Contains(reply, "product-agent（Product Agent — Demo Project）") {
		t.Fatalf("/list reply missing product-agent row: %q", reply)
	}
	if !strings.Contains(reply, "backend-agent（Backend Agent — Demo Project）") {
		t.Fatalf("/list reply missing backend-agent row: %q", reply)
	}
	if !strings.Contains(reply, "/select <agent-slug>") {
		t.Fatalf("/list reply missing select hint: %q", reply)
	}
	if len(st.created) != 0 {
		t.Fatalf("/list must not enqueue inbound messages, got %+v", st.created)
	}
}

func TestHandleInboundSelectStoresSession(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	var reply string
	outcome, err := HandleInbound(ctx, st, hostAgent(), sharedEvent("/select backend-agent"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		reply = text
		return nil
	}, nil, gateway.GateConfig{})
	if err != nil {
		t.Fatalf("HandleInbound /select: %v", err)
	}
	if !outcome.Handled || outcome.Accepted || !outcome.Replied || outcome.Reason != "selected" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	got := st.selections[selectionKey("feishu", "oc_chat", "")]
	if got != "agent-backend" {
		t.Fatalf("selected agent = %q, want agent-backend", got)
	}
	if !strings.Contains(reply, "已选择 Agent") || !strings.Contains(reply, "Backend Agent") {
		t.Fatalf("unexpected select reply: %q", reply)
	}
}

func TestHandleInboundSelectionIsChatScopedAcrossSenders(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	var reply string
	if _, err := HandleInbound(ctx, st, hostAgent(), sharedEvent("/select backend-agent"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		reply = text
		return nil
	}, nil, gateway.GateConfig{}); err != nil {
		t.Fatalf("HandleInbound /select: %v", err)
	}
	if !strings.Contains(reply, "Backend Agent") {
		t.Fatalf("unexpected select reply: %q", reply)
	}

	st.userByUnion["ou_peer"] = "user-1"
	event := sharedEvent("帮我看一下")
	event.SenderUnionID = "ou_peer"
	event.SenderOpenID = "ou_peer_open"
	outcome, err := HandleInbound(ctx, st, hostAgent(), event, func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		t.Fatalf("did not expect command reply for selected peer message: %q", text)
		return nil
	}, nil, gateway.GateConfig{})
	if err != nil {
		t.Fatalf("HandleInbound peer selected message: %v", err)
	}
	if !outcome.Accepted || outcome.AgentID != "agent-backend" {
		t.Fatalf("unexpected peer outcome: %+v", outcome)
	}
}

func TestHandleInboundWithoutSelectionAsksUserToSelect(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	var reply string
	outcome, err := HandleInbound(ctx, st, hostAgent(), sharedEvent("帮我看一下"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		reply = text
		return nil
	}, nil, gateway.GateConfig{})
	if err != nil {
		t.Fatalf("HandleInbound without selection: %v", err)
	}
	if !outcome.Handled || outcome.Accepted || !outcome.Replied || outcome.Reason != "selection_required" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if !strings.Contains(reply, "/list") || !strings.Contains(reply, "/select") {
		t.Fatalf("unexpected selection-required reply: %q", reply)
	}
	if len(st.created) != 0 {
		t.Fatalf("message without selection must not enqueue, got %+v", st.created)
	}
}

func TestHandleInboundSelectedAgentCreatesTargetedMessageWithHostAppID(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	st.selections[selectionKey("feishu", "oc_chat", "")] = "agent-backend"
	var replies []string
	outcome, err := HandleInbound(ctx, st, hostAgent(), sharedEvent("帮我评估数据库迁移"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		replies = append(replies, text)
		return nil
	}, nil, gateway.GateConfig{})
	if err != nil {
		t.Fatalf("HandleInbound selected message: %v", err)
	}
	if !outcome.Handled || !outcome.Accepted || outcome.Replied || outcome.Reason != "accepted" || outcome.AgentID != "agent-backend" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if len(replies) != 0 {
		t.Fatalf("accepted prompt should not send command reply, got %#v", replies)
	}
	if len(st.created) != 1 {
		t.Fatalf("expected one created inbound message, got %d", len(st.created))
	}
	created := st.created[0]
	if created.TargetAgentID != "agent-backend" || created.SourceAppID != "cli_shared" {
		t.Fatalf("target/source app mismatch: target=%q source_app=%q", created.TargetAgentID, created.SourceAppID)
	}
	if created.ExternalUserID != "ou_user" || created.ExternalChatID != "oc_chat" || created.ExternalMessageID != "om_message" {
		t.Fatalf("external refs mismatch: %+v", created)
	}
	if created.Text != "帮我评估数据库迁移" || created.Gateway != "feishu" || created.ConversationForm != "group" {
		t.Fatalf("unexpected message fields: %+v", created)
	}
	if len(created.Mentions) != 1 || created.Mentions[0] != "@Backend Agent" {
		t.Fatalf("unexpected mentions: %#v", created.Mentions)
	}
	if created.Metadata["shared_bot"] != true || created.Metadata["host_app_id"] != "cli_shared" || created.Metadata["selected_agent"] != "agent-backend" {
		t.Fatalf("shared metadata missing: %#v", created.Metadata)
	}
}

func TestHandleInboundStampsQuotedChainPrefixIntoMetadata(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	st.selections[selectionKey("feishu", "oc_chat", "")] = "agent-backend"
	chainPrefix := "[Quoted message]\n上面这条讲的事\n[/Quoted message]\n"
	outcome, err := HandleInbound(ctx, st, hostAgent(), sharedEvent("怎么处理?"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		return nil
	}, func(ctx context.Context, host gateway.FeishuRouteAgent, event *gateway.FeishuInboundEvent) string {
		return chainPrefix
	}, gateway.GateConfig{})
	if err != nil {
		t.Fatalf("HandleInbound: %v", err)
	}
	if !outcome.Accepted {
		t.Fatalf("expected accepted, got %+v", outcome)
	}
	if len(st.created) != 1 {
		t.Fatalf("expected one created inbound, got %d", len(st.created))
	}
	created := st.created[0]
	// Text must remain the user's verbatim input — the prefix lives in
	// metadata so audit/list/preview/raw_query stay clean.
	if created.Text != "怎么处理?" {
		t.Errorf("Text = %q, must stay verbatim", created.Text)
	}
	if got, _ := created.Metadata[QuotedChainPrefixMetadataKey].(string); got != chainPrefix {
		t.Errorf("Metadata[%q] = %q, want %q", QuotedChainPrefixMetadataKey, got, chainPrefix)
	}
}

func TestHandleInboundSkipsQuotedChainPrefixForBotSender(t *testing.T) {
	// Bot-as-sender messages must not be enriched — the bot isn't
	// quoting; its 'parent' is whatever pipeline produced the call. We
	// don't care whether the message ends up accepted (the agent may not
	// have a creator wired); only that the quoted-chain callback never
	// fires on the bot-sender branch.
	ctx := context.Background()
	st := newFakeSharedStore()
	st.selections[selectionKey("feishu", "oc_chat", "")] = "agent-backend"
	called := 0
	if _, err := HandleInbound(ctx, st, hostAgent(), botSenderEvent("hi"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		return nil
	}, func(ctx context.Context, host gateway.FeishuRouteAgent, event *gateway.FeishuInboundEvent) string {
		called++
		return "shouldnt see me"
	}, gateway.GateConfig{}); err != nil {
		t.Fatalf("HandleInbound: %v", err)
	}
	if called != 0 {
		t.Errorf("quoted-chain callback must not run for bot senders, ran %d times", called)
	}
}

// Production scenario: a chat had previously /select'd an Agent, that
// Agent's project_agents binding was later disabled/deleted by admin,
// and the next inbound surfaces store.ErrUnknownMention. HandleInbound
// must wipe the stale selection and tell the user to /list + /select
// again, not silently bail. Without this branch the bot looks dead.
func TestHandleInboundClearsStaleSelectionOnBindingLoss(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	st.selections[selectionKey("feishu", "oc_chat", "")] = "agent-backend"
	st.createErr = store.ErrUnknownMention
	var replies []string
	outcome, err := HandleInbound(ctx, st, hostAgent(), sharedEvent("帮我评估数据库迁移"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		replies = append(replies, text)
		return nil
	}, nil, gateway.GateConfig{})
	if err != nil {
		t.Fatalf("HandleInbound on binding loss: %v", err)
	}
	if !outcome.Handled || outcome.Accepted || !outcome.Replied || outcome.Reason != "selected_agent_binding_lost" {
		t.Fatalf("unexpected outcome on binding loss: %+v", outcome)
	}
	if len(replies) != 1 || !strings.Contains(replies[0], "已不可用") || !strings.Contains(replies[0], "/list") {
		t.Fatalf("expected re-/select prompt reply, got %#v", replies)
	}
	// Selection cleared so the next /select starts from a clean slate.
	if _, ok := st.selections[selectionKey("feishu", "oc_chat", "")]; ok {
		t.Fatalf("expected stale selection to be cleared, still present: %#v", st.selections)
	}
	if len(st.created) != 0 {
		t.Fatalf("expected no inbound message stored on binding loss, got %d", len(st.created))
	}
}

func TestHandleInboundSelectedAgentRechecksVisibility(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	st.selections[selectionKey("feishu", "oc_chat", "")] = "agent-backend"
	st.memberships["workspace-1:user-1"] = false
	var reply string
	outcome, err := HandleInbound(ctx, st, hostAgent(), sharedEvent("帮我看一下"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		reply = text
		return nil
	}, nil, gateway.GateConfig{})
	if err != nil {
		t.Fatalf("HandleInbound selected visibility: %v", err)
	}
	if !outcome.Handled || outcome.Accepted || !outcome.Replied || outcome.AgentID != "agent-backend" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if strings.TrimSpace(reply) == "" {
		t.Fatal("expected visibility rejection reply")
	}
	if len(st.created) != 0 {
		t.Fatalf("visibility rejection must not enqueue, got %+v", st.created)
	}
}

func TestHandleInboundPropagatesReplyError(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	wantErr := errors.New("reply failed")
	_, err := HandleInbound(ctx, st, hostAgent(), sharedEvent("/list"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		return wantErr
	}, nil, gateway.GateConfig{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

// TestHandleInboundCancelBulkCancelsInflight pins the happy path:
// /cancel in a Feishu thread resolves the conversation via
// (chat_id, thread_key), bulk-cancels the inflight runs, and replies
// with the count. Prevents a regression where /cancel falls through
// to the prompt-dispatch path and the user's queued message piles up
// behind the hung run.
func TestHandleInboundCancelBulkCancelsInflight(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	st.selections[selectionKey("feishu", "oc_chat", "")] = "agent-backend"
	st.conversationByExternalRef = map[string]string{
		"oc_chat|om_message": "conv-1",
	}
	st.cancelledRunsByConversation = map[string][]store.SupersededRun{
		"conv-1": {{ID: "run-1", ConnectorType: "agent_daemon"}, {ID: "run-2", ConnectorType: "agent_daemon"}},
	}
	var reply string
	outcome, err := HandleInbound(ctx, st, hostAgent(), sharedEvent("/cancel"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		reply = text
		return nil
	}, nil, gateway.GateConfig{})
	if err != nil {
		t.Fatalf("HandleInbound /cancel: %v", err)
	}
	if !outcome.Handled || outcome.Accepted || !outcome.Replied || outcome.Reason != "cancelled" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if !strings.Contains(reply, "已取消 2 个任务") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(st.cancelCalls) != 1 || st.cancelCalls[0].ConversationID != "conv-1" || st.cancelCalls[0].Reason != "feishu_user_cancel" {
		t.Fatalf("unexpected cancel calls: %+v", st.cancelCalls)
	}
	if len(st.created) != 0 {
		t.Fatalf("/cancel must not store an inbound message; got %d", len(st.created))
	}
}

// TestHandleInboundCancelAllUsesAllReason confirms `/cancel all` routes
// to the same bulk handler but stamps the cancel reason differently so
// audit logs can tell the two apart.
func TestHandleInboundCancelAllUsesAllReason(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	st.selections[selectionKey("feishu", "oc_chat", "")] = "agent-backend"
	st.conversationByExternalRef = map[string]string{
		"oc_chat|om_message": "conv-1",
	}
	st.cancelledRunsByConversation = map[string][]store.SupersededRun{
		"conv-1": {{ID: "run-1", ConnectorType: "agent_daemon"}},
	}
	if _, err := HandleInbound(ctx, st, hostAgent(), sharedEvent("/cancel all"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		return nil
	}, nil, gateway.GateConfig{}); err != nil {
		t.Fatalf("HandleInbound /cancel all: %v", err)
	}
	if len(st.cancelCalls) != 1 || st.cancelCalls[0].Reason != "feishu_user_cancel_all" {
		t.Fatalf("unexpected cancel calls: %+v", st.cancelCalls)
	}
}

// TestHandleInboundCancelWithoutConversation pins the "no conversation
// yet" UX — typing /cancel before ever talking to a shared bot replies
// with a friendly note instead of erroring.
func TestHandleInboundCancelWithoutConversation(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	st.selections[selectionKey("feishu", "oc_chat", "")] = "agent-backend"
	// no conversationByExternalRef entry → ErrUnknownConversation
	var reply string
	outcome, err := HandleInbound(ctx, st, hostAgent(), sharedEvent("/cancel"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		reply = text
		return nil
	}, nil, gateway.GateConfig{})
	if err != nil {
		t.Fatalf("HandleInbound /cancel (no conv): %v", err)
	}
	if outcome.Reason != "cancel_no_conversation" {
		t.Fatalf("unexpected outcome.Reason: %q", outcome.Reason)
	}
	if !strings.Contains(reply, "无法取消") {
		t.Fatalf("unexpected reply: %q", reply)
	}
	if len(st.cancelCalls) != 0 {
		t.Fatalf("CancelAllInflightForConversation must not be called when conv lookup fails; got %+v", st.cancelCalls)
	}
}

// TestHandleInboundCancelWithoutInflight pins the "found the conv but
// nothing was running" branch.
func TestHandleInboundCancelWithoutInflight(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	st.selections[selectionKey("feishu", "oc_chat", "")] = "agent-backend"
	st.conversationByExternalRef = map[string]string{
		"oc_chat|om_message": "conv-1",
	}
	// cancelledRunsByConversation is empty → returns 0 cancelled rows.
	var reply string
	outcome, err := HandleInbound(ctx, st, hostAgent(), sharedEvent("/cancel"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		reply = text
		return nil
	}, nil, gateway.GateConfig{})
	if err != nil {
		t.Fatalf("HandleInbound /cancel (no inflight): %v", err)
	}
	if outcome.Reason != "cancel_none" {
		t.Fatalf("unexpected outcome.Reason: %q", outcome.Reason)
	}
	if !strings.Contains(reply, "当前没有进行中的任务") {
		t.Fatalf("unexpected reply: %q", reply)
	}
}

// TestHandleInboundCancelSkipsSelectionGate locks the design choice
// that /cancel does NOT require a prior /select — a user whose
// previous run is hung shouldn't be told to /select before they can
// rescue themselves.
func TestHandleInboundCancelSkipsSelectionGate(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	// deliberately no st.selections entry
	st.conversationByExternalRef = map[string]string{
		"oc_chat|om_message": "conv-1",
	}
	st.cancelledRunsByConversation = map[string][]store.SupersededRun{
		"conv-1": {{ID: "run-1", ConnectorType: "agent_daemon"}},
	}
	outcome, err := HandleInbound(ctx, st, hostAgent(), sharedEvent("/cancel"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		return nil
	}, nil, gateway.GateConfig{})
	if err != nil {
		t.Fatalf("HandleInbound /cancel without selection: %v", err)
	}
	if outcome.Reason != "cancelled" {
		t.Fatalf("/cancel without prior /select must short-circuit ahead of the selection gate; got Reason=%q", outcome.Reason)
	}
}

func hostAgent() gateway.FeishuRouteAgent {
	return gateway.FeishuRouteAgent{
		AgentID:       "agent-host",
		WorkspaceID:   "workspace-1",
		WorkspaceName: "Demo Workspace",
		AgentName:     "Shared Feishu Bot",
		AgentSlug:     "shared-feishu-bot",
		Visibility:    gateway.VisibilityWorkspace,
		Config:        []byte(`{}`),
	}
}

func sharedEvent(text string) gateway.FeishuInboundEvent {
	return gateway.FeishuInboundEvent{
		AppID:          "cli_shared",
		MessageID:      "om_message",
		ChatID:         "oc_chat",
		ChatType:       "group",
		MessageType:    "text",
		RawContent:     `{"text":"` + text + `"}`,
		Text:           text,
		SenderUnionID:  "ou_user",
		SenderOpenID:   "ou_open",
		SenderUserID:   "user_feishu",
		TenantKey:      "tenant-1",
		MentionOpenIDs: []string{"ou_bot"},
	}
}

func selectionKey(platform, externalID, externalThreadID string) string {
	return platform + "\x00" + externalID + "\x00" + externalThreadID
}

// TestParseCancelCommand locks the surface the inbound manager
// consumes — recognised verbs (cancel / stop), recognised scope
// ("all"), and fall-through for unrelated slash commands so they
// can still reach the agent as prompts.
func TestParseCancelCommand(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantOK  bool
		wantCmd CancelCommand
	}{
		{"cancel current", "/cancel", true, CancelCommand{Scope: "current"}},
		{"cancel current with trailing space", "/cancel  ", true, CancelCommand{Scope: "current"}},
		{"cancel all", "/cancel all", true, CancelCommand{Scope: "all"}},
		{"cancel ALL caps", "/cancel ALL", true, CancelCommand{Scope: "all"}},
		{"stop alias", "/stop", true, CancelCommand{Scope: "current"}},
		{"stop all alias", "/stop all", true, CancelCommand{Scope: "all"}},
		{"unrelated command", "/list", false, CancelCommand{}},
		{"plain text", "hello world", false, CancelCommand{}},
		{"empty", "", false, CancelCommand{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseCancelCommand(tc.input)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.wantCmd {
				t.Fatalf("cmd = %+v, want %+v", got, tc.wantCmd)
			}
		})
	}
}

// botSenderEvent mirrors sharedEvent but flips sender_type to "app" — the
// shape Feishu delivers when one bot's interactive card mentions another.
func botSenderEvent(text string) gateway.FeishuInboundEvent {
	event := sharedEvent(text)
	event.SenderType = "app"
	event.SenderUnionID = ""
	event.SenderUserID = ""
	event.SenderOpenID = "ou_other_bot"
	event.MentionOpenIDs = nil
	return event
}

func TestHandleInboundBotSenderUsesAgentCreatorAndSkipsCommands(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	st.selections[selectionKey("feishu", "oc_chat", "")] = "agent-backend"
	target := st.routes["agent-backend"]
	target.CreatedByUserID = "user-creator"
	st.routes["agent-backend"] = target

	// "/list" from a bot must be treated as content, not as a slash command.
	outcome, err := HandleInbound(ctx, st, hostAgent(), botSenderEvent("/list please answer me"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		t.Fatalf("bot sender must not trigger any reply, got %q", text)
		return nil
	}, nil, gateway.GateConfig{})
	if err != nil {
		t.Fatalf("HandleInbound bot sender: %v", err)
	}
	if !outcome.Handled || !outcome.Accepted || outcome.Replied || outcome.Reason != "accepted" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if len(st.created) != 1 {
		t.Fatalf("expected one created inbound message, got %d", len(st.created))
	}
	created := st.created[0]
	if created.InitiatorUserID != "user-creator" {
		t.Fatalf("expected initiator=user-creator, got %q", created.InitiatorUserID)
	}
	if created.Text != "/list please answer me" {
		t.Fatalf("expected raw text passthrough, got %q", created.Text)
	}
	if created.Metadata["bot_sender"] != true || created.Metadata["sender_type"] != "app" {
		t.Fatalf("expected bot_sender metadata, got %#v", created.Metadata)
	}
}

func TestHandleInboundBotSenderWithoutSelectionStaysSilent(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	outcome, err := HandleInbound(ctx, st, hostAgent(), botSenderEvent("hi"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		t.Fatalf("bot sender without selection must stay silent, got reply %q", text)
		return nil
	}, nil, gateway.GateConfig{})
	if err != nil {
		t.Fatalf("HandleInbound bot sender without selection: %v", err)
	}
	if !outcome.Handled || outcome.Accepted || outcome.Replied || outcome.Reason != "bot_sender_no_selection" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if len(st.created) != 0 {
		t.Fatalf("no message should be enqueued, got %d", len(st.created))
	}
}

func TestHandleInboundBotSenderWithoutAgentCreatorStaysSilent(t *testing.T) {
	ctx := context.Background()
	st := newFakeSharedStore()
	st.selections[selectionKey("feishu", "oc_chat", "")] = "agent-backend"
	outcome, err := HandleInbound(ctx, st, hostAgent(), botSenderEvent("hi"), func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error {
		t.Fatalf("bot sender without agent creator must stay silent, got reply %q", text)
		return nil
	}, nil, gateway.GateConfig{})
	if err != nil {
		t.Fatalf("HandleInbound bot sender no creator: %v", err)
	}
	if !outcome.Handled || outcome.Accepted || outcome.Replied || outcome.Reason != "bot_sender_no_agent_creator" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if len(st.created) != 0 {
		t.Fatalf("no message should be enqueued, got %d", len(st.created))
	}
}
