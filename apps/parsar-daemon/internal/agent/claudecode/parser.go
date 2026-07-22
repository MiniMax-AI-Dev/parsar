package claudecode

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// pendingRecorder is the slice of pendingTable the parser needs.
// Interface form lets parser_test.go substitute a fake.
type pendingRecorder interface {
	Record(permID, ccRequestID string, input map[string]any)
	LookupByCC(ccRequestID string) (string, bool)
}

// askRecorder is the slice of pendingAskTable the parser needs.
// Mirrors pendingRecorder so tests can substitute a fake. Record covers
// the tool_use path (toolUseID), RecordControl covers the
// control_request path (ccRequestID).
type askRecorder interface {
	Record(askID, toolUseID string, questions []proto.PromptForUserChoiceQuestion)
	RecordControl(askID, ccRequestID string, questions []proto.PromptForUserChoiceQuestion)
}

// permIDMinter generates the daemon-side permission id (perm_<8hex>).
// Replaceable in tests so envelope IDs are deterministic.
type permIDMinter func() string

// askIDMinter generates the daemon-side ask id (ask_<8hex>).
// Replaceable in tests so envelope IDs are deterministic.
type askIDMinter func() string

func defaultPermIDMinter() string {
	var b [4]byte
	// crypto/rand.Read failure would silently collide perm ids across
	// pending requests, causing an approve-on-wrong-tool bug — panic
	// loud so operators see it.
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("claudecode: rand.Read for perm id failed: %v", err))
	}
	return "perm_" + hex.EncodeToString(b[:])
}

func defaultAskIDMinter() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("claudecode: rand.Read for ask id failed: %v", err))
	}
	return "ask_" + hex.EncodeToString(b[:])
}

// translator converts one NDJSON line from claude stdout into zero or
// more proto.Envelope frames. One translator lives per session.
type translator struct {
	runID      string
	pending    pendingRecorder
	askPending askRecorder
	seq        atomic.Uint64
	mint       permIDMinter
	askMint    askIDMinter
}

func newTranslator(runID string, pending pendingRecorder, askPending askRecorder, mint permIDMinter, askMint askIDMinter) *translator {
	if mint == nil {
		mint = defaultPermIDMinter
	}
	if askMint == nil {
		askMint = defaultAskIDMinter
	}
	return &translator{runID: runID, pending: pending, askPending: askPending, mint: mint, askMint: askMint}
}

// translation is the per-line parser output.
type translation struct {
	Envelopes []proto.Envelope
	// Terminal is true when this line was a `result` frame — the
	// session should stop reading stdout after consuming it.
	Terminal bool
	// SessionID is the upstream Claude session id surfaced on a system init.
	// line (and again on the result frame). session.go writes this
	// into binding metadata for --resume.
	SessionID string
}

// rawEnvelope is the minimal shared head every claude stream-json line
// has.
type rawEnvelope struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype,omitempty"`
}

// Translate parses one NDJSON line. Unknown types return an empty
// translation with no error to stay forward-compatible with new claude
// stream variants. Errors are reserved for malformed JSON on frames we
// claim to understand; the session pump treats them as drop-and-log so
// one bad line doesn't kill an otherwise fine run.
func (t *translator) Translate(line []byte) (translation, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return translation{}, nil
	}

	var head rawEnvelope
	if err := json.Unmarshal(line, &head); err != nil {
		return translation{}, fmt.Errorf("claudecode: parse stream-json head: %w", err)
	}

	switch head.Type {
	case "system":
		return t.translateSystem(line)
	case "assistant":
		return t.translateAssistant(line)
	case "user":
		return t.translateUser(line)
	case "control_request":
		return t.translateControlRequest(line)
	case "control_cancel_request":
		return t.translateControlCancel(line)
	case "result":
		return t.translateResult(line, head.Subtype)
	default:
		return translation{}, nil
	}
}

func (t *translator) translateSystem(line []byte) (translation, error) {
	var msg struct {
		SessionID string `json:"session_id"`
	}
	// System lines have variable shape (init / compact / etc); missing
	// session_id is a no-op, not an error.
	_ = json.Unmarshal(line, &msg)
	return translation{SessionID: msg.SessionID}, nil
}

