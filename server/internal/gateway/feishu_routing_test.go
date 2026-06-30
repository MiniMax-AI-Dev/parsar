package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fakeFeishuRouter implements FeishuRouter for unit tests without
// touching the database. Each field controls one branch.
type fakeFeishuRouter struct {
	agent         FeishuRouteAgent
	agentErr      error
	userID        string
	userErr       error
	isMember      bool
	memberErr     error
	gotMemberWS   string
	gotMemberUser string
	gotSubject    string
	gotAppID      string

	// Visibility-rejection auxiliary reads. Both default to "not
	// configured" so existing tests don't have to opt in.
	wsVisibility       string
	wsVisibilityErr    error
	gotWsVisibilityWS  string
	ownerNames         []string
	ownerNamesErr      error
	gotOwnerNamesWS    string
	gotOwnerNamesLimit int32
}

func (f *fakeFeishuRouter) GetAgentByFeishuAppID(ctx context.Context, appID string) (FeishuRouteAgent, error) {
	f.gotAppID = appID
	if f.agentErr != nil {
		return FeishuRouteAgent{}, f.agentErr
	}
	return f.agent, nil
}

func (f *fakeFeishuRouter) GetAgentByID(ctx context.Context, agentID string) (FeishuRouteAgent, error) {
	if f.agentErr != nil {
		return FeishuRouteAgent{}, f.agentErr
	}
	return f.agent, nil
}

func (f *fakeFeishuRouter) FindUserIDByPlatformSubject(ctx context.Context, platform, subject string) (string, error) {
	f.gotSubject = subject
	if f.userErr != nil {
		return "", f.userErr
	}
	return f.userID, nil
}

func (f *fakeFeishuRouter) IsActiveWorkspaceMember(ctx context.Context, workspaceID, userID string) (bool, error) {
	f.gotMemberWS = workspaceID
	f.gotMemberUser = userID
	return f.isMember, f.memberErr
}

func (f *fakeFeishuRouter) GetWorkspaceVisibility(ctx context.Context, workspaceID string) (string, error) {
	f.gotWsVisibilityWS = workspaceID
	if f.wsVisibilityErr != nil {
		return "", f.wsVisibilityErr
	}
	return f.wsVisibility, nil
}

func (f *fakeFeishuRouter) ListWorkspaceOwnerNames(ctx context.Context, workspaceID string, limit int32) ([]string, error) {
	f.gotOwnerNamesWS = workspaceID
	f.gotOwnerNamesLimit = limit
	if f.ownerNamesErr != nil {
		return nil, f.ownerNamesErr
	}
	return f.ownerNames, nil
}

func TestRouteFeishuInbound_RejectsEmptyAppID(t *testing.T) {
	t.Parallel()
	r := &fakeFeishuRouter{}
	_, err := RouteFeishuInbound(context.Background(), r, FeishuInboundEvent{AppID: "  "}, GateConfig{})
	if !errors.Is(err, ErrFeishuRouterUnknownAgent) {
		t.Fatalf("expected ErrFeishuRouterUnknownAgent, got %v", err)
	}
}

func TestRouteFeishuInbound_PropagatesAgentLookupError(t *testing.T) {
	t.Parallel()
	r := &fakeFeishuRouter{agentErr: ErrFeishuRouterUnknownAgent}
	_, err := RouteFeishuInbound(context.Background(), r, FeishuInboundEvent{AppID: "cli_x"}, GateConfig{})
	if !errors.Is(err, ErrFeishuRouterUnknownAgent) {
		t.Fatalf("agent lookup error must propagate; got %v", err)
	}
}

func TestRouteFeishuInbound_WorkspaceMemberAllowed(t *testing.T) {
	t.Parallel()
	r := &fakeFeishuRouter{
		agent: FeishuRouteAgent{
			AgentID: "agent-1", WorkspaceID: "ws-1", WorkspaceName: "Eng",
			Visibility: VisibilityWorkspace,
		},
		userID:   "user-1",
		isMember: true,
	}
	decision, err := RouteFeishuInbound(context.Background(), r, FeishuInboundEvent{
		AppID:         "cli_x",
		SenderUnionID: "on_union_alice",
	}, GateConfig{})
	if err != nil {
		t.Fatalf("RouteFeishuInbound: %v", err)
	}
	if !decision.Decision.Allowed {
		t.Fatalf("expected allowed, got %+v", decision.Decision)
	}
	if decision.SenderUserID != "user-1" {
		t.Errorf("SenderUserID = %q, want user-1", decision.SenderUserID)
	}
	if !decision.SenderState.WorkspaceMember {
		t.Error("SenderState.WorkspaceMember must be true")
	}
	if r.gotMemberWS != "ws-1" || r.gotMemberUser != "user-1" {
		t.Errorf("workspace member check args = (%q,%q), want (ws-1,user-1)", r.gotMemberWS, r.gotMemberUser)
	}
}

