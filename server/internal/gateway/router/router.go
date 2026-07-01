package router

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type Store interface {
	CreateInboundIMMessage(ctx context.Context, input store.CreateInboundIMMessageInput) (store.CreateInboundIMMessageResult, error)
	ListFeishuSharedBotAgents(ctx context.Context, senderUserID string, excludeAgentID string, limit int32) ([]store.FeishuSharedBotAgent, error)
	UpsertGatewaySessionSelection(ctx context.Context, input store.GatewaySessionSelectionInput) error
	GetGatewaySessionSelection(ctx context.Context, platform, externalID, externalThreadID string) (string, error)
	// Wipes the stored selection so the next inbound has to /select again;
	// called when the selected Agent has lost its active workspace binding.
	ClearGatewaySessionSelection(ctx context.Context, platform, externalID, externalThreadID string) error
	GetAgentByID(ctx context.Context, agentID string) (store.FeishuAgentRoute, error)
	GetAgentByFeishuAppID(ctx context.Context, appID string) (store.FeishuAgentRoute, error)
	FindUserIDByPlatformSubject(ctx context.Context, platform, subject string) (string, error)
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
	// Backs the "已激活话题续聊不必再 @" rule: reports whether the bot has
	// previously accepted an inbound message in this (platform, chat, thread).
	HasThreadInboundHistory(ctx context.Context, platform, externalChatID, threadKey string) (bool, error)
}

type ReplyFunc func(ctx context.Context, host gateway.FeishuRouteAgent, event gateway.InboundEvent, text string) error

// QuotedChainFunc fetches the rendered quoted-chain prefix for an
// inbound reply, or "" when there's no parent / fetching failed.
// Implementations may also mutate event.Metadata["attachments"] in place
// to inject parent-hop images alongside the user's own. Nil disables
// chain enrichment on this path.
type QuotedChainFunc func(ctx context.Context, host gateway.FeishuRouteAgent, event *gateway.InboundEvent) string

// QuotedChainPrefixMetadataKey mirrors store.TriggerMessageQuotedChainPrefixKey;
// the store layer prepends the value to TriggerMessageContent at
// dispatch time.
const QuotedChainPrefixMetadataKey = store.TriggerMessageQuotedChainPrefixKey

// platformFeishu sources the gateway platform string from the channel
// package's canonical constant (== "feishu"). It is the fallback for
// eventPlatform when an inbound event carries no platform tag (e.g. a
// directly-constructed event in tests), preserving the pre-N4 behaviour.
var platformFeishu = string(channel.PlatformFeishu)

// eventPlatform is the store-facing platform discriminator for an inbound
// event: the session-selection namespace and the stored Gateway tag. It
// reads the platform off the neutral event (Feishu Normalize stamps
// "feishu", Slack "slack") so a second platform routes under its own
// namespace instead of colliding with Feishu's. Empty falls back to
// platformFeishu so Feishu parity is byte-identical.
func eventPlatform(event gateway.InboundEvent) string {
	if p := strings.TrimSpace(event.Platform); p != "" {
		return p
	}
	return platformFeishu
}

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

