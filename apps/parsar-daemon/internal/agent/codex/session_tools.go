package codex

import (
	"encoding/json"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (s *Session) sendItemEvents(events []proto.Envelope, notification json.RawMessage) {
	var native struct {
		Item json.RawMessage `json:"item"`
	}
	if s.observeTools {
		if err := json.Unmarshal(notification, &native); err != nil {
			return
		}
	}
	for _, event := range events {
		if s.observeTools && event.Type == proto.TypeToolCall {
			var tool proto.ToolCallPayload
			if err := event.DecodePayload(&tool); err != nil {
				return
			}
			tool.NativeItem = native.Item
			payload, err := json.Marshal(tool)
			if err != nil {
				return
			}
			event.Payload = payload
		}
		s.trySend(event)
	}
}