func TestRouteFeishuInbound_UnregisteredAgainstWorkspaceRejected(t *testing.T) {
	t.Parallel()
	r := &fakeFeishuRouter{
		agent: FeishuRouteAgent{
			AgentID: "agent-1", WorkspaceID: "ws-1", WorkspaceName: "Eng",
			Visibility: VisibilityWorkspace,
		},
		userErr: ErrRouterUnknownUser,
	}
	decision, err := RouteFeishuInbound(context.Background(), r, FeishuInboundEvent{
		AppID:         "cli_x",
		SenderUnionID: "on_union_guest",
	}, GateConfig{RegisterURL: "https://parsar.example/register"})
	if err != nil {
		t.Fatalf("RouteFeishuInbound: %v", err)
	}
	if decision.Decision.Allowed {
		t.Fatal("workspace visibility + unregistered must be rejected")
	}
	if decision.SenderState.Registered {
		t.Error("SenderState.Registered must be false")
	}
	if decision.SenderUserID != "" {
		t.Errorf("SenderUserID = %q, want empty for unregistered", decision.SenderUserID)
	}
	if !strings.Contains(decision.Decision.ReplyHint, "Eng") {
		t.Errorf("ReplyHint should mention workspace name; got %q", decision.Decision.ReplyHint)
	}
}

func TestRouteFeishuInbound_PublicGuestPasses(t *testing.T) {
	t.Parallel()
	r := &fakeFeishuRouter{
		agent: FeishuRouteAgent{
			AgentID: "agent-1", WorkspaceID: "ws-1",
			Visibility: VisibilityPublic,
		},
		userErr: ErrRouterUnknownUser,
	}
	decision, err := RouteFeishuInbound(context.Background(), r, FeishuInboundEvent{
		AppID:         "cli_x",
		SenderUnionID: "on_union_guest",
	}, GateConfig{RegisterURL: "https://parsar.example/register"})
	if err != nil {
		t.Fatalf("RouteFeishuInbound: %v", err)
	}
	if !decision.Decision.Allowed {
		t.Fatal("public visibility + guest must be allowed")
	}
	if decision.Decision.Reason != ReasonAllowedPublicGuest {
		t.Errorf("Reason = %q, want %q", decision.Decision.Reason, ReasonAllowedPublicGuest)
	}
	if !strings.Contains(decision.Decision.GuestReplyHint, "register") {
		t.Errorf("GuestReplyHint must carry registration nudge; got %q", decision.Decision.GuestReplyHint)
	}
}

func TestRouteFeishuInbound_FallsBackToOpenIDWhenUnionMissing(t *testing.T) {
	t.Parallel()
	r := &fakeFeishuRouter{
		agent: FeishuRouteAgent{
			AgentID: "agent-1", WorkspaceID: "ws-1", Visibility: VisibilityPublic,
		},
		userID: "user-1",
	}
	if _, err := RouteFeishuInbound(context.Background(), r, FeishuInboundEvent{
		AppID:        "cli_x",
		SenderOpenID: "ou_only_open_id",
	}, GateConfig{}); err != nil {
		t.Fatalf("RouteFeishuInbound: %v", err)
	}
	if r.gotSubject != "ou_only_open_id" {
		t.Errorf("router should fall back to open_id when union_id absent; got %q", r.gotSubject)
	}
}

func TestRouteFeishuInbound_PropagatesMembershipError(t *testing.T) {
	t.Parallel()
	r := &fakeFeishuRouter{
		agent: FeishuRouteAgent{
			AgentID: "agent-1", WorkspaceID: "ws-1", Visibility: VisibilityWorkspace,
		},
		userID:    "user-1",
		memberErr: errors.New("db down"),
	}
	if _, err := RouteFeishuInbound(context.Background(), r, FeishuInboundEvent{
		AppID:         "cli_x",
		SenderUnionID: "on_x",
	}, GateConfig{}); err == nil {
		t.Fatal("membership lookup error must propagate")
	}
}