func (t *translator) translateAssistant(line []byte) (translation, error) {
	var msg struct {
		Message struct {
			Content []json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return translation{}, fmt.Errorf("claudecode: parse assistant frame: %w", err)
	}

	var envs []proto.Envelope
	for _, raw := range msg.Message.Content {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			continue
		}
		switch head.Type {
		case "text":
			var item struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(raw, &item); err != nil || item.Text == "" {
				continue
			}
			env, err := proto.NewEnvelope(proto.TypeDelta, t.runID, proto.DeltaPayload{
				Delta:    item.Text,
				Sequence: t.seq.Add(1),
			})
			if err != nil {
				return translation{}, err
			}
			envs = append(envs, env)
		case "thinking":
			var item struct {
				Thinking string `json:"thinking"`
			}
			if err := json.Unmarshal(raw, &item); err != nil || item.Thinking == "" {
				continue
			}
			env, err := proto.NewEnvelope(proto.TypeThinking, t.runID, proto.ThinkingPayload{
				Text:     item.Thinking,
				Sequence: t.seq.Add(1),
			})
			if err != nil {
				return translation{}, err
			}
			envs = append(envs, env)
		case "tool_use":
			var item struct {
				ID    string         `json:"id"`
				Name  string         `json:"name"`
				Input map[string]any `json:"input"`
			}
			if err := json.Unmarshal(raw, &item); err != nil {
				continue
			}
			// Note: AskUserQuestion intentionally NOT intercepted on this
			// path. claude-code under --permission-prompt-tool stdio (our
			// fixed mode) emits the SAME AskUserQuestion call twice — once
			// here as a tool_use, then a few ms later as a control_request
			// "can_use_tool" check. Intercepting both would produce two
			// PromptForUserChoice envelopes / two cards. The control_request
			// path is the one we control end-to-end (claude waits for a
			// matching control_response), so the tool_use copy goes
			// through as a regular TypeToolCall — the UI logs the call,
			// the user still only sees one card from the control_request
			// path. See translateControlRequest below.
			env, err := proto.NewEnvelope(proto.TypeToolCall, t.runID, proto.ToolCallPayload{
				ID:    item.ID,
				Name:  item.Name,
				Stage: "before",
				Args:  item.Input,
			})
			if err != nil {
				return translation{}, err
			}
			envs = append(envs, env)
		}
	}
	return translation{Envelopes: envs}, nil
}

