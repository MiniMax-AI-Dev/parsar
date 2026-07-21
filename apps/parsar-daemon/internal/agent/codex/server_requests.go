package codex

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type pendingCodexPermission struct {
	rpcID any
	timer *time.Timer
}

type pendingCodexAsk struct {
	rpcID       any
	questionIDs []string
	answerKeys  []string
	timer       *time.Timer
}

const codexInteractionTimeout = 10 * time.Minute

type pendingCodexInteractions struct {
	mu          sync.Mutex
	permissions map[string]pendingCodexPermission
	asks        map[string]pendingCodexAsk
}

func newPendingCodexInteractions() *pendingCodexInteractions {
	return &pendingCodexInteractions{
		permissions: make(map[string]pendingCodexPermission),
		asks:        make(map[string]pendingCodexAsk),
	}
}

func codexInteractionID(prefix string) string {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return prefix + "_" + hex.EncodeToString(bytes[:])
	}
	return fmt.Sprintf("%s_fallback", prefix)
}

func (s *Session) handleCodexCommandApproval(raw json.RawMessage, rpcID any) (any, error) {
	var params CommandExecutionRequestApprovalParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("decode command approval: %w", err)
	}
	title := stringPointer(params.Command)
	if title == "" {
		title = "Run command"
	}
	return s.deferCodexPermission(rpcID, "command_execution", title, stringPointer(params.Reason), raw)
}

func (s *Session) handleCodexFileApproval(raw json.RawMessage, rpcID any) (any, error) {
	var params FileChangeRequestApprovalParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("decode file approval: %w", err)
	}
	title := stringPointer(params.GrantRoot)
	if title == "" {
		title = "Apply file changes"
	}
	return s.deferCodexPermission(rpcID, "file_change", title, stringPointer(params.Reason), raw)
}

func (s *Session) handleCodexPermissionsApproval(raw json.RawMessage, rpcID any) (any, error) {
	var params PermissionsRequestApprovalParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("decode permissions approval: %w", err)
	}
	title := strings.TrimSpace(params.Cwd)
	if title == "" {
		title = "Grant additional permissions"
	}
	return s.deferCodexPermission(rpcID, "permission_request", title, stringPointer(params.Reason), raw)
}

func (s *Session) deferCodexPermission(rpcID any, tool, title, detail string, raw json.RawMessage) (any, error) {
	requestID := codexInteractionID("perm")
	payload := map[string]any{}
	_ = json.Unmarshal(raw, &payload)
	timer := time.AfterFunc(codexInteractionTimeout, func() { s.expireCodexPermission(requestID) })
	s.interactions.mu.Lock()
	s.interactions.permissions[requestID] = pendingCodexPermission{rpcID: rpcID, timer: timer}
	s.interactions.mu.Unlock()
	env, err := proto.NewEnvelope(proto.TypePermissionRequest, requestID, proto.PermissionRequestPayload{
		Tool: tool, Title: title, Detail: detail, Payload: payload,
	})
	if err != nil {
		return nil, err
	}
	s.trySend(env)
	return DeferReply, nil
}

func (s *Session) handleCodexUserInput(raw json.RawMessage, rpcID any) (any, error) {
	var params ToolRequestUserInputParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("decode requestUserInput: %w", err)
	}
	if len(params.Questions) == 0 {
		return nil, errors.New("requestUserInput contains no questions")
	}
	askID := codexInteractionID("ask")
	questions := make([]proto.PromptForUserChoiceQuestion, 0, len(params.Questions))
	questionIDs := make([]string, 0, len(params.Questions))
	answerKeys := make([]string, 0, len(params.Questions))
	for index, question := range params.Questions {
		options := make([]proto.PromptForUserChoiceOption, 0, len(question.Options))
		for _, option := range question.Options {
			options = append(options, proto.PromptForUserChoiceOption{Label: option.Label, Description: option.Description})
		}
		header := strings.TrimSpace(question.Header)
		if header == "" {
			header = fmt.Sprintf("q%d", index)
		}
		questionID := strings.TrimSpace(question.ID)
		if questionID == "" {
			questionID = header
		}
		questionIDs = append(questionIDs, questionID)
		answerKeys = append(answerKeys, header)
		questions = append(questions, proto.PromptForUserChoiceQuestion{
			ID: questionID, Header: header, Question: question.Question, Options: options,
		})
	}
	s.interactions.mu.Lock()
	timer := time.AfterFunc(codexInteractionTimeout, func() { s.expireCodexAsk(askID) })
	s.interactions.asks[askID] = pendingCodexAsk{rpcID: rpcID, questionIDs: questionIDs, answerKeys: answerKeys, timer: timer}
	s.interactions.mu.Unlock()
	env, err := proto.NewEnvelope(proto.TypePromptForUserChoice, s.runID, proto.PromptForUserChoicePayload{
		AskID: askID, Questions: questions,
	})
	if err != nil {
		return nil, err
	}
	s.trySend(env)
	return DeferReply, nil
}