// TestRouteFeishuInbound_WorkspaceRejectionFetchesOwnersAndJoinURL pins the
// rejection-only fast path: when visibility=workspace + non-member, the
// router consults workspace visibility + owner names to enrich the
// rejection card. JoinURLBuilder is wired only for public workspaces.
func TestRouteFeishuInbound_WorkspaceRejectionFetchesOwnersAndJoinURL(t *testing.T) {
	t.Parallel()

	builderCalls := 0
	r := &fakeFeishuRouter{
		agent: FeishuRouteAgent{
			AgentID: "agent-1", WorkspaceID: "ws-1", WorkspaceName: "Eng",
			Visibility: VisibilityWorkspace,
		},
		userID:       "user-1",
		isMember:     false,
		wsVisibility: "public",
		ownerNames:   []string{"张三", "李四"},
	}
	decision, err := RouteFeishuInbound(context.Background(), r, FeishuInboundEvent{
		AppID:         "cli_x",
		SenderUnionID: "on_nonmember",
	}, GateConfig{
		JoinURLBuilder: func(wsID string) string {
			builderCalls++
			return "https://parsar.example/join-workspace?id=" + wsID + "&from=feishu"
		},
	})
	if err != nil {
		t.Fatalf("RouteFeishuInbound: %v", err)
	}
	if decision.Decision.Allowed {
		t.Fatal("expected rejection")
	}
	if r.gotWsVisibilityWS != "ws-1" {
		t.Errorf("GetWorkspaceVisibility called with %q, want ws-1", r.gotWsVisibilityWS)
	}
	if r.gotOwnerNamesWS != "ws-1" {
		t.Errorf("ListWorkspaceOwnerNames called with %q, want ws-1", r.gotOwnerNamesWS)
	}
	if r.gotOwnerNamesLimit <= 0 {
		t.Errorf("ListWorkspaceOwnerNames called with non-positive limit %d", r.gotOwnerNamesLimit)
	}
	if builderCalls != 1 {
		t.Errorf("JoinURLBuilder calls = %d, want 1", builderCalls)
	}
	for _, want := range []string{"Eng", "管理员: 张三、李四", "[申请加入 workspace](https://parsar.example/join-workspace?id=ws-1&from=feishu)"} {
		if !strings.Contains(decision.Decision.ReplyHint, want) {
			t.Errorf("ReplyHint missing %q\nfull:\n%s", want, decision.Decision.ReplyHint)
		}
	}
}

// TestRouteFeishuInbound_WorkspaceRejectionPrivateWorkspaceOmitsJoinURL
// covers the "workspace.visibility=private" branch: API forbids self-
// service join (createJoinRequest 404s by design), so the rejection card
// must NOT surface a link — fall back to "请联系上述管理员加入".
func TestRouteFeishuInbound_WorkspaceRejectionPrivateWorkspaceOmitsJoinURL(t *testing.T) {
	t.Parallel()
	r := &fakeFeishuRouter{
		agent: FeishuRouteAgent{
			AgentID: "agent-1", WorkspaceID: "ws-1", WorkspaceName: "Eng",
			Visibility: VisibilityWorkspace,
		},
		userID:       "user-1",
		isMember:     false,
		wsVisibility: "private",
		ownerNames:   []string{"张三"},
	}
	builderCalls := 0
	decision, err := RouteFeishuInbound(context.Background(), r, FeishuInboundEvent{
		AppID:         "cli_x",
		SenderUnionID: "on_nonmember",
	}, GateConfig{
		JoinURLBuilder: func(string) string { builderCalls++; return "https://NO" },
	})
	if err != nil {
		t.Fatalf("RouteFeishuInbound: %v", err)
	}
	if decision.Decision.Allowed {
		t.Fatal("expected rejection")
	}
	if builderCalls != 0 {
		t.Errorf("JoinURLBuilder must NOT be called for private workspace; got %d calls", builderCalls)
	}
	if strings.Contains(decision.Decision.ReplyHint, "申请加入") {
		t.Errorf("ReplyHint must not contain join link for private workspace; got %q", decision.Decision.ReplyHint)
	}
	if !strings.Contains(decision.Decision.ReplyHint, "请联系上述管理员加入") {
		t.Errorf("ReplyHint should fall back to contact-admin hint; got %q", decision.Decision.ReplyHint)
	}
}

