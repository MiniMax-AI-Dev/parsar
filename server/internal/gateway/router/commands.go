package router

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func handleList(ctx context.Context, st Store, host gateway.FeishuRouteAgent, event gateway.InboundEvent, reply ReplyFunc, senderUserID string) (Outcome, error) {
	current, _ := st.GetGatewaySessionSelection(ctx, eventPlatform(event), event.ExternalChatID, "")
	agents, err := st.ListFeishuSharedBotAgents(ctx, senderUserID, host.AgentID, 20)
	if err != nil {
		return Outcome{}, err
	}
	return replyAndStop(ctx, reply, host, event, formatAgentList(agents, current), "list")
}

func handleSelect(ctx context.Context, st Store, host gateway.FeishuRouteAgent, event gateway.InboundEvent, reply ReplyFunc, senderUserID string, args []string) (Outcome, error) {
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
		Platform:   eventPlatform(event),
		ExternalID: event.ExternalChatID,
		AgentID:    selected.AgentID,
		Metadata: map[string]any{
			"host_app_id": event.BotID,
			"tenant_key":  event.Sender.TenantKey,
			"chat_type":   event.ChatType,
		},
	}); err != nil {
		return Outcome{}, err
	}
	text := fmt.Sprintf("已选择 Agent「%s」（%s / %s）。", selected.AgentName, selected.WorkspaceName, selected.WorkspaceSlug)
	return replyAndStop(ctx, reply, host, event, text, "selected")
}

// handleCancel marks every queued/running run on the conversation as
// cancelled; the connector sees the status flip on its next tick. We
// deliberately do NOT call connector.Abort here — that would couple the
// router to the connector registry, and the poll-driven cancel is
// enough to unblock the queue.
func handleCancel(ctx context.Context, st Store, host gateway.FeishuRouteAgent, event gateway.InboundEvent, reply ReplyFunc, args []string) (Outcome, error) {
	scope := "current"
	if len(args) > 0 && strings.EqualFold(strings.TrimSpace(args[0]), "all") {
		scope = "all"
	}
	conversationID, err := st.FindConversationByExternalRef(ctx, eventPlatform(event), event.ExternalChatID, event.ThreadKey())
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
			lines = append(lines, fmt.Sprintf("%s（%s — %s）%s", agent.AgentSlug, agent.AgentName, agent.WorkspaceSlug, marker))
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
