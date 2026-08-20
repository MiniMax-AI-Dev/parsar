// Server-side conversation history injection and stale-session recovery.
//
// Engines without resume receive the transcript on every turn. Engines with
// resume receive the same bounded block separately, so an adapter can use it
// only if the upstream session has disappeared.
//
// The transcript is a bounded tail rather than the whole conversation so a
// fallback does not turn one lost engine session into an unbounded prompt.
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

	block, count := c.readConversationHistoryBlock(ctx, in)
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
		"turn_count", count,
		"block_bytes", len(block))
}

func (c *Connector) resumeFallbackPrompt(
	ctx context.Context,
	in connector.PromptInput,
	info store.AgentDaemonSupportedAgentKind,
	sessionID string,
	opts map[string]any,
) string {
	if !info.Capabilities.Resume || strings.TrimSpace(sessionID) == "" || stringFromMap(opts, "override_system_prompt") != "" {
		return ""
	}
	block, _ := c.readConversationHistoryBlock(ctx, in)
	return block
}

func (c *Connector) readConversationHistoryBlock(ctx context.Context, in connector.PromptInput) (string, int) {
	if c.conversationHistory == nil || strings.TrimSpace(in.ConversationID) == "" {
		return "", 0
	}
	messages, err := c.conversationHistory.ListRecentConversationHistory(ctx, in.ConversationID, historyTurnLimit)
	if err != nil {
		c.log.Warn("agent_daemon: conversation history read failed; proceeding without transcript",
			"run_id", in.RunID, "conversation_id", in.ConversationID, "err", err.Error())
		return "", 0
	}
	return renderConversationHistory(messages, in.TriggerMessageID, in.TriggerMessageContent), len(messages)
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