func (t *translator) translateUser(line []byte) (translation, error) {
	var msg struct {
		Message struct {
			Content []json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return translation{}, fmt.Errorf("claudecode: parse user frame: %w", err)
	}

	var envs []proto.Envelope
	for _, raw := range msg.Message.Content {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			continue
		}
		if head.Type != "tool_result" {
			continue
		}
		var item struct {
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		env, err := proto.NewEnvelope(proto.TypeToolCall, t.runID, proto.ToolCallPayload{
			ID:    item.ToolUseID,
			Stage: "after",
			Result: map[string]any{
				"content":  decodeToolResultContent(item.Content),
				"is_error": item.IsError,
			},
		})
		if err != nil {
			return translation{}, err
		}
		envs = append(envs, env)
	}
	return translation{Envelopes: envs}, nil
}

// decodeToolResultContent best-effort decodes claude's tool_result
// content field, declared as `string | ContentBlock[]`. Returned as
// the natural Go shape so downstream consumers don't have to re-parse.
func decodeToolResultContent(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return v
}

func (t *translator) translateControlRequest(line []byte) (translation, error) {
	var msg struct {
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype  string         `json:"subtype"`
			ToolName string         `json:"tool_name"`
			Input    map[string]any `json:"input"`
		} `json:"request"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return translation{}, fmt.Errorf("claudecode: parse control_request: %w", err)
	}
	if msg.RequestID == "" {
		return translation{}, fmt.Errorf("claudecode: control_request missing request_id")
	}

	// AskUserQuestion comes through here too when claude-code runs with
	// --permission-prompt-tool stdio. The SDK wraps it as a "can_use_tool"
	// permission check rather than emitting a normal tool_use frame, so
	// the tool_use-branch interception in translateAssistant never sees
	// it. We re-route it into the ask flow here. The CC request_id is
	// stashed under askID inside ccByAsk so SubmitPromptForUserChoice can
	// write a matching control_response back.
	if msg.Request.ToolName == askUserQuestionToolName {
		if askEnv, ok := t.interceptAskUserQuestionFromControlRequest(msg.RequestID, msg.Request.Input); ok {
			return translation{Envelopes: []proto.Envelope{askEnv}}, nil
		}
		// Fall through to the permission path when interception can't
		// build a valid payload (e.g. questions array missing). claude
		// will at least see SOME response on the control_request channel
		// rather than blocking; the user will get a permission card they
		// can deny.
	}

	permID := t.mint()
	if t.pending != nil {
		t.pending.Record(permID, msg.RequestID, msg.Request.Input)
	}

	title := msg.Request.ToolName
	if title == "" {
		title = "Permission request"
	}
	env, err := proto.NewEnvelope(proto.TypePermissionRequest, t.runID, proto.PermissionRequestPayload{
		RequestID: permID,
		Tool:      msg.Request.ToolName,
		Title:     title,
		Payload:   msg.Request.Input,
	})
	if err != nil {
		return translation{}, err
	}
	return translation{Envelopes: []proto.Envelope{env}}, nil
}

func (t *translator) translateControlCancel(line []byte) (translation, error) {
	var msg struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return translation{}, fmt.Errorf("claudecode: parse control_cancel_request: %w", err)
	}
	if msg.RequestID == "" || t.pending == nil {
		return translation{}, nil
	}
	permID, ok := t.pending.LookupByCC(msg.RequestID)
	if !ok {
		// Approval already came through and the entry was Delete'd —
		// drop silently.
		return translation{}, nil
	}
	env, err := proto.NewEnvelope(proto.TypePermissionCancel, permID, nil)
	if err != nil {
		return translation{}, err
	}
	return translation{Envelopes: []proto.Envelope{env}}, nil
}

// resultUsage is split out so we can decode usage even when the
// success/error branches differ.
type resultUsage struct {
	InputTokens              int32 `json:"input_tokens"`
	OutputTokens             int32 `json:"output_tokens"`
	CacheCreationInputTokens int32 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int32 `json:"cache_read_input_tokens,omitempty"`
}

func (t *translator) translateResult(line []byte, subtype string) (translation, error) {
	var msg struct {
		IsError      bool        `json:"is_error"`
		Result       string      `json:"result"`
		Error        string      `json:"error"`
		SessionID    string      `json:"session_id"`
		TotalCostUSD float64     `json:"total_cost_usd"`
		Usage        resultUsage `json:"usage"`
		// ModelUsage's map KEY is the model slug — the result frame
		// has no top-level `"model"` field. Single-turn chats have
		// exactly one entry; multi-model orchestration would have
		// more, and we take whatever the map iteration hands us first
		// (the renderer's footer keys off a single model anyway).
		ModelUsage map[string]json.RawMessage `json:"modelUsage"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return translation{}, fmt.Errorf("claudecode: parse result frame: %w", err)
	}

	var envs []proto.Envelope

	// Pick the first model the CLI reports under modelUsage. Map
	// iteration order is fine for the 1-model common case; multi-model
	// runs land on whichever wins the iteration.
	model := ""
	for k := range msg.ModelUsage {
		model = k
		break
	}

	usage := proto.Usage{
		Provider:     "claude_code",
		Model:        model,
		InputTokens:  msg.Usage.InputTokens,
		OutputTokens: msg.Usage.OutputTokens,
		CostUSD:      msg.TotalCostUSD,
	}
	if msg.Usage.CacheCreationInputTokens != 0 || msg.Usage.CacheReadInputTokens != 0 {
		usage.Raw = map[string]any{
			"cache_creation_input_tokens": msg.Usage.CacheCreationInputTokens,
			"cache_read_input_tokens":     msg.Usage.CacheReadInputTokens,
		}
	}
	if usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.CostUSD != 0 || usage.Raw != nil {
		usageEnv, err := proto.NewEnvelope(proto.TypeUsage, t.runID, proto.UsagePayload{Usage: usage})
		if err != nil {
			return translation{}, err
		}
		envs = append(envs, usageEnv)
	}

	// Subtype "success" is the only success-shaped result; everything
	// else (error_during_execution, error_max_turns, ...) is a failure.
	isError := msg.IsError || (subtype != "" && subtype != "success" && strings.HasPrefix(subtype, "error"))
	if isError {
		errMsg := msg.Error
		if errMsg == "" {
			if subtype != "" {
				errMsg = "claude_code: " + subtype
			} else {
				errMsg = "claude_code: unspecified error"
			}
		}
		errEnv, err := proto.NewEnvelope(proto.TypeError, t.runID, proto.ErrorPayload{Error: errMsg})
		if err != nil {
			return translation{}, err
		}
		envs = append(envs, errEnv)
	}

	var doneMeta map[string]any
	if strings.TrimSpace(msg.SessionID) != "" {
		doneMeta = map[string]any{
			proto.DoneMetaAgentSessionID:   msg.SessionID,
			proto.DoneMetaAgentSessionType: "claude_session",
		}
	}
	doneEnv, err := proto.NewEnvelope(proto.TypeDone, t.runID, proto.DonePayload{
		Content:  msg.Result,
		Usage:    usage,
		Metadata: doneMeta,
	})
	if err != nil {
		return translation{}, err
	}
	envs = append(envs, doneEnv)

	return translation{Envelopes: envs, Terminal: true, SessionID: msg.SessionID}, nil
}
