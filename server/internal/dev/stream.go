package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
)

// streamConnectorPrompt is the dev SSE endpoint that exposes a connector's
// StreamPrompt over HTTP. POST a JSON PromptInput; the server opens an SSE
// response and forwards each PromptEvent as one SSE event whose data: field
// is the JSON-encoded event.
//
// This endpoint does NOT manage agent_run state — callers MUST drive
// agent_run lifecycle via the existing dev endpoints.
//
// Event wire format
// -----------------
//
//	event: <type>
//	data: <JSON-encoded PromptEvent>
//
// The event: field mirrors PromptEvent.Type so an EventSource consumer
// can dispatch by event name. EventDone.Final is included so callers
// don't have to re-accumulate deltas.
//
// HTTP behaviour
// --------------
//
//   - 503 if no OpenCode AgentConnector is wired.
//
//   - 400 on body parse / required-field errors.
//
//   - 200 + Content-Type: text/event-stream once streaming starts.
//     Errors AFTER the stream is open are surfaced as an SSE EventError +
//     EventDone pair before the connection closes.
//
//     @Summary		Stream a raw connector prompt (dev SSE)
//     @Description	Dev-only Server-Sent Events endpoint that forwards the OpenCode connector's StreamPrompt output. Does NOT manage agent_run state — callers drive lifecycle via the regular dev endpoints.
//     @Tags			gateway
//     @ID				streamDevConnectorPrompt
//     @Accept			json
//     @Produce		json-stream
//     @Param			body	body		connector.PromptInput	true	"Prompt input (workspace_id, conversation_id, run_id, agent_config required)"
//     @Success		200		"SSE stream opened"
//     @Failure		400		{object}	map[string]string	"Body decode or validation error"
//     @Failure		500		{object}	map[string]string	"ResponseWriter does not support flushing or connector failure"
//     @Failure		501		{object}	map[string]string	"Connector does not support streaming"
//     @Failure		503		{object}	map[string]string	"OpenCode AgentConnector is not registered"
//     @Router			/dev/connectors/opencode/stream [post]
func streamConnectorPrompt(cfg *routerConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg == nil || cfg.openCodeConnector == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "opencode AgentConnector is not registered; pass WithOpenCodeConnector at router construction",
			})
			return
		}
		// Streaming requires HTTP flushing.
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "ResponseWriter does not support flushing; SSE requires http.Flusher",
			})
			return
		}

		var in connector.PromptInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("decode request body: %v", err),
			})
			return
		}
		if err := validatePromptInputForStream(in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		// Wrap r.Context so we can cancel on write-failure mid-stream
		// in addition to the client-disconnect cancel http.Server does.
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		events, err := cfg.openCodeConnector.StreamPrompt(ctx, in)
		if err != nil {
			// Spawn-time errors (missing model, secret, etc.) arrive
			// here synchronously; return as plain JSON before opening
			// the SSE stream so the caller sees the failure without
			// parsing event-stream framing.
			status := http.StatusInternalServerError
			if errors.Is(err, connector.ErrNotSupported) {
				status = http.StatusNotImplemented
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}

		// SSE response from here on. Flush immediately so the client
		// sees the open stream.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		// Disable nginx/upstream buffering.
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		for ev := range events {
			if err := writeSSEEvent(w, ev); err != nil {
				// Client closed; cancel ctx so the upstream stream
				// stops generating tokens.
				cancel()
				// Drain remaining events so the connector goroutine
				// exits cleanly.
				for range events {
				}
				return
			}
			flusher.Flush()
		}
	}
}

// validatePromptInputForStream enforces the required fields up-front so we
// can return a 400 with a precise message instead of letting the connector
// error out mid-stream. The connector re-validates internally too.
func validatePromptInputForStream(in connector.PromptInput) error {
	if strings.TrimSpace(in.WorkspaceID) == "" {
		return fmt.Errorf("workspace_id is required")
	}
	if strings.TrimSpace(in.ConversationID) == "" {
		return fmt.Errorf("conversation_id is required (the inflight tracker keys by it)")
	}
	if strings.TrimSpace(in.RunID) == "" {
		return fmt.Errorf("run_id is required (per-run scratch dir is namespaced by it)")
	}
	if _, ok := stringFromConfig(in.AgentConfig, "model_id"); !ok {
		return fmt.Errorf("agent_config.model_id is required")
	}
	if _, ok := stringFromConfig(in.AgentConfig, "workdir"); !ok {
		return fmt.Errorf("agent_config.workdir is required (must be absolute or start with ~)")
	}
	return nil
}

// stringFromConfig reads a non-empty string field from a config map.
func stringFromConfig(m map[string]any, key string) (string, bool) {
	if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v), true
	}
	return "", false
}

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
	ID      string         `json:"id,omitempty"`
	Tool    string         `json:"tool,omitempty"`
	Title   string         `json:"title,omitempty"`
	Detail  string         `json:"detail,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
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
	Options     []wireUserChoiceOption `json:"options"`
}

type wireUserChoiceReq struct {
	ID        string                   `json:"id"`
	Questions []wireUserChoiceQuestion `json:"questions"`
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

func toWirePermReq(p *connector.PermissionRequest) *wirePermReq {
	if p == nil {
		return nil
	}
	return &wirePermReq{
		ID:      p.ID,
		Tool:    p.Tool,
		Title:   p.Title,
		Detail:  p.Detail,
		Payload: p.Payload,
	}
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
			MultiSelect: question.MultiSelect, Options: options,
		})
	}
	return &wireUserChoiceReq{ID: request.ID, Questions: questions}
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
			Perm:     toWirePermReq(ev.Permission),
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
