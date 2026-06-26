package codex

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// ItemBuffers holds the per-itemId text accumulators used to fold
// delta notifications back into a single chunk on item/completed.
// Codex streams reasoning and agentMessage in two channels — the
// {reasoning,agentMessage}/delta notifications, plus the final item
// body on item/completed. We keep both: deltas drive incremental UI
// (TypeDelta / TypeThinking), and completed-item bodies anchor the
// final text for the done event.
type ItemBuffers struct {
	Reasoning   map[string]string
	AgentText   map[string]string
}

// NewItemBuffers returns an empty buffer set.
func NewItemBuffers() *ItemBuffers {
	return &ItemBuffers{
		Reasoning: map[string]string{},
		AgentText: map[string]string{},
	}
}

// DispatchStartedItem maps the item.started variants we care about
// (tool_call surfaces) into proto envelopes. The function never returns
// terminal envelopes; per Codex, only item/completed + turn/completed
// can finish a turn.
//
// item types we silence by intent:
//
//   - reasoning / agentMessage / userMessage: text is streamed via deltas
//     and re-stamped in item/completed
//   - error: surfaced by turn/completed.status="failed" instead
func DispatchStartedItem(runID string, item ThreadItem) ([]proto.Envelope, error) {
	switch item.Type {
	case "commandExecution":
		return wrapToolCall(runID, item.ID, "Bash", map[string]any{
			"command": item.Command,
			"cwd":     item.Cwd,
		})
	case "fileChange":
		return wrapToolCall(runID, item.ID, "Edit", map[string]any{
			"changes": item.Changes,
		})
	case "mcpToolCall":
		name := fmt.Sprintf("mcp__%s__%s", item.Server, item.Tool)
		return wrapToolCall(runID, item.ID, name, argMap(item.Arguments))
	case "dynamicToolCall":
		// dynamicToolCall has no server; tool name is namespaced via
		// item.Namespace when present.
		name := item.Tool
		if item.Namespace != "" {
			name = item.Namespace + "::" + item.Tool
		}
		return wrapToolCall(runID, item.ID, name, argMap(item.Arguments))
	case "webSearch":
		return wrapToolCall(runID, item.ID, "WebSearch", map[string]any{
			"query": item.Query,
		})
	}
	return nil, nil
}

// DispatchCompletedItem folds an item.completed payload into envelopes.
// Reasoning and agentMessage produce a final TypeThinking / TypeDelta
// (delta+sequence=0) so the buffer drained by upstream deltas can be
// flushed; tool-call variants produce an "after" envelope so the UI
// gets a stage transition.
//
// emitFinalDelta=true asks the dispatch to emit a synthetic full-text
// TypeDelta whose Sequence will be set by the caller using a session-
// level monotonic counter; this is only needed when no deltas were
// observed (item.completed arrived without any item/agentMessage/delta).
//
// Returns the agent text body for agentMessage items so the session can
// stamp it into DonePayload.Content. Empty string for everything else.
func DispatchCompletedItem(runID string, item ThreadItem, bufs *ItemBuffers) (envelopes []proto.Envelope, agentText string, err error) {
	switch item.Type {
	case "reasoning":
		body := bufs.Reasoning[item.ID]
		delete(bufs.Reasoning, item.ID)
		if body == "" {
			body = fallbackReasoningText(item)
		}
		if body == "" {
			return nil, "", nil
		}
		env, err := proto.NewEnvelope(proto.TypeThinking, runID, proto.ThinkingPayload{Text: body})
		if err != nil {
			return nil, "", fmt.Errorf("codex: thinking envelope: %w", err)
		}
		return []proto.Envelope{env}, "", nil
	case "agentMessage":
		body := bufs.AgentText[item.ID]
		delete(bufs.AgentText, item.ID)
		if body == "" {
			body = item.Text
		}
		return nil, body, nil
	case "commandExecution", "fileChange", "mcpToolCall", "dynamicToolCall", "webSearch":
		// Surface a tool_call "after" so the UI flips status.
		name := toolNameForItem(item)
		result := map[string]any{}
		if item.ExitCode != nil {
			result["exit_code"] = *item.ExitCode
		}
		if item.Status != "" {
			result["status"] = item.Status
		}
		envs, err := wrapToolCallStage(runID, item.ID, name, nil, result, "after")
		if err != nil {
			return nil, "", err
		}
		return envs, "", nil
	}
	return nil, "", nil
}

// FoldDeltaIntoBuffer accumulates a delta string under the matching
// itemId. Returns the running prefix so the caller can emit a
// progressive TypeDelta / TypeThinking envelope for streaming UI.
func FoldDeltaIntoBuffer(bufs *ItemBuffers, kind, itemID, delta string) string {
	if delta == "" || itemID == "" {
		return ""
	}
	switch kind {
	case "agent":
		bufs.AgentText[itemID] += delta
		return bufs.AgentText[itemID]
	case "reasoning":
		bufs.Reasoning[itemID] += delta
		return bufs.Reasoning[itemID]
	}
	return ""
}

func toolNameForItem(item ThreadItem) string {
	switch item.Type {
	case "commandExecution":
		return "Bash"
	case "fileChange":
		return "Edit"
	case "mcpToolCall":
		return fmt.Sprintf("mcp__%s__%s", item.Server, item.Tool)
	case "dynamicToolCall":
		if item.Namespace != "" {
			return item.Namespace + "::" + item.Tool
		}
		return item.Tool
	case "webSearch":
		return "WebSearch"
	}
	return item.Type
}

func wrapToolCall(runID, id, name string, args map[string]any) ([]proto.Envelope, error) {
	return wrapToolCallStage(runID, id, name, args, nil, "before")
}

func wrapToolCallStage(runID, id, name string, args, result map[string]any, stage string) ([]proto.Envelope, error) {
	env, err := proto.NewEnvelope(proto.TypeToolCall, runID, proto.ToolCallPayload{
		ID:     id,
		Name:   name,
		Stage:  stage,
		Args:   args,
		Result: result,
	})
	if err != nil {
		return nil, fmt.Errorf("codex: tool_call envelope: %w", err)
	}
	return []proto.Envelope{env}, nil
}

// argMap normalises ThreadItem.Arguments — which arrives as
// any (typically map[string]any decoded from JSON) — into a
// map suitable for ToolCallPayload.Args. Non-object args are wrapped
// under "value" to avoid silently dropping arrays / strings.
func argMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	// Round-trip through JSON for any unexpected shape so the wire
	// payload stays structured. Falls back to {"value": <json>} when
	// it's not an object.
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"value": fmt.Sprintf("%v", v)}
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		return obj
	}
	return map[string]any{"value": json.RawMessage(raw)}
}

func fallbackReasoningText(item ThreadItem) string {
	if item.Text != "" {
		return item.Text
	}
	if item.SummaryText != "" {
		return item.SummaryText
	}
	if len(item.Summary) > 0 {
		var b strings.Builder
		for i, s := range item.Summary {
			if i > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(s)
		}
		return b.String()
	}
	if len(item.Content) > 0 {
		var b strings.Builder
		for i, s := range item.Content {
			if i > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(s)
		}
		return b.String()
	}
	return ""
}
