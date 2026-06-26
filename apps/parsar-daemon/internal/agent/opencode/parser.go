package opencode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type translator struct {
	runID string
	seq   atomic.Uint64

	deltaBuf strings.Builder
	plainBuf strings.Builder
	rawLines []string
	usage    proto.Usage
}

type translation struct {
	Envelopes []proto.Envelope
}

func newTranslator(runID string) *translator { return &translator{runID: runID} }

func (t *translator) Translate(line []byte) (translation, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return translation{}, nil
	}
	t.rawLines = append(t.rawLines, string(line))

	var head struct {
		Type       string          `json:"type"`
		Properties json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(line, &head); err != nil || head.Type == "" {
		if t.plainBuf.Len() > 0 {
			t.plainBuf.WriteByte('\n')
		}
		t.plainBuf.Write(line)
		return translation{}, nil
	}

	switch head.Type {
	case "message.part.delta":
		return t.translatePartDelta(head.Properties)
	case "message.updated", "message.updated.1":
		t.captureUsage(head.Properties)
		return translation{}, nil
	default:
		t.captureGenericUsage(line)
		return translation{}, nil
	}
}

func (t *translator) translatePartDelta(raw json.RawMessage) (translation, error) {
	var p struct {
		Field string `json:"field"`
		Delta string `json:"delta"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return translation{}, fmt.Errorf("opencode: parse message.part.delta: %w", err)
	}
	if p.Delta == "" || (p.Field != "" && p.Field != "text") {
		return translation{}, nil
	}
	t.deltaBuf.WriteString(p.Delta)
	env, err := proto.NewEnvelope(proto.TypeDelta, t.runID, proto.DeltaPayload{Delta: p.Delta, Sequence: t.seq.Add(1)})
	if err != nil {
		return translation{}, err
	}
	return translation{Envelopes: []proto.Envelope{env}}, nil
}

func (t *translator) captureUsage(raw json.RawMessage) {
	var p struct {
		Info usageInfo `json:"info"`
	}
	if err := json.Unmarshal(raw, &p); err == nil {
		t.mergeUsage(p.Info)
	}
}

func (t *translator) captureGenericUsage(raw json.RawMessage) {
	var p struct {
		Info   usageInfo   `json:"info"`
		Tokens usageTokens `json:"tokens"`
		Cost   float64     `json:"cost"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	t.mergeUsage(p.Info)
	if p.Tokens.Input != 0 || p.Tokens.Output != 0 || p.Cost != 0 {
		t.mergeUsage(usageInfo{Tokens: p.Tokens, Cost: p.Cost})
	}
}

type usageInfo struct {
	Tokens usageTokens `json:"tokens"`
	Cost   float64     `json:"cost"`
}

type usageTokens struct {
	Input      int32 `json:"input"`
	Output     int32 `json:"output"`
	Reasoning  int32 `json:"reasoning"`
	CacheRead  int32 `json:"cacheRead"`
	CacheWrite int32 `json:"cacheWrite"`
	Total      int32 `json:"total"`
}

func (t *translator) mergeUsage(info usageInfo) {
	if info.Tokens.Input != 0 {
		t.usage.InputTokens = info.Tokens.Input
	}
	if info.Tokens.Output != 0 {
		t.usage.OutputTokens = info.Tokens.Output
	}
	if info.Cost != 0 {
		t.usage.CostUSD = info.Cost
	}
	if info.Tokens.Reasoning != 0 || info.Tokens.CacheRead != 0 || info.Tokens.CacheWrite != 0 || info.Tokens.Total != 0 {
		if t.usage.Raw == nil {
			t.usage.Raw = map[string]any{}
		}
		if info.Tokens.Reasoning != 0 {
			t.usage.Raw["reasoning_tokens"] = info.Tokens.Reasoning
		}
		if info.Tokens.CacheRead != 0 {
			t.usage.Raw["cache_read_tokens"] = info.Tokens.CacheRead
		}
		if info.Tokens.CacheWrite != 0 {
			t.usage.Raw["cache_write_tokens"] = info.Tokens.CacheWrite
		}
		if info.Tokens.Total != 0 {
			t.usage.Raw["total_tokens"] = info.Tokens.Total
		}
	}
}

func (t *translator) terminalEnvelopes(waitErr error, stderr string, cancelled bool) []proto.Envelope {
	var envs []proto.Envelope
	if t.plainBuf.Len() > 0 && t.deltaBuf.Len() == 0 {
		delta := strings.TrimSpace(t.plainBuf.String())
		if delta != "" {
			if env, err := proto.NewEnvelope(proto.TypeDelta, t.runID, proto.DeltaPayload{Delta: delta, Sequence: t.seq.Add(1)}); err == nil {
				envs = append(envs, env)
			}
			t.deltaBuf.WriteString(delta)
		}
	}
	usage := t.usage
	usage.Provider = "opencode"
	if usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.CostUSD != 0 || usage.Raw != nil {
		if env, err := proto.NewEnvelope(proto.TypeUsage, t.runID, proto.UsagePayload{Usage: usage}); err == nil {
			envs = append(envs, env)
		}
	}
	if waitErr != nil || cancelled {
		msg := "opencode: subprocess exited without success"
		if waitErr != nil {
			msg = fmt.Sprintf("opencode: subprocess exited: %v", waitErr)
		}
		if strings.TrimSpace(stderr) != "" {
			msg += ": " + truncate(strings.TrimSpace(stderr), 400)
		}
		if cancelled {
			msg = "opencode: cancelled"
		}
		if env, err := proto.NewEnvelope(proto.TypeError, t.runID, proto.ErrorPayload{Error: msg}); err == nil {
			envs = append(envs, env)
		}
	}
	content := strings.TrimSpace(t.deltaBuf.String())
	metadata := map[string]any{"connector_path": "opencode_run"}
	if len(t.rawLines) > 0 {
		metadata["opencode_raw_lines"] = t.rawLines
	}
	if env, err := proto.NewEnvelope(proto.TypeDone, t.runID, proto.DonePayload{Content: content, Transcript: strings.Join(t.rawLines, "\n"), Usage: usage, Metadata: metadata}); err == nil {
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
