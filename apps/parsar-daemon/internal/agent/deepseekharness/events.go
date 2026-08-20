package deepseekharness

import (
	"encoding/json"
	"strings"
)

// The downlink frame shape. Every event arrives as a server-request
// envelope whose method names the frame family; the family Parsar cares
// about is session/event, and its real vocabulary is one level deeper, in
// payload.event.type. Decoding stops at the boundaries this adapter maps
// so an unrecognised dsh event is ignored rather than failing a turn.
const (
	frameMethodSessionEvent      = "session/event"
	frameMethodSessionSubscribed = "session/subscribed"
)

// Durable session event types this adapter maps. dsh logs far more than
// this; the rest carry no Parsar-visible meaning.
const (
	eventTurnStart        = "turn/start"
	eventTurnEnd          = "turn/end"
	eventAssistantChunk   = "assistant/chunk"
	eventAssistantMessage = "assistant/message"
	eventToolCall         = "tool/call"
	eventToolResult       = "tool/result"
	eventApprovalAsked    = "approval/asked"
	eventAgentError       = "agent/error"
)

// Streaming chunk types inside assistant/chunk.
const (
	chunkTextDelta      = "text-delta"
	chunkReasoningDelta = "reasoning-delta"
	chunkUsage          = "usage"
)

// downlinkFrame is the outer envelope on /api/events.mux.
type downlinkFrame struct {
	Type    string          `json:"type"`
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload"`
}

// sessionEventPayload is the session/event body. SessionID is what makes
// the mux usable: one connection carries every session the server holds,
// so a consumer must filter rather than assume.
type sessionEventPayload struct {
	SessionID string `json:"sessionId"`
	Event     struct {
		Type string          `json:"type"`
		Seq  uint64          `json:"seq"`
		Time int64           `json:"time"`
		Data json.RawMessage `json:"data"`
	} `json:"event"`
}

type sessionScopedPayload struct {
	SessionID string `json:"sessionId"`
}

// assistantChunkData is one streamed piece of the assistant's answer.
// Text and reasoning are separate block types on separate indices, so a
// turn interleaves them and the consumer must keep them apart.
type assistantChunkData struct {
	Turn  int `json:"turn"`
	Step  int `json:"step"`
	Chunk struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
		Text  string `json:"text"`
		Usage struct {
			InputTokens     int32 `json:"inputTokens"`
			OutputTokens    int32 `json:"outputTokens"`
			CacheReadTokens int32 `json:"cacheReadTokens"`
		} `json:"usage"`
	} `json:"chunk"`
}

// assistantMessageData is the assembled message for one step. It is the
// authoritative text: the deltas are a preview, and a step that was
// retried upstream can produce a message that does not equal the
// concatenated deltas.
type assistantMessageData struct {
	Turn    int `json:"turn"`
	Step    int `json:"step"`
	Message struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

// toolCallData is a tool invocation. Arguments is a JSON *string*, not an
// object — dsh logs the model's raw argument text so a malformed call is
// still auditable.
type toolCallData struct {
	CallID    string `json:"callId"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// toolResultData is the tool's answer. The call id is nested under the
// message source, not at the top level, so a result can be matched to its
// call without a positional assumption.
type toolResultData struct {
	Message struct {
		Source struct {
			CallID string `json:"callId"`
		} `json:"source"`
		Content []struct {
			Type       string `json:"type"`
			ToolCallID string `json:"toolCallId"`
			IsError    bool   `json:"isError"`
			Content    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"content"`
	} `json:"message"`
}

// turnEndData closes a turn. Reason.Kind is "completed" for a turn that
// finished normally; anything else means the turn stopped early and the
// run should not be reported as a success.
type turnEndData struct {
	Turn   int `json:"turn"`
	Reason struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"reason"`
}

// textFromToolResult flattens a tool result's nested content into one
// string plus its error flag, which is all the Parsar tool_call frame
// carries.
func (d toolResultData) textFromToolResult() (string, bool) {
	var sb strings.Builder
	isError := false
	for _, part := range d.Message.Content {
		if part.Type != "tool-result" {
			continue
		}
		if part.IsError {
			isError = true
		}
		for _, inner := range part.Content {
			if inner.Type != "text" || inner.Text == "" {
				continue
			}
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(inner.Text)
		}
	}
	return sb.String(), isError
}

// assistantText concatenates the message's text blocks. Reasoning blocks
// are excluded: they are surfaced as thinking frames while streaming and
// must not leak into the turn's answer.
func (d assistantMessageData) assistantText() string {
	var sb strings.Builder
	for _, part := range d.Message.Content {
		if part.Type != "text" || part.Text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(part.Text)
	}
	return sb.String()
}

// decodeFrame parses one downlink frame. A frame that is not a
// session/event for the given session yields ok=false, which the caller
// treats as "ignore", not "error": the mux carries other sessions' events
// and other frame families by design.
func decodeFrame(raw []byte, sessionID string) (sessionEventPayload, bool) {
	var frame downlinkFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		return sessionEventPayload{}, false
	}
	if frame.Method != frameMethodSessionEvent {
		return sessionEventPayload{}, false
	}
	var payload sessionEventPayload
	if err := json.Unmarshal(frame.Payload, &payload); err != nil {
		return sessionEventPayload{}, false
	}
	if payload.SessionID != sessionID {
		return sessionEventPayload{}, false
	}
	return payload, true
}

// argsFromJSONString parses a tool call's raw argument text. A model can
// emit invalid JSON, and that must not fail the turn — the raw text is
// preserved under a "raw" key so the operator still sees what was asked.
func argsFromJSONString(arguments string) map[string]any {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
		return parsed
	}
	return map[string]any{"raw": trimmed}
}
