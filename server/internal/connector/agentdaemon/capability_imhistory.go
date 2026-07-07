package agentdaemon

import (
	"context"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
)

// Auto-injected fetch-chat-history capability.
//
// Every agent_daemon-backed agent gets the ability to pull IM group-chat
// history for the CURRENT conversation for free — no capability row, no user
// configuration. Delivery is universal across agent kinds (claude_code /
// codex / opencode / pi): a system_prompt instruction tells the agent to run
// a single curl command, and three env vars carry the scoped URL + token +
// conversation id into the sandbox shell.
//
//   - The instruction references env var NAMES ($PARSAR_IM_HISTORY_TOKEN),
//     never the literal token, so the token stays out of model context.
//   - The env vars ride opts["env"], which each daemon adapter forwards to the
//     agent CLI subprocess; the agent's shell/bash tool inherits them.
//   - curl is present in every sandbox image, so nothing has to be seeded or
//     baked at runtime.
//
// Tokens + IM SDK logic stay server-side; the sandbox only ever sees a scoped
// URL + HMAC bearer token minted per conversation by the connector.

// imHistoryServerName is the built-in capability key used for the per-agent
// enable/disable flag (agent_builtin_capabilities.enabled).
const imHistoryServerName = "parsar_chat_history"

// imHistoryInstruction is the system_prompt block appended when the built-in
// is enabled. It teaches any agent kind how to fetch group-chat history via a
// single shell command. Platform caps are informational — the server-side
// adapter clamps the requested limit to its platform ceiling regardless and
// returns the effective cap in the "cap" field.
const imHistoryInstruction = `## Fetching IM group-chat history

You can read recent IM group-chat history for the CURRENT conversation
(Feishu / Slack / Discord / Teams) by running a single shell command. The
following environment variables are already set in your shell — reference them
directly, and never ask the user for a token:

  PARSAR_IM_HISTORY_URL, PARSAR_IM_HISTORY_TOKEN, PARSAR_CONVERSATION_ID

Command:

  curl -s "$PARSAR_IM_HISTORY_URL?conversation_id=$PARSAR_CONVERSATION_ID&limit=50" \
    -H "Authorization: Bearer $PARSAR_IM_HISTORY_TOKEN"

The response is JSON: a "messages" array (oldest-first) where each item has
id, sender, sender_id, text, thread_id, created_at (RFC3339), and from_bot;
plus "next_cursor", "cap" (the platform's effective per-page ceiling), and
"platform".

- limit is your requested page size. It is silently clamped to the platform
  cap (Feishu 50, Slack 15, Discord 100, Teams 50); check the returned "cap"
  for the effective ceiling.
- To page further back, pass the previous response's "next_cursor" as
  "&cursor=<value>".
- To scope history to one platform-native thread, add "&thread_id=<id>"
  (Slack thread_ts / Discord thread channel id / Teams reply message id).
  Omit it to read the whole chat.`

// imHistoryEnv returns the three env vars carrying the scoped history endpoint,
// bearer token, and conversation id for a conversation — or (nil,false) when
// the tool is disabled (no endpoint/signer configured, or no conversation to
// scope the token to).
func (c *Connector) imHistoryEnv(conversationID string) (map[string]string, bool) {
	conversationID = strings.TrimSpace(conversationID)
	if c.imHistoryEndpoint == "" || c.imHistoryToken == nil || conversationID == "" {
		return nil, false
	}
	token := c.imHistoryToken(conversationID)
	if token == "" {
		return nil, false
	}
	return map[string]string{
		"PARSAR_IM_HISTORY_URL":   c.imHistoryEndpoint,
		"PARSAR_IM_HISTORY_TOKEN": token,
		"PARSAR_CONVERSATION_ID":  conversationID,
	}, true
}

// applyIMHistoryPromptInjection folds the fetch-chat-history capability into
// opts: it merges the three env vars into opts["env"] and appends the
// instruction to opts["system_prompt"]. It is a no-op when:
//
//   - an override_system_prompt is set (an explicit override fully replaces the
//     system prompt, mirroring applySpecMemoryInjection's override guard);
//   - the built-in is disabled for this agent;
//   - the endpoint/signer/conversation is not configured (imHistoryEnv miss).
func (c *Connector) applyIMHistoryPromptInjection(ctx context.Context, opts map[string]any, in connector.PromptInput) {
	if stringFromMap(opts, "override_system_prompt") != "" {
		return
	}
	if !c.imHistoryEnabledForAgent(ctx, in.AgentID) {
		return
	}
	histEnv, ok := c.imHistoryEnv(in.ConversationID)
	if !ok {
		return
	}

	env := copyStringAnyMap(opts["env"])
	for k, v := range histEnv {
		env[k] = v
	}
	opts["env"] = env

	base := stringFromMap(opts, "system_prompt")
	if base == "" {
		opts["system_prompt"] = imHistoryInstruction
		return
	}
	opts["system_prompt"] = base + "\n\n" + imHistoryInstruction
}

// imHistoryEnabledForAgent reports whether the built-in fetch-chat-history
// capability should be injected for this agent. Built-ins default to ON, so a
// nil store, an empty agent id, or a lookup error all resolve to enabled — a
// bookkeeping failure must never silently strip the capability. Only an
// explicit disabled flag (agent_builtin_capabilities.enabled = false)
// suppresses injection.
func (c *Connector) imHistoryEnabledForAgent(ctx context.Context, agentID string) bool {
	if c.capabilities == nil || strings.TrimSpace(agentID) == "" {
		return true
	}
	enabled, err := c.capabilities.IsBuiltinCapabilityEnabled(ctx, agentID, imHistoryServerName)
	if err != nil {
		c.log.Debug("agent_daemon: im-history builtin flag lookup failed; defaulting on", "agent_id", agentID, "err", err)
		return true
	}
	return enabled
}
