package feishushared

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type Store interface {
	CreateInboundIMMessage(ctx context.Context, input store.CreateInboundIMMessageInput) (store.CreateInboundIMMessageResult, error)
	ListFeishuSharedBotAgents(ctx context.Context, senderUserID string, excludeAgentID string, limit int32) ([]store.FeishuSharedBotAgent, error)
	UpsertGatewaySessionSelection(ctx context.Context, input store.GatewaySessionSelectionInput) error
	GetGatewaySessionSelection(ctx context.Context, platform, externalID, externalThreadID string) (string, error)
	// Wipes the stored selection so the next inbound has to /select again;
	// called when the selected Agent has lost its active project binding.
	ClearGatewaySessionSelection(ctx context.Context, platform, externalID, externalThreadID string) error
	GetAgentByID(ctx context.Context, agentID string) (store.FeishuAgentRoute, error)
	GetAgentByFeishuAppID(ctx context.Context, appID string) (store.FeishuAgentRoute, error)
	FindUserIDByFeishuUnionID(ctx context.Context, unionID string) (string, error)
	IsActiveWorkspaceMember(ctx context.Context, workspaceID, userID string) (bool, error)
	// Feeds the visibility=workspace rejection card. Failures degrade the
	// card content but must not block the rejection itself.
	GetWorkspaceVisibility(ctx context.Context, workspaceID string) (string, error)
	ListActiveWorkspaceOwnerNames(ctx context.Context, workspaceID string, limit int32) ([]string, error)
	// Powers the /cancel command: resolves the chat thread to a Parsar
	// conversation id without going through CreateInboundIMMessage (which
	// would store "/cancel" as a user prompt), then bulk-cancels every
	// queued/running run on that conversation.
	FindConversationByExternalRef(ctx context.Context, gateway, externalChatID, externalThreadID string) (string, error)
	CancelAllInflightForConversation(ctx context.Context, conversationID, reason string) ([]store.SupersededRun, error)
}

type ReplyFunc func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string) error

// QuotedChainFunc fetches the rendered quoted-chain prefix for an
// inbound reply, or "" when there's no parent / fetching failed.
// Implementations may also mutate event.Metadata["attachments"] in place
// to inject parent-hop images alongside the user's own. Nil disables
// chain enrichment on this path.
type QuotedChainFunc func(ctx context.Context, host gateway.FeishuRouteAgent, event *gateway.FeishuInboundEvent) string

// QuotedChainPrefixMetadataKey mirrors store.TriggerMessageQuotedChainPrefixKey;
// the store layer prepends the value to TriggerMessageContent at
// dispatch time.
const QuotedChainPrefixMetadataKey = store.TriggerMessageQuotedChainPrefixKey

const (
	gatewayPlatformFeishu = "feishu"
)

type Outcome struct {
	Handled  bool
	Accepted bool
	Replied  bool
	Reason   string
	AgentID  string

	// InboundMessageID is the local UUID returned by
	// CreateInboundIMMessage on the accepted-and-stored path. Empty when
	// Accepted=false or when the outcome is a command echo (/list,
	// /select, /help) that doesn't create a user-message row. The Feishu
	// manager uses this to pair the stored row with the reaction_id
	// Feishu returns, so the outbound terminal path can undo the
	// "Typing" reaction once a terminal card lands.
	InboundMessageID string
}

func RoutingMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "shared", "router", "command_router":
		return "shared"
	default:
		return "direct"
	}
}

func IsSharedRoutingMode(raw string) bool {
	return RoutingMode(raw) == "shared"
}

