package items

import (
	"encoding/json"
	"strconv"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

func inputMessages(turn string, sequence int64, raw json.RawMessage) []Update {
	var p struct {
		Text  *string `json:"text"`
		Input []struct {
			Role    string           `json:"role"`
			Content []v1.ItemContent `json:"content"`
		} `json:"input"`
	}
	// Internal admission predates the public schema and accepts arbitrary objects.
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil
	}
	key := "input:" + strconv.FormatInt(sequence, 10)
	if p.Text != nil {
		return []Update{{Item: message(turn, key, "user", *p.Text, "completed")}}
	}
	var updates []Update
	for i, input := range p.Input {
		if input.Role != "user" || len(input.Content) == 0 {
			continue
		}
		valid := true
		for _, c := range input.Content {
			if !((c.Type == "input_text" && c.Text != nil) || (c.Type == "input_image" && c.ImageURL != "")) {
				valid = false
			}
		}
		if !valid {
			continue
		}
		item := v1.Item{ID: Identity(turn, key+":"+strconv.Itoa(i)), TurnID: turn, Type: "message", Status: "completed", Role: "user", Content: input.Content}
		updates = append(updates, Update{Item: item})
	}
	return updates
}
