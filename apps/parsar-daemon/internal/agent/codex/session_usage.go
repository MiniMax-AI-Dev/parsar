package codex

import (
	"encoding/json"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (s *Session) beginUsageTurn(raw json.RawMessage) {
	var p TurnStartedNotification
	if json.Unmarshal(raw, &p) != nil || p.Turn.ID == "" {
		return
	}
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if s.usageTurnID == p.Turn.ID {
		return
	}
	s.usageTurnID = p.Turn.ID
	s.usageBaseline = s.usageTotal
	s.latestUsage = nil
}

func (s *Session) onUsageUpdated(raw json.RawMessage) {
	var p ThreadTokenUsageUpdatedNotification
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	if threadID := s.currentThreadID(); threadID != "" && p.ThreadID != threadID {
		return
	}
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	if p.TokenUsage != nil && p.TokenUsage.Total != nil {
		// app-server replays the previous thread total after resume and
		// before turn/started. It establishes a baseline, not new usage.
		if s.usageTurnID == "" {
			s.usageTotal = *p.TokenUsage.Total
			return
		}
		if p.TurnID != s.usageTurnID {
			return
		}
		s.usageTotal = *p.TokenUsage.Total
		u := subtractUsage(s.usageTotal, s.usageBaseline)
		s.latestUsage = &u
		return
	}
	if p.Usage != nil && (p.TurnID == "" || p.TurnID == s.usageTurnID) {
		u := *p.Usage
		s.latestUsage = &u
	}
}

func subtractUsage(total, baseline TurnUsage) TurnUsage {
	return TurnUsage{
		observed: true,
		complete: total.complete && (!baseline.observed || baseline.complete) &&
			total.InputTokens >= baseline.InputTokens && total.OutputTokens >= baseline.OutputTokens &&
			total.CachedInputTokens >= baseline.CachedInputTokens &&
			total.ReasoningOutputTokens >= baseline.ReasoningOutputTokens && total.TotalTokens >= baseline.TotalTokens,
		ReasoningOutputTokens: max(0, total.ReasoningOutputTokens-baseline.ReasoningOutputTokens),
		InputTokens:           max(0, total.InputTokens-baseline.InputTokens),
		OutputTokens:          max(0, total.OutputTokens-baseline.OutputTokens),
		CachedInputTokens:     max(0, total.CachedInputTokens-baseline.CachedInputTokens),
		CacheReadInputTokens:  max(0, total.CacheReadInputTokens-baseline.CacheReadInputTokens),
		TotalTokens:           max(0, total.TotalTokens-baseline.TotalTokens),
	}
}

func (u *TurnUsage) UnmarshalJSON(raw []byte) error {
	type plain TurnUsage
	var value plain
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	*u = TurnUsage(value)
	u.observed, u.complete = true, true
	for _, key := range []string{"inputTokens", "outputTokens", "cachedInputTokens", "reasoningOutputTokens", "totalTokens"} {
		var n *int64
		if json.Unmarshal(fields[key], &n) != nil || n == nil || *n < 0 {
			u.complete = false
		}
	}
	return nil
}

func (s *Session) usagePayload(u TurnUsage) proto.Usage {
	result := proto.Usage{Provider: "openai", Model: s.resolvedModel, InputTokens: int32(u.InputTokens), OutputTokens: int32(u.OutputTokens)}
	if u.complete {
		result.Tokens = &proto.TokenUsage{InputTokens: int64(u.InputTokens), OutputTokens: int64(u.OutputTokens), CachedInputTokens: int64(u.CachedInputTokens), ReasoningOutputTokens: int64(u.ReasoningOutputTokens), TotalTokens: int64(u.TotalTokens)}
	}
	return result
}
