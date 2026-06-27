package pi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// translator converts one NDJSON line from pi's `--mode json` stdout
// into zero or more proto.Envelope frames. One translator lives per
// session. pi has no explicit terminal frame: the stream ends at process
// EOF, so the session pump calls terminalEnvelopes after cmd.Wait.
type translator struct {
	runID string
	seq   atomic.Uint64

	deltaBuf  strings.Builder
	finalText string
	rawLines  []string
	usage     proto.Usage
	usageSet  bool
	sessionID string
}

type translation struct {
	Envelopes []proto.Envelope
	// SessionID is surfaced from the session header line so session.go
	// can write it into binding metadata for --session resume.
	SessionID string
}

func newTranslator(runID string) *translator { return &translator{runID: runID} }

func (t *translator) Translate(line []byte) (translation, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return translation{}, nil
	}
	t.rawLines = append(t.rawLines, string(line))

	var head struct {
		Type string `json:"type"`
	}
	// pi emits strict JSON per line; a non-JSON line is a stray log, not
	// a fatal error — skip it to keep one bad line from killing the run.
	if err := json.Unmarshal(line, &head); err != nil || head.Type == "" {
		return translation{}, nil
	}

	switch head.Type {
	case "session":
		return t.translateSessionHeader(line)
	case "message_update":
		return t.translateMessageUpdate(line)
	case "tool_execution_start":
		return t.translateToolStart(line)
	case "tool_execution_end":
		return t.translateToolEnd(line)
	case "message_end":
		return t.translateMessageEnd(line)
	default:
		return translation{}, nil
	}
}

func (t *translator) translateSessionHeader(line []byte) (translation, error) {
	var msg struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(line, &msg)
	if msg.ID != "" {
		t.sessionID = msg.ID
	}
	return translation{SessionID: msg.ID}, nil
}

func (t *translator) translateMessageUpdate(line []byte) (translation, error) {
	var msg struct {
		Event struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
		} `json:"assistantMessageEvent"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return translation{}, fmt.Errorf("pi: parse message_update: %w", err)
	}
	switch msg.Event.Type {
	case "text_delta":
		if msg.Event.Delta == "" {
			return translation{}, nil
		}
		t.deltaBuf.WriteString(msg.Event.Delta)
		env, err := proto.NewEnvelope(proto.TypeDelta, t.runID, proto.DeltaPayload{
			Delta:    msg.Event.Delta,
			Sequence: t.seq.Add(1),
		})
		if err != nil {
			return translation{}, err
		}
		return translation{Envelopes: []proto.Envelope{env}}, nil
	case "thinking_delta":
		if msg.Event.Delta == "" {
			return translation{}, nil
		}
		env, err := proto.NewEnvelope(proto.TypeThinking, t.runID, proto.ThinkingPayload{
			Text:     msg.Event.Delta,
			Sequence: t.seq.Add(1),
		})
		if err != nil {
			return translation{}, err
		}
		return translation{Envelopes: []proto.Envelope{env}}, nil
	default:
		return translation{}, nil
	}
}

func (t *translator) translateToolStart(line []byte) (translation, error) {
	var msg struct {
		ToolCallID string         `json:"toolCallId"`
		ToolName   string         `json:"toolName"`
		Args       map[string]any `json:"args"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return translation{}, fmt.Errorf("pi: parse tool_execution_start: %w", err)
	}
	env, err := proto.NewEnvelope(proto.TypeToolCall, t.runID, proto.ToolCallPayload{
		ID:    msg.ToolCallID,
		Name:  msg.ToolName,
		Stage: "before",
		Args:  msg.Args,
	})
	if err != nil {
		return translation{}, err
	}
	return translation{Envelopes: []proto.Envelope{env}}, nil
}

