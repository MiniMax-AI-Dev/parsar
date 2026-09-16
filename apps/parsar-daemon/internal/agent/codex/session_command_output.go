package codex

import (
	"encoding/json"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (s *Session) onCommandOutput(raw json.RawMessage) {
	if !s.observeToolObservations {
		return
	}
	var p AgentMessageDeltaNotification
	if json.Unmarshal(raw, &p) != nil || !s.isRootTurn(p.ThreadID, p.TurnID) || p.ItemID == "" || p.Delta == "" {
		return
	}
	env, err := proto.NewEnvelope(proto.TypeCommandOutput, s.runID, proto.CommandOutputPayload{ID: p.ItemID, Delta: p.Delta})
	if err == nil {
		s.trySend(env)
	}
}
