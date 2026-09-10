package claudecode

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// resultUsage is split out so we can decode usage even when the
// success/error branches differ.
type resultUsage struct {
	InputTokens              int32 `json:"input_tokens"`
	OutputTokens             int32 `json:"output_tokens"`
	CacheCreationInputTokens int32 `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int32 `json:"cache_read_input_tokens,omitempty"`
}

func (t *translator) translateResult(line []byte, subtype string) (translation, error) {
	defer clear(t.partialBlocks)

	var msg struct {
		IsError      bool        `json:"is_error"`
		Result       string      `json:"result"`
		Error        string      `json:"error"`
		Errors       []string    `json:"errors"`
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
		errMsg := strings.TrimSpace(msg.Error)
		// Claude Code sometimes reports provider/API failures with
		// subtype="success" and is_error=true, placing the useful error in
		// result instead of error. Preserve that message rather than emitting
		// the misleading fallback "claude_code: success".
		if errMsg == "" && msg.IsError {
			errMsg = strings.TrimSpace(msg.Result)
		}
		if errMsg == "" {
			var details []string
			for _, detail := range msg.Errors {
				if detail = strings.TrimSpace(detail); detail != "" {
					details = append(details, detail)
				}
			}
			errMsg = strings.Join(details, "\n")
		}
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
