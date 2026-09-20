package dev

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
)

// writeSSEEvent serialises a PromptEvent as one SSE event. Returns an
// error if the writer is gone (client disconnected).
func writeSSEEvent(w http.ResponseWriter, ev connector.PromptEvent) error {
	payload, err := json.Marshal(newStreamEventWire(ev))
	if err != nil {
		// Should never happen with the typed PromptEvent struct; if
		// it does, surface as an error event.
		payload = []byte(fmt.Sprintf(`{"type":"error","error":%q}`, "internal: failed to encode event: "+err.Error()))
	}
	eventName := wireEventName(ev.Type)
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, payload); err != nil {
		return err
	}
	return nil
}

// wireEventName maps the connector-internal PromptEventType to the SSE
// wire event name. The connector enum keeps its existing values for
// back-compat while the SSE contract uses short names ("tool" / "permission").
func wireEventName(t connector.PromptEventType) string {
	switch t {
	case connector.EventToolCall:
		return "tool"
	case connector.EventPermissionRequest:
		return "permission"
	case connector.EventPromptForUserChoice:
		return "prompt_for_user_choice"
	case "":
		return "message"
	default:
		return string(t)
	}
}

// streamEventWire is the JSON shape we emit on the SSE wire. Field names
// are lower_snake_case so TS consumers don't need to know Go conventions.
type streamEventWire struct {
	wireEvent
	// emittedAt is added by the dev endpoint for client-side latency
	// observation; the connector itself doesn't track it.
	EmittedAt string `json:"emitted_at,omitempty"`
}

type wireEvent struct {
	Type     string             `json:"type"`
	Sequence uint64             `json:"sequence,omitempty"`
	Delta    string             `json:"delta,omitempty"`
	Thinking string             `json:"thinking,omitempty"`
	Error    string             `json:"error,omitempty"`
	Final    *wireFinal         `json:"final,omitempty"`
	Tool     *wireToolCall      `json:"tool,omitempty"`
	Perm     *wirePermReq       `json:"permission,omitempty"`
	Choice   *wireUserChoiceReq `json:"prompt_for_user_choice,omitempty"`
}

type wireFinal struct {
	Content    string         `json:"content,omitempty"`
	Transcript string         `json:"transcript,omitempty"`
	UsageRaw   map[string]any `json:"usage_raw,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// wireToolCall mirrors connector.ToolCallEvent in lower_snake_case.
// We don't embed the connector type directly because its fields lack
// json tags and would serialize as PascalCase.
type wireToolCall struct {
	ID     string         `json:"id,omitempty"`
	Name   string         `json:"name,omitempty"`
	Stage  string         `json:"stage,omitempty"`
	Args   map[string]any `json:"args,omitempty"`
	Result map[string]any `json:"result,omitempty"`
}

// wirePermReq mirrors connector.PermissionRequest in lower_snake_case.
type wirePermReq struct {
	ID           string            `json:"id,omitempty"`
	Tool         string            `json:"tool,omitempty"`
	Title        string            `json:"title,omitempty"`
	Detail       string            `json:"detail,omitempty"`
	Payload      map[string]any    `json:"payload,omitempty"`
	HookDecision *wireHookDecision `json:"hook_decision,omitempty"`
}

// wireHookDecision carries plugin hook auto-decision metadata on the SSE wire.
type wireHookDecision struct {
	Result string `json:"result"` // "deny" or "allow"
	Reason string `json:"reason,omitempty"`
	Plugin string `json:"plugin,omitempty"`
}

type wireUserChoiceOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type wireUserChoiceQuestion struct {
	ID          string                 `json:"id"`
	Header      string                 `json:"header,omitempty"`
	Question    string                 `json:"question"`
	MultiSelect bool                   `json:"multi_select,omitempty"`
	IsOther     bool                   `json:"is_other"`
	IsSecret    bool                   `json:"is_secret"`
	Options     []wireUserChoiceOption `json:"options"`
}

type wireUserChoiceReq struct {
	ID               string                   `json:"id"`
	Questions        []wireUserChoiceQuestion `json:"questions"`
	AutoResolutionMs *uint64                  `json:"auto_resolution_ms,omitempty"`
}

func toWireToolCall(t *connector.ToolCallEvent) *wireToolCall {
	if t == nil {
		return nil
	}
	return &wireToolCall{
		ID:     t.ID,
		Name:   t.Name,
		Stage:  t.Stage,
		Args:   t.Args,
		Result: t.Result,
	}
}

func toWirePermReq(p *connector.PermissionRequest, hookDecision *connector.HookDecisionMeta) *wirePermReq {
	if p == nil {
		return nil
	}
	wire := &wirePermReq{
		ID:      p.ID,
		Tool:    p.Tool,
		Title:   p.Title,
		Detail:  p.Detail,
		Payload: p.Payload,
	}
	if hookDecision != nil {
		wire.HookDecision = &wireHookDecision{
			Result: hookDecision.Result,
			Reason: hookDecision.Reason,
			Plugin: hookDecision.Plugin,
		}
	}
	return wire
}

func toWireUserChoiceReq(request *connector.PromptForUserChoiceRequest) *wireUserChoiceReq {
	if request == nil {
		return nil
	}
	questions := make([]wireUserChoiceQuestion, 0, len(request.EffectiveQuestions()))
	for _, question := range request.EffectiveQuestions() {
		options := make([]wireUserChoiceOption, 0, len(question.Options))
		for _, option := range question.Options {
			options = append(options, wireUserChoiceOption{Label: option.Label, Description: option.Description})
		}
		questions = append(questions, wireUserChoiceQuestion{
			ID: question.ID, Header: question.Header, Question: question.Question,
			MultiSelect: question.MultiSelect, IsOther: question.IsOther,
			IsSecret: question.IsSecret, Options: options,
		})
	}
	return &wireUserChoiceReq{ID: request.ID, Questions: questions, AutoResolutionMs: request.AutoResolutionMs}
}

func newStreamEventWire(ev connector.PromptEvent) streamEventWire {
	w := streamEventWire{
		wireEvent: wireEvent{
			Type:     wireEventName(ev.Type),
			Sequence: ev.Sequence,
			Delta:    ev.Delta,
			Thinking: ev.Thinking,
			Error:    ev.Error,
			Tool:     toWireToolCall(ev.Tool),
			Perm:     toWirePermReq(ev.Permission, ev.HookDecision),
			Choice:   toWireUserChoiceReq(ev.PromptForUserChoice),
		},
		EmittedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if ev.Final != nil {
		w.Final = &wireFinal{
			Content:    ev.Final.Content,
			Transcript: ev.Final.Transcript,
			UsageRaw:   ev.Final.Usage.Raw,
			Metadata:   ev.Final.Metadata,
		}
	}
	return w
}