func HandleInbound(ctx context.Context, st Store, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, reply ReplyFunc, quotedChain QuotedChainFunc, gateCfg gateway.GateConfig) (Outcome, error) {
	if st == nil {
		return Outcome{}, errors.New("feishushared: store is required")
	}
	senderSubject := SenderSubject(event)
	if strings.TrimSpace(senderSubject) == "" {
		return replyAndStop(ctx, reply, host, event, "无法识别飞书发送者，请稍后重试。", "missing_sender")
	}
	botSender := event.IsBotSender()

	var senderUserID string
	if !botSender {
		uid, err := findSenderUserID(ctx, st, senderSubject)
		if err != nil {
			return Outcome{}, err
		}
		senderUserID = uid
	}

	if !botSender {
		if cmd, args, ok := parseCommand(event.Text); ok {
			switch cmd {
			case "list":
				return handleList(ctx, st, host, event, reply, senderUserID)
			case "select":
				return handleSelect(ctx, st, host, event, reply, senderUserID, args)
			case "help":
				return replyAndStop(ctx, reply, host, event, helpText(), "help")
			case "cancel", "stop":
				return handleCancel(ctx, st, host, event, reply, args)
			}
		}
	}

	selectedAgentID, err := st.GetGatewaySessionSelection(ctx, gatewayPlatformFeishu, event.ChatID, "")
	if err != nil {
		if errors.Is(err, store.ErrUnknownGatewaySessionSelection) {
			if botSender {
				return Outcome{Handled: true, Reason: "bot_sender_no_selection"}, nil
			}
			return replyAndStop(ctx, reply, host, event, "请先发送 /list 查看可用 Agent，再发送 /select <agent-slug> 选择。", "selection_required")
		}
		return Outcome{}, err
	}
	target, err := st.GetAgentByID(ctx, selectedAgentID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownFeishuAgent) {
			if botSender {
				return Outcome{Handled: true, Reason: "bot_sender_selected_agent_unavailable"}, nil
			}
			return replyAndStop(ctx, reply, host, event, "当前选择的 Agent 已不可用，请重新发送 /list 后 /select。", "selected_agent_unavailable")
		}
		return Outcome{}, err
	}

	var (
		decision        gateway.FeishuInboundDecision
		initiatorUserID string
	)
	if botSender {
		decision = gateway.FeishuInboundDecision{
			Agent:          routeFromStore(target),
			Decision:       gateway.Decision{Allowed: true},
			NormalizedText: event.Text,
		}
		initiatorUserID = strings.TrimSpace(target.CreatedByUserID)
		if initiatorUserID == "" {
			return Outcome{Handled: true, Reason: "bot_sender_no_agent_creator", AgentID: target.AgentID}, nil
		}
	} else {
		decision, err = gateway.RouteFeishuInboundToAgent(ctx, routeAdapter{store: st}, event, routeFromStore(target), gateCfg)
		if err != nil {
			return Outcome{}, err
		}
		if !decision.Decision.Allowed {
			replied := false
			if strings.TrimSpace(decision.Decision.ReplyHint) != "" {
				if err := reply(ctx, host, event, decision.Decision.ReplyHint); err != nil {
					return Outcome{}, err
				}
				replied = true
			}
			return Outcome{Handled: true, Replied: replied, Reason: string(decision.Decision.Reason), AgentID: target.AgentID}, nil
		}
	}

	metadata := sharedMetadata(event, decision)
	if decision.Decision.GuestReplyHint != "" {
		metadata["guest_reply_hint"] = decision.Decision.GuestReplyHint
	}
	if botSender {
		metadata["bot_sender"] = true
		metadata["sender_type"] = event.SenderType
	}
	// Stamp the quoted-chain prefix into metadata when this inbound is
	// a reply. The store layer prepends it to TriggerMessageContent at
	// dispatch time; messages.content stays the user's verbatim input.
	// The callback may also mutate event.Metadata["attachments"] to add
	// parent-hop images; resync that key after the call so the prepared
	// metadata snapshot picks up the additions.
	if !botSender && quotedChain != nil {
		if quoted := quotedChain(ctx, host, &event); quoted != "" {
			metadata[QuotedChainPrefixMetadataKey] = quoted
		}
		if att, ok := event.Metadata["attachments"]; ok {
			metadata["attachments"] = att
		}
	}
	result, err := st.CreateInboundIMMessage(ctx, store.CreateInboundIMMessageInput{
		ConversationTitle: ConversationTitle(decision.NormalizedText),
		Text:              decision.NormalizedText,
		Mentions:          []string{"@" + decision.Agent.AgentName},
		Source:            "gateway",
		Gateway:           "feishu",
		ExternalUserID:    senderSubject,
		InitiatorUserID:   initiatorUserID,
		SenderOpenID:      event.SenderOpenID,
		ExternalChatID:    event.ChatID,
		ExternalThreadID:  event.ThreadKey(),
		ExternalMessageID: event.MessageID,
		TargetAgentID:     decision.Agent.AgentID,
		SourceAppID:       event.AppID,
		ConversationForm:  conversationForm(event),
		Metadata:          metadata,
	})
	if err != nil {
		if errors.Is(err, store.ErrUnknownMention) {
			if clearErr := st.ClearGatewaySessionSelection(ctx, gatewayPlatformFeishu, event.ChatID, ""); clearErr != nil {
				return Outcome{}, fmt.Errorf("clear stale gateway selection after binding loss: %w", clearErr)
			}
			if botSender {
				return Outcome{Handled: true, Reason: "bot_sender_selected_agent_binding_lost", AgentID: target.AgentID}, nil
			}
			return replyAndStop(ctx, reply, host, event,
				"当前选择的 Agent 已不可用(项目绑定已失效),请重新发送 /list 查看可用 Agent 后 /select 选择。",
				"selected_agent_binding_lost")
		}
		return Outcome{}, err
	}
	return Outcome{Handled: true, Accepted: true, Reason: "accepted", AgentID: target.AgentID, InboundMessageID: result.MessageID}, nil
}