func HandleInbound(ctx context.Context, st Store, host gateway.FeishuRouteAgent, event gateway.InboundEvent, reply ReplyFunc, quotedChain QuotedChainFunc, gateCfg gateway.GateConfig) (Outcome, error) {
	if st == nil {
		return Outcome{}, errors.New("router: store is required")
	}
	senderSubject := SenderSubject(event)
	if strings.TrimSpace(senderSubject) == "" {
		return replyAndStop(ctx, reply, host, event, "无法识别飞书发送者，请稍后重试。", "missing_sender")
	}
	botSender := event.SenderIsBot

	var senderUserID string
	if !botSender {
		uid, err := findSenderUserID(ctx, st, event.Platform, senderSubject)
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

	selectedAgentID, err := st.GetGatewaySessionSelection(ctx, eventPlatform(event), event.ExternalChatID, "")
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
		decision, err = gateway.RouteInboundToAgent(ctx, routeAdapter{store: st}, event, routeFromStore(target), gateCfg)
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
		if v, ok := event.Metadata["sender_type"]; ok {
			metadata["sender_type"] = v
		}
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
		Gateway:           eventPlatform(event),
		ExternalUserID:    senderSubject,
		InitiatorUserID:   initiatorUserID,
		SenderOpenID:      event.Sender.LocalUserID,
		ExternalChatID:    event.ExternalChatID,
		ExternalThreadID:  event.ThreadKey(),
		ExternalMessageID: event.ExternalMessageID,
		TargetAgentID:     decision.Agent.AgentID,
		SourceAppID:       event.BotID,
		ConversationForm:  conversationForm(event),
		Metadata:          metadata,
	})
	if err != nil {
		if errors.Is(err, store.ErrUnknownMention) {
			if clearErr := st.ClearGatewaySessionSelection(ctx, eventPlatform(event), event.ExternalChatID, ""); clearErr != nil {
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

func SenderSubject(event gateway.InboundEvent) string {
	for _, candidate := range []string{event.Sender.PlatformUserID, event.Sender.LocalUserID} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return ""
}

func findSenderUserID(ctx context.Context, st Store, platform, subject string) (string, error) {
	if strings.TrimSpace(subject) == "" {
		return "", nil
	}
	userID, err := st.FindUserIDByPlatformSubject(ctx, platform, subject)
	if err == nil {
		return userID, nil
	}
	if errors.Is(err, store.ErrUnknownPlatformUser) {
		return "", nil
	}
	return "", err
}

func replyAndStop(ctx context.Context, reply ReplyFunc, host gateway.FeishuRouteAgent, event gateway.InboundEvent, text string, reason string) (Outcome, error) {
	if reply == nil {
		return Outcome{Handled: true, Replied: false, Reason: reason}, nil
	}
	if err := reply(ctx, host, event, text); err != nil {
		return Outcome{}, err
	}
	return Outcome{Handled: true, Replied: true, Reason: reason}, nil
}

func sharedMetadata(event gateway.InboundEvent, decision gateway.FeishuInboundDecision) map[string]any {
	metadata := map[string]any{
		"chat_type":      event.ChatType,
		"tenant_key":     event.Sender.TenantKey,
		"sender_state":   decision.SenderState,
		"root_id":        event.ExternalRootID,
		"parent_id":      event.ReplyTo,
		"thread_id":      event.ExternalThreadID,
		"shared_bot":     true,
		"host_app_id":    event.BotID,
		"selected_agent": decision.Agent.AgentID,
		"selected_slug":  decision.Agent.AgentSlug,
	}
	// message_type / raw_content live in the neutral event's Metadata bag
	// (folded there by NeutralFromFeishuEvent); surface them back as typed
	// keys so the stored jsonb matches the legacy Feishu-typed path.
	if v, ok := event.Metadata["message_type"]; ok {
		metadata["message_type"] = v
	}
	if v, ok := event.Metadata["raw_content"]; ok {
		metadata["raw_content"] = v
	}
	for key, value := range event.Metadata {
		if strings.TrimSpace(key) == "" || value == nil {
			continue
		}
		// These three already have explicit handling above (message_type /
		// raw_content) or belong to the bot-sender branch (sender_type);
		// skip them in the blanket merge to avoid clobbering / leaking.
		switch key {
		case "message_type", "raw_content", "sender_type":
			continue
		}
		metadata[key] = value
	}
	return metadata
}

func conversationForm(event gateway.InboundEvent) string {
	if strings.EqualFold(strings.TrimSpace(event.ChatType), "dm") {
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

func (r routeAdapter) FindUserIDByPlatformSubject(ctx context.Context, platform, subject string) (string, error) {
	userID, err := r.store.FindUserIDByPlatformSubject(ctx, platform, subject)
	if err != nil {
		if errors.Is(err, store.ErrUnknownPlatformUser) {
			return "", gateway.ErrRouterUnknownUser
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
// titles mid-codepoint. Shared by feishuinbound, router, and
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
