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
	agents, err := st.ListFeishuSharedBotAgents(ctx, host.WorkspaceID, senderUserID, host.AgentID, 20)
	if err != nil {
		return Outcome{}, err
	}
	return replyAndStop(ctx, reply, host, event, formatAgentList(agents, current), "list")
}

func handleSelect(ctx context.Context, st Store, host gateway.FeishuRouteAgent, event gateway.InboundEvent, reply ReplyFunc, senderUserID string, args []string) (Outcome, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return replyAndStop(ctx, reply, host, event, "Usage: /select <agent-slug>", "select_usage")
	}
	agents, err := st.ListFeishuSharedBotAgents(ctx, host.WorkspaceID, senderUserID, host.AgentID, 50)
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
	text := fmt.Sprintf("Selected Agent \"%s\" (%s / %s).", selected.AgentName, selected.WorkspaceName, selected.WorkspaceSlug)
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
				"This conversation has no in-progress tasks to cancel.", "cancel_no_conversation")
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
		return replyAndStop(ctx, reply, host, event, "No in-progress tasks.", "cancel_none")
	}
	msg := fmt.Sprintf("Cancelled %d task(s).", len(cancelled))
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
		return "No agents available."
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
	lines := []string{"Available Agents:"}
	for _, name := range groupOrder {
		lines = append(lines, "", fmt.Sprintf("---- %s ----", name))
		for _, agent := range groups[name] {
			marker := ""
			if agent.AgentID == currentAgentID {
				marker = " ✓"
			}
			lines = append(lines, fmt.Sprintf("%s (%s — %s)%s", agent.AgentSlug, agent.AgentName, agent.WorkspaceSlug, marker))
		}
	}
	lines = append(lines, "", "Send /select <agent-slug> to choose.")
	return strings.Join(lines, "\n")
}

func selectAgent(agents []store.FeishuSharedBotAgent, token string) (store.FeishuSharedBotAgent, error) {
	token = strings.ToLower(strings.TrimSpace(token))
	for _, agent := range agents {
		if strings.EqualFold(agent.AgentSlug, token) {
			return agent, nil
		}
	}
	return store.FeishuSharedBotAgent{}, fmt.Errorf("Agent %q not found; send /list to see available Agents.", token)
}

func helpText() string {
	return "Available commands:\n/list — list available Agents\n/select <agent-slug> — switch the current Agent\n/cancel — cancel in-progress tasks in this conversation\n/help — show help"
}
