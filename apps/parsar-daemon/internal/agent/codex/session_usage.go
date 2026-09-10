package codex

import "encoding/json"

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
		InputTokens:          max(0, total.InputTokens-baseline.InputTokens),
		OutputTokens:         max(0, total.OutputTokens-baseline.OutputTokens),
		CachedInputTokens:    max(0, total.CachedInputTokens-baseline.CachedInputTokens),
		CacheReadInputTokens: max(0, total.CacheReadInputTokens-baseline.CacheReadInputTokens),
		TotalTokens:          max(0, total.TotalTokens-baseline.TotalTokens),
	}
}