// TestRouteFeishuInbound_WorkspaceRejectionNoBuilderOmitsJoinURL: even
// for public workspaces, if PublicURL is unconfigured the caller passes
// JoinURLBuilder=nil and we must NOT crash or render a half-built link.
func TestRouteFeishuInbound_WorkspaceRejectionNoBuilderOmitsJoinURL(t *testing.T) {
	t.Parallel()
	r := &fakeFeishuRouter{
		agent: FeishuRouteAgent{
			AgentID: "agent-1", WorkspaceID: "ws-1", WorkspaceName: "Eng",
			Visibility: VisibilityWorkspace,
		},
		userID:       "user-1",
		isMember:     false,
		wsVisibility: "public",
		ownerNames:   []string{"张三"},
	}
	decision, err := RouteFeishuInbound(context.Background(), r, FeishuInboundEvent{
		AppID:         "cli_x",
		SenderUnionID: "on_nonmember",
	}, GateConfig{}) // JoinURLBuilder == nil
	if err != nil {
		t.Fatalf("RouteFeishuInbound: %v", err)
	}
	if decision.Decision.Allowed {
		t.Fatal("expected rejection")
	}
	if r.gotWsVisibilityWS != "" {
		t.Errorf("GetWorkspaceVisibility should be skipped when builder nil; got call with %q", r.gotWsVisibilityWS)
	}
	if strings.Contains(decision.Decision.ReplyHint, "申请加入") {
		t.Errorf("ReplyHint must not contain join link when builder is nil; got %q", decision.Decision.ReplyHint)
	}
}

// TestRouteFeishuInbound_AllowedPathSkipsOwnerLookup verifies the
// auxiliary reads only fire on the rejection branch — we don't want to
// add a per-message DB round-trip to allowed traffic.
func TestRouteFeishuInbound_AllowedPathSkipsOwnerLookup(t *testing.T) {
	t.Parallel()
	r := &fakeFeishuRouter{
		agent: FeishuRouteAgent{
			AgentID: "agent-1", WorkspaceID: "ws-1", WorkspaceName: "Eng",
			Visibility: VisibilityWorkspace,
		},
		userID:   "user-1",
		isMember: true,
	}
	builderCalls := 0
	if _, err := RouteFeishuInbound(context.Background(), r, FeishuInboundEvent{
		AppID:         "cli_x",
		SenderUnionID: "on_member",
	}, GateConfig{
		JoinURLBuilder: func(string) string { builderCalls++; return "" },
	}); err != nil {
		t.Fatalf("RouteFeishuInbound: %v", err)
	}
	if r.gotOwnerNamesWS != "" {
		t.Errorf("ListWorkspaceOwnerNames must not be called when allowed; got call with %q", r.gotOwnerNamesWS)
	}
	if r.gotWsVisibilityWS != "" {
		t.Errorf("GetWorkspaceVisibility must not be called when allowed; got call with %q", r.gotWsVisibilityWS)
	}
	if builderCalls != 0 {
		t.Errorf("JoinURLBuilder must not be called when allowed; got %d calls", builderCalls)
	}
}

// TestRouteFeishuInbound_WorkspaceRejectionSwallowsAuxiliaryErrors makes
// sure a flaky DB read in the auxiliary reads (owner list, visibility)
// never blocks the rejection itself — the user still sees the standard
// "you don't have access" message.
func TestRouteFeishuInbound_WorkspaceRejectionSwallowsAuxiliaryErrors(t *testing.T) {
	t.Parallel()
	r := &fakeFeishuRouter{
		agent: FeishuRouteAgent{
			AgentID: "agent-1", WorkspaceID: "ws-1", WorkspaceName: "Eng",
			Visibility: VisibilityWorkspace,
		},
		userID:          "user-1",
		isMember:        false,
		wsVisibilityErr: errors.New("workspace lookup transient err"),
		ownerNamesErr:   errors.New("owner lookup transient err"),
	}
	decision, err := RouteFeishuInbound(context.Background(), r, FeishuInboundEvent{
		AppID:         "cli_x",
		SenderUnionID: "on_nonmember",
	}, GateConfig{
		JoinURLBuilder: func(string) string { return "https://parsar.example/join-workspace?id=ws-1" },
	})
	if err != nil {
		t.Fatalf("rejection must not be blocked by auxiliary read errors; got %v", err)
	}
	if decision.Decision.Allowed {
		t.Fatal("expected rejection")
	}
	// No owner line, no join link — the fallback "请联系管理员开通" must surface.
	if strings.Contains(decision.Decision.ReplyHint, "管理员: ") {
		t.Errorf("ReplyHint should NOT have owner line when owner lookup failed: %q", decision.Decision.ReplyHint)
	}
	if strings.Contains(decision.Decision.ReplyHint, "申请加入") {
		t.Errorf("ReplyHint should NOT have join link when visibility lookup failed: %q", decision.Decision.ReplyHint)
	}
}

