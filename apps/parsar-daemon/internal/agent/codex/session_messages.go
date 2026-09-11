package codex

import (
	"encoding/json"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (s *Session) onAgentDelta(raw json.RawMessage) {
	var p AgentMessageDeltaNotification
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	if p.Delta == "" {
		return
	}
	_ = FoldDeltaIntoBuffer(s.bufs, "agent", p.ItemID, p.Delta)
	seq := s.deltaSeq.Add(1)
	payload := proto.DeltaPayload{Delta: p.Delta, Sequence: seq}
	if s.observeMessages {
		payload.ItemID = p.ItemID
	}
	env, err := proto.NewEnvelope(proto.TypeDelta, s.runID, payload)
	if err != nil {
		return
	}
	s.trySend(env)
}

func (s *Session) onItemStarted(raw json.RawMessage) {
	var p ItemStartedNotification
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	s.observeMessage(p.Item, "in_progress", nil)
	envs, err := DispatchStartedItem(s.runID, p.Item)
	if err != nil {
		s.cfg.logger.Warn("codex: dispatch started item failed", "run_id", s.runID, "err", err)
		return
	}
	for _, env := range envs {
		s.trySend(env)
	}
}

func (s *Session) onItemCompleted(raw json.RawMessage) {
	var p ItemCompletedNotification
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	envs, text, err := DispatchCompletedItem(s.runID, p.Item, s.bufs)
	if err != nil {
		s.cfg.logger.Warn("codex: dispatch completed item failed", "run_id", s.runID, "err", err)
		return
	}
	for _, env := range envs {
		s.trySend(env)
	}
	messageText := p.Item.Text
	if messageText == "" {
		messageText = text
	}
	s.observeMessage(p.Item, "completed", &messageText)
	if text != "" {
		s.appendFinalText(text)
	}
}

func (s *Session) observeMessage(item ThreadItem, status string, text *string) {
	if !s.observeMessages || item.Type != "agentMessage" {
		return
	}
	env, err := proto.NewEnvelope(proto.TypeOutputMessage, s.runID, proto.OutputMessagePayload{ID: item.ID, Status: status, Phase: item.Phase, Text: text})
	if err == nil {
		s.trySend(env)
	}
}
