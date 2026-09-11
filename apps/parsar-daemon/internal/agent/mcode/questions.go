package mcode

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

type formOption struct {
	Value       string `json:"const"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type formProperty struct {
	Type        string       `json:"type"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Options     []formOption `json:"oneOf"`
	Items       struct {
		Options []formOption `json:"anyOf"`
	} `json:"items"`
}

type pendingQuestion struct {
	RPCID       json.RawMessage
	Properties  map[string]formProperty
	Required    []string
	OtherFields map[string]string
}

func (s *Session) askQuestion(frame rpcFrame) error {
	var request struct {
		SessionID string `json:"sessionId"`
		Mode      string `json:"mode"`
		Schema    struct {
			Type       string                  `json:"type"`
			Properties map[string]formProperty `json:"properties"`
			Required   []string                `json:"required"`
		} `json:"requestedSchema"`
	}
	if err := json.Unmarshal(frame.Params, &request); err != nil {
		return fmt.Errorf("mcode: invalid input request")
	}
	if request.SessionID != s.sessionID {
		return fmt.Errorf("mcode: input request belongs to another session")
	}
	if request.Mode != "form" || request.Schema.Type != "object" || len(request.Schema.Properties) == 0 {
		return s.write(rpcFrame{JSONRPC: "2.0", ID: frame.ID, Error: &rpcError{Code: -32602, Message: "Parsar requires a nonempty input form"}})
	}
	otherFields := questionnaireOtherFields(request.Schema.Properties)
	for _, key := range otherFields {
		delete(request.Schema.Properties, key)
	}
	keys := make([]string, 0, len(request.Schema.Properties))
	for key := range request.Schema.Properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	questions := make([]proto.PromptForUserChoiceQuestion, 0, len(keys))
	for _, key := range keys {
		property := request.Schema.Properties[key]
		if property.Type != "string" && property.Type != "array" {
			return s.write(rpcFrame{JSONRPC: "2.0", ID: frame.ID, Error: &rpcError{Code: -32602, Message: "Parsar supports text and choice input fields"}})
		}
		question := proto.PromptForUserChoiceQuestion{ID: key, Question: property.Title, MultiSelect: property.Type == "array", Options: []proto.PromptForUserChoiceOption{}}
		if question.Question == "" {
			question.Question = key
		}
		if property.Description != "" {
			question.Question += "\n" + property.Description
		}
		options := property.Options
		if question.MultiSelect {
			options = property.Items.Options
		}
		labels := map[string]bool{}
		for _, option := range options {
			label := option.Title
			if label == "" {
				label = option.Value
			}
			if labels[label] {
				return fmt.Errorf("mcode: input choices have ambiguous labels")
			}
			labels[label] = true
			question.Options = append(question.Options, proto.PromptForUserChoiceOption{Label: label, Description: option.Description})
		}
		question.IsOther = len(options) == 0 || otherFields[key] != ""
		questions = append(questions, question)
	}
	id := "ask_" + uuid.NewString()
	s.mu.Lock()
	s.questions[id] = pendingQuestion{RPCID: frame.ID, Properties: request.Schema.Properties, Required: request.Schema.Required, OtherFields: otherFields}
	s.mu.Unlock()
	s.emit(proto.TypePromptForUserChoice, proto.PromptForUserChoicePayload{AskID: id, Questions: questions})
	return nil
}

// Native questionnaires encode Other as a companion field, not a second question.
func questionnaireOtherFields(properties map[string]formProperty) map[string]string {
	fields := map[string]string{}
	for key, property := range properties {
		if len(property.Options) == 0 && len(property.Items.Options) == 0 {
			continue
		}
		for candidate, other := range properties {
			if strings.HasPrefix(candidate, key+"__other") && strings.Trim(strings.TrimPrefix(candidate, key+"__other"), "_") == "" && other.Type == "string" && len(other.Options) == 0 && other.Title == property.Title+" — Other" {
				fields[key] = candidate
				break
			}
		}
	}
	return fields
}

func (s *Session) SubmitPromptForUserChoice(_ context.Context, id string, decision proto.PromptForUserChoiceDecisionPayload) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.questions[id]
	if !ok {
		return agent.ErrUnknownAsk
	}
	response := map[string]any{"action": "cancel"}
	if !decision.Cancelled {
		content, err := questionContent(pending, decision)
		if err != nil {
			return err
		}
		response = map[string]any{"action": "accept", "content": content}
	}
	raw, _ := json.Marshal(response)
	if err := s.write(rpcFrame{JSONRPC: "2.0", ID: pending.RPCID, Result: raw}); err != nil {
		return err
	}
	delete(s.questions, id)
	return nil
}

func questionContent(pending pendingQuestion, decision proto.PromptForUserChoiceDecisionPayload) (map[string]any, error) {
	answers := decision.QuestionAnswers
	if len(answers) == 0 && len(pending.Properties) == 1 {
		for key := range pending.Properties {
			answers = []proto.PromptForUserChoiceQuestionAnswer{{QuestionID: key, Answers: decision.Answers}}
		}
	}
	content := map[string]any{}
	for _, answer := range answers {
		property, ok := pending.Properties[answer.QuestionID]
		if !ok {
			return nil, fmt.Errorf("mcode: unknown input field")
		}
		values := append([]string{}, answer.Answers...)
		if len(values) == 0 && answer.Answer != "" {
			values = []string{answer.Answer}
		}
		if len(values) == 0 {
			continue
		}
		options := property.Options
		if property.Type == "array" {
			options = property.Items.Options
		} else if len(values) != 1 {
			return nil, fmt.Errorf("mcode: input field requires a single answer")
		}
		selected := make([]string, 0, len(values))
		for _, value := range values {
			if len(options) == 0 {
				selected = append(selected, value)
				continue
			}
			found := false
			for _, option := range options {
				label := option.Title
				if label == "" {
					label = option.Value
				}
				if value == label {
					selected = append(selected, option.Value)
					found = true
					break
				}
			}
			if !found {
				other := pending.OtherFields[answer.QuestionID]
				if other == "" {
					return nil, fmt.Errorf("mcode: answer is not an offered choice")
				}
				if _, exists := content[other]; exists {
					return nil, fmt.Errorf("mcode: input field requires a single custom answer")
				}
				content[other] = value
			}
		}
		if len(selected) == 0 {
			continue
		}
		if property.Type == "array" {
			content[answer.QuestionID] = selected
		} else {
			content[answer.QuestionID] = selected[0]
		}
	}
	for _, key := range pending.Required {
		if _, ok := content[key]; !ok {
			return nil, fmt.Errorf("mcode: required input is missing")
		}
	}
	return content, nil
}
