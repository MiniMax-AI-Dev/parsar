package execution

import (
	"context"
	"encoding/json"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func messageText(raw json.RawMessage) (string, error) {
	var input struct {
		Text  string            `json:"text"`
		Input []v1.InputMessage `json:"input"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return "", store.ErrInvalidInput
	}
	if len(input.Input) == 0 {
		if strings.TrimSpace(input.Text) == "" {
			return "", store.ErrInvalidInput
		}
		return input.Text, nil
	}
	messages := make([]string, 0, len(input.Input))
	for _, message := range input.Input {
		if message.Role != "user" {
			return "", store.ErrInvalidInput
		}
		var text strings.Builder
		for _, content := range message.Content {
			if content.Type != "input_text" {
				return "", store.ErrInvalidInput
			}
			text.WriteString(content.Text)
		}
		if strings.TrimSpace(text.String()) == "" {
			return "", store.ErrInvalidInput
		}
		messages = append(messages, text.String())
	}
	return strings.Join(messages, "\n\n"), nil
}

func (d *Dispatcher) initialInput(ctx context.Context, tenant, session, turn string) (string, int64, error) {
	inputs, err := d.Store.ListTurnInputs(ctx, tenant, session, turn, 0, 100)
	if err != nil {
		return "", 0, err
	}
	var texts []string
	var through int64
	size := 0
	for _, input := range inputs {
		if input.Kind != "message" {
			return "", 0, store.ErrInvalidInput
		}
		text, err := messageText(input.Payload)
		if err != nil {
			return "", 0, err
		}
		if size+len(text) > 512*1024 && len(texts) > 0 {
			break
		}
		texts = append(texts, text)
		size += len(text)
		through = input.Sequence
	}
	if len(texts) == 0 {
		return "", 0, store.ErrInvalidInput
	}
	return strings.Join(texts, "\n\n"), through, nil
}
