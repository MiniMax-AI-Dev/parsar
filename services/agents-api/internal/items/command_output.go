package items

import (
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func projectCommandOutput(turn string, raw json.RawMessage) ([]Update, error) {
	var p proto.CommandOutputPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if p.ID == "" || p.Delta == "" {
		return nil, errors.New("invalid command output observation")
	}
	return []Update{{Item: v1.Item{ID: Identity(turn, "tool:"+p.ID), TurnID: turn, Type: "command_execution"}, CommandOutputDelta: &p.Delta}}, nil
}

func mergeCommandOutput(update Update, previous v1.Item) (v1.Item, error) {
	if previous.ID != update.Item.ID || previous.TurnID != update.Item.TurnID || previous.Type != "command_execution" {
		return v1.Item{}, errors.New("command output requires its existing command")
	}
	if previous.Status != "in_progress" {
		return previous, nil
	}
	var output string
	if previous.Output != nil {
		if err := json.Unmarshal(encoded(previous.Output), &output); err != nil {
			return v1.Item{}, err
		}
	}
	previous.Output = output + *update.CommandOutputDelta
	return previous, nil
}