func TestDecodeFeishuConnectorConfig_RoundTrip(t *testing.T) {
	t.Parallel()

	raw, _ := json.Marshal(map[string]any{
		"connectors": map[string]any{
			"feishu": map[string]any{
				"enabled":                true,
				"app_id":                 "cli_abc",
				"app_secret_ref":         "secret_app",
				"verification_token_ref": "secret_verify",
				"encrypt_key_ref":        "secret_encrypt",
				"bot_open_id":            "ou_bot",
				"routing_mode":           "shared",
			},
		},
	})
	cfg, ok, err := DecodeFeishuConnectorConfig(raw)
	if err != nil {
		t.Fatalf("DecodeFeishuConnectorConfig: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if cfg.AppID != "cli_abc" || cfg.AppSecretRef != "secret_app" || cfg.RoutingMode != "shared" {
		t.Errorf("decoded cfg = %+v", cfg)
	}
}

func TestDecodeFeishuConnectorConfig_AbsentSubtreeReturnsOkFalse(t *testing.T) {
	t.Parallel()
	for _, raw := range [][]byte{
		nil,
		[]byte(`{}`),
		[]byte(`{"connectors":{}}`),
		[]byte(`{"connectors":{"slack":{"enabled":true}}}`),
	} {
		_, ok, err := DecodeFeishuConnectorConfig(raw)
		if err != nil {
			t.Errorf("input %q: unexpected error %v", string(raw), err)
		}
		if ok {
			t.Errorf("input %q: expected ok=false", string(raw))
		}
	}
}

func TestFeishuInboundEventFromWebhook_Parses(t *testing.T) {
	t.Parallel()
	body := []byte(`{
		"header": {"app_id": "cli_router_test"},
		"event": {
			"message": {
				"message_id": "om_x",
				"chat_id": "oc_x",
				"chat_type": "p2p",
				"thread_id": "",
				"content": "{\"text\":\"hi there\"}"
			},
			"sender": {
				"sender_id": {"open_id": "ou_alice", "user_id": "uid_alice", "union_id": "on_alice"},
				"tenant_key": "tk_x"
			}
		}
	}`)
	event, err := FeishuInboundEventFromWebhook(body)
	if err != nil {
		t.Fatalf("FeishuInboundEventFromWebhook: %v", err)
	}
	if event.AppID != "cli_router_test" {
		t.Errorf("AppID = %q", event.AppID)
	}
	if event.SenderUnionID != "on_alice" {
		t.Errorf("SenderUnionID = %q", event.SenderUnionID)
	}
	if event.Text != "hi there" {
		t.Errorf("Text = %q", event.Text)
	}
}

// TestReplyAnchorMessageID_AlwaysInboundMessageID pins the post-fix
// contract that prevents Feishu thread-mode reply fan-out: the anchor
// MUST be the inbound's own MessageID, regardless of whether
// RootID/ParentID/ThreadID are populated.
//
// Background — the bug this guards against:
//
//	Feishu's POST /im/v1/messages/{message_id}/reply, when the anchor is
//	a thread root and reply_in_thread=true, fans out a single send into
//	N distinct message_ids — one per existing reply already in the
//	thread. The user sees N visually-identical cards. DB-side trace
//	shows exactly one outbound row + one SendMessage + one returned om_
//	id, so the duplication is invisible without inspecting the client
//	or polling messages by id.
//
// Reference implementations consistently anchor reply on the inbound
// message_id, not on root_id; this matches.
func TestReplyAnchorMessageID_AlwaysInboundMessageID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		event FeishuInboundEvent
		want  string
	}{
		{
			name:  "top_level_message_no_thread",
			event: FeishuInboundEvent{MessageID: "om_inbound"},
			want:  "om_inbound",
		},
		{
			name: "thread_mode_anchor_must_NOT_use_root",
			event: FeishuInboundEvent{
				MessageID: "om_inbound",
				RootID:    "om_thread_root",
				ParentID:  "om_thread_parent",
				ThreadID:  "thr_xyz",
			},
			want: "om_inbound",
		},
		{
			name: "reply_to_message_anchor_must_NOT_use_parent",
			event: FeishuInboundEvent{
				MessageID: "om_inbound",
				ParentID:  "om_parent",
			},
			want: "om_inbound",
		},
		{
			name:  "empty_message_id_returns_empty",
			event: FeishuInboundEvent{RootID: "om_root", ParentID: "om_parent"},
			want:  "",
		},
		{
			name:  "whitespace_message_id_is_trimmed_away",
			event: FeishuInboundEvent{MessageID: "   "},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.event.ReplyAnchorMessageID(); got != tc.want {
				t.Errorf("anchor = %q, want %q — Feishu thread reply MUST anchor on the inbound MessageID to avoid fan-out", got, tc.want)
			}
		})
	}
}