func SenderSubject(event gateway.FeishuInboundEvent) string {
	for _, candidate := range []string{event.SenderUnionID, event.SenderOpenID, event.SenderUserID} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return ""
}

func findSenderUserID(ctx context.Context, st Store, subject string) (string, error) {
	if strings.TrimSpace(subject) == "" {
		return "", nil
	}
	userID, err := st.FindUserIDByFeishuUnionID(ctx, subject)
	if err == nil {
		return userID, nil
	}
	if errors.Is(err, store.ErrUnknownFeishuUser) {
		return "", nil
	}
	return "", err
}

func handleList(ctx context.Context, st Store, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, reply ReplyFunc, senderUserID string) (Outcome, error) {
	current, _ := st.GetGatewaySessionSelection(ctx, gatewayPlatformFeishu, event.ChatID, "")
	agents, err := st.ListFeishuSharedBotAgents(ctx, senderUserID, host.AgentID, 20)
	if err != nil {
		return Outcome{}, err
	}
	return replyAndStop(ctx, reply, host, event, formatAgentList(agents, current), "list")
}

func handleSelect(ctx context.Context, st Store, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, reply ReplyFunc, senderUserID string, args []string) (Outcome, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return replyAndStop(ctx, reply, host, event, "用法：/select <agent-slug>", "select_usage")
	}
	agents, err := st.ListFeishuSharedBotAgents(ctx, senderUserID, host.AgentID, 50)
	if err != nil {
		return Outcome{}, err
	}
	selected, err := selectAgent(agents, strings.TrimSpace(args[0]))
	if err != nil {
		return replyAndStop(ctx, reply, host, event, err.Error(), "select_failed")
	}
	if err := st.UpsertGatewaySessionSelection(ctx, store.GatewaySessionSelectionInput{
		Platform:   gatewayPlatformFeishu,
		ExternalID: event.ChatID,
		AgentID:    selected.AgentID,
		Metadata: map[string]any{
			"host_app_id": event.AppID,
			"tenant_key":  event.TenantKey,
			"chat_type":   event.ChatType,
		},
	}); err != nil {
		return Outcome{}, err
	}
	text := fmt.Sprintf("已选择 Agent「%s」（%s / %s）。", selected.AgentName, selected.WorkspaceName, selected.ProjectName)
	return replyAndStop(ctx, reply, host, event, text, "selected")
}

