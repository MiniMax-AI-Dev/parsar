package api

import (
	"encoding/base64"
	"encoding/json"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentskill"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func decodeInlineSkills(raw json.RawMessage) ([]store.InlineSkill, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || len(entries) > 50 {
		return nil, store.ErrInvalidInput
	}
	result := make([]store.InlineSkill, 0, len(entries))
	for _, entry := range entries {
		var input struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Source      json.RawMessage `json:"source"`
		}
		if decodeInputObject(entry, &input, "type", "name", "description", "source") != nil || input.Type != "inline" {
			return nil, store.ErrInvalidInput
		}
		var source struct {
			Type      string `json:"type"`
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
		}
		if decodeInputObject(input.Source, &source, "type", "media_type", "data") != nil || source.Type != "base64" || source.MediaType != "application/zip" || len(source.Data) > base64.StdEncoding.EncodedLen(agentskill.MaxArchiveBytes) {
			return nil, store.ErrInvalidInput
		}
		body, err := base64.StdEncoding.Strict().DecodeString(source.Data)
		if err != nil {
			return nil, store.ErrInvalidInput
		}
		result = append(result, store.InlineSkill{Metadata: agentskill.Metadata{Type: input.Type, Name: input.Name, Description: input.Description}, Archive: body})
	}
	return result, store.ValidateInlineSkills(result)
}

func skillResponse(skills []agentskill.Metadata) []json.RawMessage {
	result := make([]json.RawMessage, 0, len(skills))
	for _, skill := range skills {
		raw, _ := json.Marshal(skill)
		result = append(result, raw)
	}
	return result
}

func storedSkills(raw json.RawMessage) ([]json.RawMessage, error) {
	if len(raw) == 0 {
		return []json.RawMessage{}, nil
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || len(entries) > 50 {
		return nil, store.ErrInvalidInput
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		var metadata agentskill.Metadata
		if decodeInputObject(entry, &metadata, "type", "name", "description") != nil || metadata.Type != "inline" || metadata.Name == "" || metadata.Description == "" || seen[metadata.Name] {
			return nil, store.ErrInvalidInput
		}
		seen[metadata.Name] = true
	}
	if entries == nil {
		entries = []json.RawMessage{}
	}
	return entries, nil
}