func (s *Session) submitCodexPermission(requestID string, decision proto.PermissionDecisionPayload) error {
	s.interactions.mu.Lock()
	pending, ok := s.interactions.permissions[requestID]
	if ok {
		delete(s.interactions.permissions, requestID)
	}
	s.interactions.mu.Unlock()
	if !ok {
		return agent.ErrUnknownPermission
	}
	if pending.timer != nil {
		pending.timer.Stop()
	}
	value := "decline"
	if decision.Approved {
		value = "accept"
	}
	if err := s.rpc.SendServerReply(pending.rpcID, ApprovalDecisionResult{Decision: value}); err != nil {
		pending.timer = time.AfterFunc(codexInteractionTimeout, func() { s.expireCodexPermission(requestID) })
		s.interactions.mu.Lock()
		s.interactions.permissions[requestID] = pending
		s.interactions.mu.Unlock()
		return err
	}
	return nil
}

func (s *Session) submitCodexUserInput(askID string, decision proto.PromptForUserChoiceDecisionPayload) error {
	s.interactions.mu.Lock()
	pending, ok := s.interactions.asks[askID]
	if ok {
		delete(s.interactions.asks, askID)
	}
	s.interactions.mu.Unlock()
	if !ok {
		return agent.ErrUnknownAsk
	}
	if pending.timer != nil {
		pending.timer.Stop()
	}
	if decision.Cancelled {
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			reason = "user cancelled input request"
		}
		if err := s.rpc.SendServerError(pending.rpcID, -32001, reason, nil); err != nil {
			pending.timer = time.AfterFunc(codexInteractionTimeout, func() { s.expireCodexAsk(askID) })
			s.interactions.mu.Lock()
			s.interactions.asks[askID] = pending
			s.interactions.mu.Unlock()
			return err
		}
		return nil
	}
	byID := make(map[string][]string, len(decision.QuestionAnswers))
	byHeader := make(map[string][]string, len(decision.QuestionAnswers))
	for _, answer := range decision.QuestionAnswers {
		values := answer.Answers
		if len(values) == 0 {
			values = splitCodexAnswers(answer.Answer)
		}
		if answer.QuestionID != "" {
			byID[answer.QuestionID] = values
		}
		if answer.Header != "" {
			byHeader[answer.Header] = values
		}
	}
	result := ToolRequestUserInputResponse{Answers: make(map[string]ToolRequestUserInputAnswer, len(pending.questionIDs))}
	for index, questionID := range pending.questionIDs {
		values := byID[questionID]
		if len(values) == 0 && index < len(pending.answerKeys) {
			values = byHeader[pending.answerKeys[index]]
		}
		if len(values) == 0 && index < len(decision.QuestionAnswers) {
			values = decision.QuestionAnswers[index].Answers
			if len(values) == 0 {
				values = splitCodexAnswers(decision.QuestionAnswers[index].Answer)
			}
		}
		if len(values) == 0 && index == 0 && len(decision.Answers) > 0 {
			values = decision.Answers
		}
		result.Answers[questionID] = ToolRequestUserInputAnswer{Answers: values}
	}
	if err := s.rpc.SendServerReply(pending.rpcID, result); err != nil {
		pending.timer = time.AfterFunc(codexInteractionTimeout, func() { s.expireCodexAsk(askID) })
		s.interactions.mu.Lock()
		s.interactions.asks[askID] = pending
		s.interactions.mu.Unlock()
		return err
	}
	return nil
}

func (s *Session) expireCodexPermission(requestID string) {
	s.interactions.mu.Lock()
	pending, ok := s.interactions.permissions[requestID]
	if ok {
		delete(s.interactions.permissions, requestID)
	}
	s.interactions.mu.Unlock()
	if ok {
		if pending.timer != nil {
			pending.timer.Stop()
		}
		_ = s.rpc.SendServerReply(pending.rpcID, ApprovalDecisionResult{Decision: "decline"})
	}
}

func (s *Session) expireCodexAsk(askID string) {
	s.interactions.mu.Lock()
	pending, ok := s.interactions.asks[askID]
	if ok {
		delete(s.interactions.asks, askID)
	}
	s.interactions.mu.Unlock()
	if ok {
		if pending.timer != nil {
			pending.timer.Stop()
		}
		_ = s.rpc.SendServerError(pending.rpcID, -32001, "input request timed out", nil)
	}
}

func (s *Session) stopCodexInteractionTimers() {
	s.interactions.mu.Lock()
	defer s.interactions.mu.Unlock()
	for id, pending := range s.interactions.permissions {
		if pending.timer != nil {
			pending.timer.Stop()
		}
		delete(s.interactions.permissions, id)
	}
	for id, pending := range s.interactions.asks {
		if pending.timer != nil {
			pending.timer.Stop()
		}
		delete(s.interactions.asks, id)
	}
}

func splitCodexAnswers(answer string) []string {
	parts := strings.Split(strings.TrimSpace(answer), ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func stringPointer(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