// handleCancel marks every queued/running run on the conversation as
// cancelled; the connector sees the status flip on its next tick. We
// deliberately do NOT call connector.Abort here — that would couple the
// router to the connector registry, and the poll-driven cancel is
// enough to unblock the queue.
func handleCancel(ctx context.Context, st Store, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, reply ReplyFunc, args []string) (Outcome, error) {
	scope := "current"
	if len(args) > 0 && strings.EqualFold(strings.TrimSpace(args[0]), "all") {
		scope = "all"
	}
	conversationID, err := st.FindConversationByExternalRef(ctx, gatewayPlatformFeishu, event.ChatID, event.ThreadKey())
	if err != nil {
		if errors.Is(err, store.ErrUnknownConversation) {
			return replyAndStop(ctx, reply, host, event,
				"当前会话还没有进行中的任务，无法取消。", "cancel_no_conversation")
		}
		return Outcome{}, err
	}
	reason := "feishu_user_cancel"
	if scope == "all" {
		reason = "feishu_user_cancel_all"
	}
	cancelled, err := st.CancelAllInflightForConversation(ctx, conversationID, reason)
	if err != nil {
		return Outcome{}, err
	}
	if len(cancelled) == 0 {
		return replyAndStop(ctx, reply, host, event, "当前没有进行中的任务。", "cancel_none")
	}
	msg := fmt.Sprintf("已取消 %d 个任务。", len(cancelled))
	return replyAndStop(ctx, reply, host, event, msg, "cancelled")
}

func replyAndStop(ctx context.Context, reply ReplyFunc, host gateway.FeishuRouteAgent, event gateway.FeishuInboundEvent, text string, reason string) (Outcome, error) {
	if reply == nil {
		return Outcome{Handled: true, Replied: false, Reason: reason}, nil
	}
	if err := reply(ctx, host, event, text); err != nil {
		return Outcome{}, err
	}
	return Outcome{Handled: true, Replied: true, Reason: reason}, nil
}

func parseCommand(text string) (cmd string, args []string, ok bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", nil, false
	}
	parts := strings.Fields(strings.TrimPrefix(text, "/"))
	if len(parts) == 0 {
		return "", nil, false
	}
	return strings.ToLower(parts[0]), parts[1:], true
}

// CancelCommand summarises a parsed /cancel or /cancel all input.
type CancelCommand struct {
	Scope string // "current" | "all"
}

// ParseCancelCommand recognises `/cancel`, `/cancel all`, `/stop`, and
// `/stop all`. Returns ok=false for anything else so the caller can
// fall through to the normal prompt path.
func ParseCancelCommand(text string) (CancelCommand, bool) {
	cmd, args, ok := parseCommand(text)
	if !ok {
		return CancelCommand{}, false
	}
	switch cmd {
	case "cancel", "stop":
		scope := "current"
		if len(args) > 0 && strings.EqualFold(strings.TrimSpace(args[0]), "all") {
			scope = "all"
		}
		return CancelCommand{Scope: scope}, true
	}
	return CancelCommand{}, false
}

// formatAgentList renders /list output grouped by workspace. We render
// agent.slug (not a positional [N]) because the visible set is
// per-sender, so any index would mean different things to different
// people in the same chat.
func formatAgentList(agents []store.FeishuSharedBotAgent, currentAgentID string) string {
	if len(agents) == 0 {
		return "暂无可用 Agent。"
	}
	groups := make(map[string][]store.FeishuSharedBotAgent)
	var groupOrder []string
	for _, agent := range agents {
		key := agent.WorkspaceName
		if _, ok := groups[key]; !ok {
			groupOrder = append(groupOrder, key)
		}
		groups[key] = append(groups[key], agent)
	}
	lines := []string{"可用 Agent："}
	for _, name := range groupOrder {
		lines = append(lines, "", fmt.Sprintf("---- %s ----", name))
		for _, agent := range groups[name] {
			marker := ""
			if agent.AgentID == currentAgentID {
				marker = " ✓"
			}
			lines = append(lines, fmt.Sprintf("%s（%s — %s）%s", agent.AgentSlug, agent.AgentName, agent.ProjectName, marker))
		}
	}
	lines = append(lines, "", "发送 /select <agent-slug> 选择。")
	return strings.Join(lines, "\n")
}

func selectAgent(agents []store.FeishuSharedBotAgent, token string) (store.FeishuSharedBotAgent, error) {
	token = strings.ToLower(strings.TrimSpace(token))
	for _, agent := range agents {
		if strings.EqualFold(agent.AgentSlug, token) {
			return agent, nil
		}
	}
	return store.FeishuSharedBotAgent{}, fmt.Errorf("未找到 Agent %q，请发送 /list 查看可用 Agent。", token)
}

