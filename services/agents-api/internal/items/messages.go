// Package items projects execution observations to the supported public Item variants.
package items

import (
	"encoding/json"
	"errors"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

type Update struct {
	Item       v1.Item
	AppendText bool
	// A legacy aggregate is used only when no native message identity was recorded.
	LegacyFinal bool
}

func Identity(turn, key string) string {
	return uuid.NewSHA1(uuid.MustParse(turn), []byte(key)).String()
}

func message(turn, key, role, text, status string) v1.Item {
	contentType := "output_text"
	if role == "user" {
		contentType = "input_text"
	}
	return v1.Item{ID: Identity(turn, key), TurnID: turn, Type: "message", Role: role, Status: status,
		Content: []v1.ItemContent{{Type: contentType, Text: &text}}}
}

func Project(turn, kind string, sequence int64, raw json.RawMessage) ([]Update, error) {
	switch kind {
	case "message":
		return inputMessages(turn, sequence, raw), nil
	case proto.TypeDelta:
		var p proto.DeltaPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		key := "message:" + p.ItemID
		if p.ItemID == "" {
			key = "legacy-message"
		}
		return []Update{{Item: message(turn, key, "assistant", p.Delta, "in_progress"), AppendText: true}}, nil
	case proto.TypeOutputMessage:
		var p proto.OutputMessagePayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		if p.ID == "" || (p.Status != "in_progress" && p.Status != "completed") {
			return nil, errors.New("invalid message observation")
		}
		text := ""
		if p.Text != nil {
			text = *p.Text
		}
		item := message(turn, "message:"+p.ID, "assistant", text, p.Status)
		if p.Phase == "commentary" || p.Phase == "final_answer" {
			item.Phase = p.Phase
		}
		return []Update{{Item: item, AppendText: p.Text == nil}}, nil
	case proto.TypeDone:
		var p proto.DonePayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		if p.Content == "" {
			return nil, nil
		}
		return []Update{{Item: message(turn, "legacy-message", "assistant", p.Content, "completed"), LegacyFinal: true}}, nil
	case "cancel_receipt", "execution_completed", "execution_failed", "execution_cancelled":
		var p struct {
			Applied bool               `json:"applied"`
			Outcome *proto.DonePayload `json:"outcome"`
			Done    *proto.DonePayload `json:"done"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		final := p.Done
		if kind == "cancel_receipt" {
			if !p.Applied {
				return nil, nil
			}
			final = p.Outcome
		}
		if final == nil || final.Content == "" {
			return nil, nil
		}
		status := "incomplete"
		if kind == "execution_completed" {
			status = "completed"
		}
		return []Update{{Item: message(turn, "legacy-message", "assistant", final.Content, status), LegacyFinal: true}}, nil
	case proto.TypeToolCall:
		return projectTool(turn, raw)
	default:
		return nil, nil
	}
}

func Merge(update Update, previous v1.Item) v1.Item {
	item := update.Item
	if previous.ID == "" {
		return item
	}
	if previous.Status != "in_progress" && !(update.LegacyFinal && previous.Status == "incomplete") {
		return previous
	}
	if (item.Type == "function_call") && (item.Arguments == nil || string(encoded(item.Arguments)) == "null") {
		item.Arguments = previous.Arguments
	}
	if update.AppendText {
		if previous.Status != "in_progress" {
			return previous
		}
		text := *previous.Content[0].Text + *item.Content[0].Text
		item.Content[0].Text = &text
		if item.Phase == "" {
			item.Phase = previous.Phase
		}
	}
	return item
}
