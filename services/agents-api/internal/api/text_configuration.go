package api

import (
	"errors"
	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

func resolveText(input *v1.TextConfigInput) (v1.TextConfig, error) {
	text := v1.TextConfig{Format: v1.TextFormat{Type: "text"}, Verbosity: "medium"}
	if input == nil {
		return text, nil
	}
	if input.Format != nil && input.Format.Type != "text" {
		return text, errors.New("This service currently supports text.format.type=text only.")
	}
	if input.Verbosity != nil {
		switch *input.Verbosity {
		case "low", "medium", "high":
			text.Verbosity = *input.Verbosity
		default:
			return text, errors.New("text.verbosity must be low, medium or high.")
		}
	}
	return text, nil
}