func helpText() string {
	return "可用命令：\n/list — 查看可用 Agent\n/select <agent-slug> — 切换当前 Agent\n/cancel — 取消当前会话进行中的任务\n/help — 显示帮助"
}

func sharedMetadata(event gateway.FeishuInboundEvent, decision gateway.FeishuInboundDecision) map[string]any {
	metadata := map[string]any{
		"chat_type":      event.ChatType,
		"tenant_key":     event.TenantKey,
		"sender_state":   decision.SenderState,
		"message_type":   event.MessageType,
		"raw_content":    event.RawContent,
		"root_id":        event.RootID,
		"parent_id":      event.ParentID,
		"thread_id":      event.ThreadID,
		"shared_bot":     true,
		"host_app_id":    event.AppID,
		"selected_agent": decision.Agent.AgentID,
		"selected_slug":  decision.Agent.AgentSlug,
	}
	for key, value := range event.Metadata {
		if strings.TrimSpace(key) == "" || value == nil {
			continue
		}
		metadata[key] = value
	}
	return metadata
}

func conversationForm(event gateway.FeishuInboundEvent) string {
	if strings.EqualFold(strings.TrimSpace(event.ChatType), "p2p") {
		return "dm"
	}
	return "group"
}

type routeAdapter struct {
	store Store
}

func (r routeAdapter) GetAgentByFeishuAppID(ctx context.Context, appID string) (gateway.FeishuRouteAgent, error) {
	route, err := r.store.GetAgentByFeishuAppID(ctx, appID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownFeishuAgent) {
			return gateway.FeishuRouteAgent{}, gateway.ErrFeishuRouterUnknownAgent
		}
		return gateway.FeishuRouteAgent{}, err
	}
	return routeFromStore(route), nil
}

func (r routeAdapter) GetAgentByID(ctx context.Context, agentID string) (gateway.FeishuRouteAgent, error) {
	route, err := r.store.GetAgentByID(ctx, agentID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownFeishuAgent) {
			return gateway.FeishuRouteAgent{}, gateway.ErrFeishuRouterUnknownAgent
		}
		return gateway.FeishuRouteAgent{}, err
	}
	return routeFromStore(route), nil
}

func (r routeAdapter) FindUserIDByFeishuUnionID(ctx context.Context, unionID string) (string, error) {
	userID, err := r.store.FindUserIDByFeishuUnionID(ctx, unionID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownFeishuUser) {
			return "", gateway.ErrFeishuRouterUnknownUser
		}
		return "", err
	}
	return userID, nil
}

func (r routeAdapter) IsActiveWorkspaceMember(ctx context.Context, workspaceID, userID string) (bool, error) {
	return r.store.IsActiveWorkspaceMember(ctx, workspaceID, userID)
}

func (r routeAdapter) GetWorkspaceVisibility(ctx context.Context, workspaceID string) (string, error) {
	return r.store.GetWorkspaceVisibility(ctx, workspaceID)
}

func (r routeAdapter) ListWorkspaceOwnerNames(ctx context.Context, workspaceID string, limit int32) ([]string, error) {
	return r.store.ListActiveWorkspaceOwnerNames(ctx, workspaceID, limit)
}

func routeFromStore(route store.FeishuAgentRoute) gateway.FeishuRouteAgent {
	return gateway.FeishuRouteAgent{
		AgentID:         route.AgentID,
		WorkspaceID:     route.WorkspaceID,
		WorkspaceName:   route.WorkspaceName,
		AgentName:       route.AgentName,
		AgentSlug:       route.AgentSlug,
		Visibility:      gateway.Visibility(route.Visibility),
		Config:          route.Config,
		CreatedByUserID: route.CreatedByUserID,
	}
}

// ConversationTitle builds the sidebar title for a Feishu-sourced
// conversation. The 30-rune cap (not bytes) avoids slicing Chinese
// titles mid-codepoint. Shared by feishuinbound, feishushared, and
// dev/routes.go — all three must agree since they create the same
// conversation rows.
func ConversationTitle(text string) string {
	const prefix = "[飞书] "
	const maxRunes = 30
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return prefix + "未命名"
	}
	runes := []rune(trimmed)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return prefix + string(runes)
}