func (t *translator) translateToolEnd(line []byte) (translation, error) {
	var msg struct {
		ToolCallID string `json:"toolCallId"`
		ToolName   string `json:"toolName"`
		Result     any    `json:"result"`
		IsError    bool   `json:"isError"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return translation{}, fmt.Errorf("pi: parse tool_execution_end: %w", err)
	}
	env, err := proto.NewEnvelope(proto.TypeToolCall, t.runID, proto.ToolCallPayload{
		ID:    msg.ToolCallID,
		Name:  msg.ToolName,
		Stage: "after",
		Result: map[string]any{
			"content":  msg.Result,
			"is_error": msg.IsError,
		},
	})
	if err != nil {
		return translation{}, err
	}
	return translation{Envelopes: []proto.Envelope{env}}, nil
}

type piUsage struct {
	Input        int32 `json:"input"`
	Output       int32 `json:"output"`
	CacheRead    int32 `json:"cacheRead"`
	CacheWrite   int32 `json:"cacheWrite"`
	CacheWrite1h int32 `json:"cacheWrite1h"`
	Reasoning    int32 `json:"reasoning"`
	TotalTokens  int32 `json:"totalTokens"`
	Cost         struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

func (t *translator) translateMessageEnd(line []byte) (translation, error) {
	var msg struct {
		Message struct {
			Role         string `json:"role"`
			Content      []json.RawMessage
			Provider     string  `json:"provider"`
			Model        string  `json:"model"`
			Usage        piUsage `json:"usage"`
			StopReason   string  `json:"stopReason"`
			ErrorMessage string  `json:"errorMessage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return translation{}, fmt.Errorf("pi: parse message_end: %w", err)
	}
	if msg.Message.Role != "assistant" {
		return translation{}, nil
	}

	t.mergeUsage(msg.Message.Usage, msg.Message.Provider, msg.Message.Model)
	if text := extractText(msg.Message.Content); text != "" {
		t.finalText = text
	}

	if msg.Message.StopReason == "error" {
		errMsg := msg.Message.ErrorMessage
		if errMsg == "" {
			errMsg = "pi: assistant message ended with error"
		}
		env, err := proto.NewEnvelope(proto.TypeError, t.runID, proto.ErrorPayload{Error: errMsg})
		if err != nil {
			return translation{}, err
		}
		return translation{Envelopes: []proto.Envelope{env}}, nil
	}
	return translation{}, nil
}

func extractText(content []json.RawMessage) string {
	var b strings.Builder
	for _, raw := range content {
		var item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		if item.Type == "text" {
			b.WriteString(item.Text)
		}
	}
	return b.String()
}

func (t *translator) mergeUsage(u piUsage, provider, model string) {
	t.usageSet = true
	t.usage.InputTokens += u.Input
	t.usage.OutputTokens += u.Output
	t.usage.CostUSD += u.Cost.Total
	if provider != "" {
		t.usage.Provider = provider
	}
	if model != "" {
		t.usage.Model = model
	}
	if u.CacheRead != 0 || u.CacheWrite != 0 || u.CacheWrite1h != 0 || u.Reasoning != 0 || u.TotalTokens != 0 {
		if t.usage.Raw == nil {
			t.usage.Raw = map[string]any{}
		}
		if u.CacheRead != 0 {
			t.usage.Raw["cache_read_tokens"] = u.CacheRead
		}
		if u.CacheWrite != 0 {
			t.usage.Raw["cache_write_tokens"] = u.CacheWrite
		}
		if u.CacheWrite1h != 0 {
			t.usage.Raw["cache_write_1h_tokens"] = u.CacheWrite1h
		}
		if u.Reasoning != 0 {
			t.usage.Raw["reasoning_tokens"] = u.Reasoning
		}
		if u.TotalTokens != 0 {
			t.usage.Raw["total_tokens"] = u.TotalTokens
		}
	}
}

func (t *translator) terminalEnvelopes(waitErr error, stderr string, cancelled bool) []proto.Envelope {
	var envs []proto.Envelope

	content := strings.TrimSpace(t.deltaBuf.String())
	if content == "" && t.finalText != "" {
		content = strings.TrimSpace(t.finalText)
		if content != "" {
			if env, err := proto.NewEnvelope(proto.TypeDelta, t.runID, proto.DeltaPayload{Delta: content, Sequence: t.seq.Add(1)}); err == nil {
				envs = append(envs, env)
			}
		}
	}

	usage := t.usage
	if usage.Provider == "" {
		usage.Provider = "pi"
	}
	if t.usageSet {
		if env, err := proto.NewEnvelope(proto.TypeUsage, t.runID, proto.UsagePayload{Usage: usage}); err == nil {
			envs = append(envs, env)
		}
	}

	if waitErr != nil || cancelled {
		msg := "pi: subprocess exited without success"
		if waitErr != nil {
			msg = fmt.Sprintf("pi: subprocess exited: %v", waitErr)
		}
		if strings.TrimSpace(stderr) != "" {
			msg += ": " + truncate(strings.TrimSpace(stderr), 400)
		}
		if cancelled {
			msg = "pi: cancelled"
		}
		if env, err := proto.NewEnvelope(proto.TypeError, t.runID, proto.ErrorPayload{Error: msg}); err == nil {
			envs = append(envs, env)
		}
	}

	metadata := map[string]any{"connector_path": "pi_print"}
	if t.sessionID != "" {
		metadata["pi_session_id"] = t.sessionID
		// claude_session_id is the canonical key the server's resume
		// write-back reads (binder MetaClaudeSessionID); mirror pi's id
		// into it so --session resume works, exactly as codex does.
		metadata["claude_session_id"] = t.sessionID
	}
	if env, err := proto.NewEnvelope(proto.TypeDone, t.runID, proto.DonePayload{
		Content:    content,
		Transcript: strings.Join(t.rawLines, "\n"),
		Usage:      usage,
		Metadata:   metadata,
	}); err == nil {
		envs = append(envs, env)
	}
	return envs
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