// ThreadKey: root_id > message_id. We deliberately ignore ThreadID
// because real prod inbounds (2026-06-15 19:57, chat oc_0b5d…) show
// Feishu populates ThreadID with a separate identifier
// (omt_194a6050094f9b81) that does not overlap with the thread root's
// MessageID; using ThreadID would split a thread's root and its
// replies into two distinct keys. RootID is consistent: replies stamp
// the root's MessageID into RootID, and the root itself defaults to
// its own MessageID.
func TestThreadKey_PrefersRootIDThenMessageIDAndIgnoresThreadID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		event FeishuInboundEvent
		want  string
	}{
		{
			// Real prod payload: thread root message — RootID empty,
			// ThreadID empty, MessageID is the future RootID for all
			// replies inside this thread.
			name: "thread_root_message_uses_its_own_message_id",
			event: FeishuInboundEvent{
				MessageID: "om_x100b6dcac75f78a8c2d7390a8bafdaf",
			},
			want: "om_x100b6dcac75f78a8c2d7390a8bafdaf",
		},
		{
			// Real prod payload: reply inside the thread — RootID
			// = the thread root's MessageID, ThreadID is Feishu's
			// independent omt_… identifier (which we ignore).
			name: "thread_reply_uses_root_id_not_thread_id",
			event: FeishuInboundEvent{
				MessageID: "om_x100b6dcac49c40b0c2c16d6c2bde64e",
				RootID:    "om_x100b6dcac75f78a8c2d7390a8bafdaf",
				ParentID:  "om_x100b6dcac75f78a8c2d7390a8bafdaf",
				ThreadID:  "omt_194a6050094f9b81",
			},
			want: "om_x100b6dcac75f78a8c2d7390a8bafdaf",
		},
		{
			// Top-level non-thread message — no signals, falls through
			// to MessageID. Each such message gets its own conversation.
			name:  "non_thread_top_level_uses_message_id",
			event: FeishuInboundEvent{MessageID: "om_top"},
			want:  "om_top",
		},
		{
			// Whitespace-only RootID must not poison the key — fall
			// through to MessageID.
			name:  "whitespace_root_id_falls_through_to_message_id",
			event: FeishuInboundEvent{MessageID: "om_inbound", RootID: "   "},
			want:  "om_inbound",
		},
		{
			// ThreadID-only payload must NOT become the key. The reply
			// would never reach this branch in real traffic because
			// Feishu also sets RootID alongside ThreadID, but the
			// invariant matters: ThreadID is structurally ignored.
			name:  "thread_id_alone_is_ignored",
			event: FeishuInboundEvent{MessageID: "om_inbound", ThreadID: "omt_only"},
			want:  "om_inbound",
		},
		{
			name:  "all_empty_returns_empty",
			event: FeishuInboundEvent{},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.event.ThreadKey(); got != tc.want {
				t.Errorf("ThreadKey() = %q, want %q", got, tc.want)
			}
		})
	}
}
