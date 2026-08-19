// Server-side conversation history for engines that cannot resume.
//
// claude_code, codex and pi keep their own conversation state and get an
// upstream session id back through agent_engine_sessions, so the daemon
// replays nothing for them. opencode and deepseek_harness advertise
// Capabilities.Resume=false: every prompt is a fresh engine session, so
// without this injection turn two has no idea what turn one said.
//
// The transcript is folded into the system-prompt slot, which every adapter
// already forwards (as --append-system-prompt, or prepended to the task for
// the engines with no system-prompt flag). It is deliberately a bounded tail
// rather than the whole conversation: these engines have no prompt-cache
// reuse, so every injected byte is paid for on every turn.
package agentdaemon

import (
	"context"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

const (
	// historyTurnLimit bounds how many stored turns are read and rendered.
	historyTurnLimit = 12

	// historyTotalBudgetBytes caps the rendered block. Oldest turns are
	// dropped first once the budget is exhausted.
	historyTotalBudgetBytes = 6000

	// historyMessageBudgetBytes caps one turn so a single pasted log cannot
	// consume the whole block.
	historyMessageBudgetBytes = 800
)

const historyHeader = `## Earlier turns in this conversation

You start every turn without memory of previous ones, so the recent
exchange is reproduced below for context. It is history, not a new
request: do not answer it again, and do not repeat work already done.`

// ConversationHistoryReader is the narrow read surface the injection needs.
// Satisfied by *store.Store.
type ConversationHistoryReader interface {
	ListRecentConversationHistory(ctx context.Context, conversationID string, limit int32) ([]store.ConversationHistoryMessage, error)
}

// applyConversationHistoryInjection appends the recent transcript to
// opts["system_prompt"] when the bound engine cannot resume its own session.
//
// The gate is the device's live heartbeat descriptor rather than a
// server-side list of engine names, so an engine that gains resume support
// stops getting a duplicate transcript the moment it advertises it.
//
// Fail-soft: a read error is logged and swallowed. Losing context degrades
// an answer; failing the prompt loses the turn.
func (c *Connector) applyConversationHistoryInjection(
	ctx context.Context,
	opts map[string]any,
	in connector.PromptInput,
	info store.AgentDaemonSupportedAgentKind,
) {
	if c.conversationHistory == nil || opts == nil {
		return
	}
	if info.Capabilities.Resume {
		return
	}
	// An explicit override owns the whole system prompt, mirroring
	// applySpecMemoryInjection and applyIMHistoryPromptInjection.
	if stringFromMap(opts, "override_system_prompt") != "" {
		return
	}
	if strings.TrimSpace(in.ConversationID) == "" {
		return
	}

	messages, err := c.conversationHistory.ListRecentConversationHistory(ctx, in.ConversationID, historyTurnLimit)
	if err != nil {
		c.log.Warn("agent_daemon: conversation history read failed; proceeding without transcript",
			"run_id", in.RunID, "conversation_id", in.ConversationID, "err", err.Error())
		return
	}
	block := renderConversationHistory(messages, in.TriggerMessageID, in.TriggerMessageContent)
	if block == "" {
		return
	}
	base := stringFromMap(opts, "system_prompt")
	if base == "" {
		opts["system_prompt"] = block
	} else {
		opts["system_prompt"] = base + "\n\n" + block
	}
	c.log.Info("agent_daemon: conversation history injected",
		"run_id", in.RunID,
		"agent_kind", info.Kind,
		"turn_count", len(messages),
		"block_bytes", len(block))
}

// renderConversationHistory renders stored turns oldest-first, excluding the
// message that triggered this run. Returns "" when nothing is left to say.
func renderConversationHistory(messages []store.ConversationHistoryMessage, triggerMessageID, triggerContent string) string {
	lines := make([]string, 0, len(messages))
	for _, msg := range messages {
		if isTriggerMessage(msg, triggerMessageID, triggerContent) {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		lines = append(lines, historySpeaker(msg.SenderType)+": "+truncateHistoryText(content, historyMessageBudgetBytes))
	}
	if len(lines) == 0 {
		return ""
	}
	// Drop from the oldest end until the block fits; the newest turns are
	// the ones the next answer depends on.
	budget := historyTotalBudgetBytes - len(historyHeader)
	for len(lines) > 1 && historyBlockSize(lines) > budget {
		lines = lines[1:]
	}
	if historyBlockSize(lines) > budget {
		lines[0] = truncateHistoryText(lines[0], budget)
	}
	return historyHeader + "\n\n" + strings.Join(lines, "\n\n")
}

func historyBlockSize(lines []string) int {
	total := 0
	for _, line := range lines {
		total += len(line) + 2
	}
	return total
}

// isTriggerMessage reports whether a stored turn is the task this run is
// already carrying. The id is authoritative; the content comparison only
// covers callers that synthesize a prompt without a stored message id, and
// tolerates the gateway's quoted-chain prefix, which rides on the dispatched
// content but not on the stored row.
func isTriggerMessage(msg store.ConversationHistoryMessage, triggerMessageID, triggerContent string) bool {
	if id := strings.TrimSpace(triggerMessageID); id != "" {
		return msg.ID == id
	}
	stored := strings.TrimSpace(msg.Content)
	trigger := strings.TrimSpace(triggerContent)
	if stored == "" || trigger == "" {
		return false
	}
	return stored == trigger || strings.HasSuffix(trigger, stored)
}

func historySpeaker(senderType string) string {
	switch strings.TrimSpace(senderType) {
	case "agent":
		return "Assistant"
	default:
		// user + external (unregistered IM sender) are both humans here.
		return "User"
	}
}

func truncateHistoryText(text string, budget int) string {
	if budget <= 0 || len(text) <= budget {
		return text
	}
	const marker = "… [truncated]"
	if budget <= len(marker) {
		return text[:budget]
	}
	cut := budget - len(marker)
	// Trim a partial UTF-8 sequence rather than emitting a broken rune.
	for cut > 0 && !utf8Boundary(text[cut]) {
		cut--
	}
	return text[:cut] + marker
}

func utf8Boundary(b byte) bool { return b&0xC0 != 0x80 }
