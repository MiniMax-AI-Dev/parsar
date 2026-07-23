package codex

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	stdstrconv "strconv"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type pendingCodexPermission struct {
	rpcID       any
	kind        codexPermissionKind
	permissions map[string]any
	timeout     time.Duration
	timer       *time.Timer
}

type pendingCodexAsk struct {
	rpcID       any
	questionIDs []string
	answerKeys  []string
	kind        codexAskKind
	mcpFields   []mcpElicitationField
	timeout     time.Duration
	timer       *time.Timer
}

type codexAskKind uint8

const (
	codexAskUserInput codexAskKind = iota
	codexAskMCPElicitationForm
	codexAskMCPElicitationURL
)

type mcpElicitationField struct {
	ID          string
	Type        string
	MultiSelect bool
	OptionValue map[string]string
}

const codexInteractionTimeout = 10 * time.Minute

type codexPermissionKind uint8

const (
	codexDecisionApproval codexPermissionKind = iota
	codexPermissionsApproval
)

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
	return s.deferCodexPermission(rpcID, codexDecisionApproval, "command_execution", title, stringPointer(params.Reason), raw, nil)
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
	return s.deferCodexPermission(rpcID, codexDecisionApproval, "file_change", title, stringPointer(params.Reason), raw, nil)
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
	return s.deferCodexPermission(rpcID, codexPermissionsApproval, "permission_request", title, stringPointer(params.Reason), raw, params.Permissions)
}

func (s *Session) deferCodexPermission(rpcID any, kind codexPermissionKind, tool, title, detail string, raw json.RawMessage, permissions map[string]any) (any, error) {
	requestID := codexInteractionID("perm")
	payload := map[string]any{}
	_ = json.Unmarshal(raw, &payload)
	pending := pendingCodexPermission{
		rpcID: rpcID, kind: kind, permissions: permissions,
		timeout: codexInteractionTimeout,
	}
	s.interactions.mu.Lock()
	// Start the timer while holding the table lock. Even a future very short
	// timeout then blocks in expireCodexPermission until the entry is visible,
	// rather than firing before insertion and leaving an immortal request.
	pending.timer = time.AfterFunc(pending.timeout, func() { s.expireCodexPermission(requestID) })
	s.interactions.permissions[requestID] = pending
	s.interactions.mu.Unlock()
	env, err := proto.NewEnvelope(proto.TypePermissionRequest, s.runID, proto.PermissionRequestPayload{
		RequestID: requestID, Tool: tool, Title: title, Detail: detail, Payload: payload,
	})
	if err != nil {
		s.interactions.mu.Lock()
		pending, ok := s.interactions.permissions[requestID]
		if ok {
			delete(s.interactions.permissions, requestID)
		}
		s.interactions.mu.Unlock()
		if ok && pending.timer != nil {
			pending.timer.Stop()
		}
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
			IsOther: question.IsOther, IsSecret: question.IsSecret,
		})
	}
	timeout := codexInteractionTimeout
	if params.AutoResolutionMs != nil && *params.AutoResolutionMs > 0 {
		const maxMillis = uint64(^uint64(0)>>1) / uint64(time.Millisecond)
		if *params.AutoResolutionMs <= maxMillis {
			timeout = time.Duration(*params.AutoResolutionMs) * time.Millisecond
		}
	}
	pending := pendingCodexAsk{rpcID: rpcID, questionIDs: questionIDs, answerKeys: answerKeys, kind: codexAskUserInput, timeout: timeout}
	return s.deferCodexAsk(pending, questions, params.AutoResolutionMs)
}

func (s *Session) handleCodexMCPElicitation(raw json.RawMessage, rpcID any) (any, error) {
	var params MCPServerElicitationRequestParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("decode MCP elicitation: %w", err)
	}
	questions, fields, kind, err := mcpElicitationQuestions(params)
	if err != nil {
		return nil, err
	}
	questionIDs := make([]string, 0, len(questions))
	answerKeys := make([]string, 0, len(questions))
	for _, question := range questions {
		questionIDs = append(questionIDs, question.ID)
		answerKeys = append(answerKeys, question.Header)
	}
	pending := pendingCodexAsk{
		rpcID: rpcID, questionIDs: questionIDs, answerKeys: answerKeys,
		kind: kind, mcpFields: fields, timeout: codexInteractionTimeout,
	}
	return s.deferCodexAsk(pending, questions, nil)
}

func (s *Session) deferCodexAsk(pending pendingCodexAsk, questions []proto.PromptForUserChoiceQuestion, autoResolutionMs *uint64) (any, error) {
	askID := codexInteractionID("ask")
	s.interactions.mu.Lock()
	pending.timer = time.AfterFunc(pending.timeout, func() { s.expireCodexAsk(askID) })
	s.interactions.asks[askID] = pending
	s.interactions.mu.Unlock()
	env, err := proto.NewEnvelope(proto.TypePromptForUserChoice, s.runID, proto.PromptForUserChoicePayload{
		AskID: askID, Questions: questions, AutoResolutionMs: autoResolutionMs,
	})
	if err != nil {
		s.interactions.mu.Lock()
		pending, ok := s.interactions.asks[askID]
		if ok {
			delete(s.interactions.asks, askID)
		}
		s.interactions.mu.Unlock()
		if ok && pending.timer != nil {
			pending.timer.Stop()
		}
		return nil, err
	}
	s.trySend(env)
	return DeferReply, nil
}

func mcpElicitationQuestions(params MCPServerElicitationRequestParams) ([]proto.PromptForUserChoiceQuestion, []mcpElicitationField, codexAskKind, error) {
	serverName := strings.TrimSpace(params.ServerName)
	if serverName == "" {
		serverName = "MCP server"
	}
	message := strings.TrimSpace(params.Message)
	if message == "" {
		message = serverName + " needs additional input."
	}
	if params.Mode == "url" {
		url := strings.TrimSpace(params.URL)
		if url == "" {
			return nil, nil, 0, errors.New("MCP URL elicitation is missing url")
		}
		return []proto.PromptForUserChoiceQuestion{{
			ID: "continue", Header: serverName, Question: message,
			Options: []proto.PromptForUserChoiceOption{{Label: "Continue", Description: url}},
		}}, []mcpElicitationField{{ID: "continue", Type: "url"}}, codexAskMCPElicitationURL, nil
	}
	if params.Mode != "form" && params.Mode != "openai/form" {
		return nil, nil, 0, fmt.Errorf("unsupported MCP elicitation mode %q", params.Mode)
	}

	properties, ok := params.RequestedSchema["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		return []proto.PromptForUserChoiceQuestion{{
			ID: "confirm", Header: serverName, Question: message,
			Options: []proto.PromptForUserChoiceOption{{Label: "Continue"}},
		}}, []mcpElicitationField{{ID: "confirm", Type: "confirm"}}, codexAskMCPElicitationForm, nil
	}
	required := stringSet(params.RequestedSchema["required"])
	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	questions := make([]proto.PromptForUserChoiceQuestion, 0, len(keys))
	fields := make([]mcpElicitationField, 0, len(keys))
	for _, key := range keys {
		property, ok := properties[key].(map[string]any)
		if !ok {
			return nil, nil, 0, fmt.Errorf("MCP elicitation property %q must be an object", key)
		}
		question, field, err := mcpElicitationQuestion(key, property, message, required[key])
		if err != nil {
			return nil, nil, 0, err
		}
		questions = append(questions, question)
		fields = append(fields, field)
	}
	return questions, fields, codexAskMCPElicitationForm, nil
}

func mcpElicitationQuestion(id string, property map[string]any, fallback string, required bool) (proto.PromptForUserChoiceQuestion, mcpElicitationField, error) {
	typeName, _ := property["type"].(string)
	title, _ := property["title"].(string)
	if strings.TrimSpace(title) == "" {
		title = id
	}
	description, _ := property["description"].(string)
	if strings.TrimSpace(description) == "" {
		description = fallback
	}
	question := proto.PromptForUserChoiceQuestion{ID: id, Header: title, Question: description}
	field := mcpElicitationField{ID: id, Type: typeName, OptionValue: map[string]string{}}

	options, multiSelect := mcpElicitationOptions(property)
	if len(options) > 0 {
		question.Options = make([]proto.PromptForUserChoiceOption, 0, len(options)+1)
		question.MultiSelect = multiSelect
		field.MultiSelect = multiSelect
		for _, option := range options {
			question.Options = append(question.Options, proto.PromptForUserChoiceOption{Label: option.label, Description: option.description})
			field.OptionValue[option.label] = option.value
		}
	} else {
		switch typeName {
		case "boolean":
			question.Options = []proto.PromptForUserChoiceOption{{Label: "Yes"}, {Label: "No"}}
			field.OptionValue["Yes"] = "true"
			field.OptionValue["No"] = "false"
		case "string", "number", "integer":
			question.IsOther = true
		case "array":
			return proto.PromptForUserChoiceQuestion{}, mcpElicitationField{}, fmt.Errorf("MCP elicitation property %q array must declare enum items", id)
		default:
			return proto.PromptForUserChoiceQuestion{}, mcpElicitationField{}, fmt.Errorf("MCP elicitation property %q has unsupported type %q", id, typeName)
		}
	}
	if !required {
		question.Options = append(question.Options, proto.PromptForUserChoiceOption{Label: "Skip"})
		field.OptionValue["Skip"] = ""
	}
	return question, field, nil
}

type mcpElicitationOption struct {
	label       string
	value       string
	description string
}

func mcpElicitationOptions(property map[string]any) ([]mcpElicitationOption, bool) {
	if raw, ok := property["enum"].([]any); ok {
		names, _ := property["enumNames"].([]any)
		return stringOptions(raw, names), false
	}
	if raw, ok := property["oneOf"].([]any); ok {
		return constOptions(raw), false
	}
	items, _ := property["items"].(map[string]any)
	if raw, ok := items["enum"].([]any); ok {
		return stringOptions(raw, nil), true
	}
	if raw, ok := items["anyOf"].([]any); ok {
		return constOptions(raw), true
	}
	if raw, ok := items["oneOf"].([]any); ok {
		return constOptions(raw), true
	}
	return nil, false
}

func stringOptions(values, names []any) []mcpElicitationOption {
	options := make([]mcpElicitationOption, 0, len(values))
	for index, raw := range values {
		value, ok := raw.(string)
		if !ok {
			continue
		}
		label := value
		if index < len(names) {
			if named, ok := names[index].(string); ok && strings.TrimSpace(named) != "" {
				label = named
			}
		}
		description := ""
		if label != value {
			description = value
		}
		options = append(options, mcpElicitationOption{label: label, value: value, description: description})
	}
	return options
}

func constOptions(values []any) []mcpElicitationOption {
	options := make([]mcpElicitationOption, 0, len(values))
	for _, raw := range values {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		value, _ := entry["const"].(string)
		if value == "" {
			continue
		}
		label, _ := entry["title"].(string)
		if strings.TrimSpace(label) == "" {
			label = value
		}
		description := ""
		if label != value {
			description = value
		}
		options = append(options, mcpElicitationOption{label: label, value: value, description: description})
	}
	return options
}

func stringSet(raw any) map[string]bool {
	result := map[string]bool{}
	values, _ := raw.([]any)
	for _, value := range values {
		if text, ok := value.(string); ok {
			result[text] = true
		}
	}
	return result
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
	if err := s.sendCodexPermissionReply(pending, decision.Approved); err != nil {
		s.interactions.mu.Lock()
		pending.timer = time.AfterFunc(pending.timeout, func() { s.expireCodexPermission(requestID) })
		s.interactions.permissions[requestID] = pending
		s.interactions.mu.Unlock()
		return err
	}
	return nil
}

func (s *Session) sendCodexPermissionReply(pending pendingCodexPermission, approved bool) error {
	if pending.kind == codexPermissionsApproval {
		permissions := map[string]any{}
		if approved && pending.permissions != nil {
			permissions = pending.permissions
		}
		return s.rpc.SendServerReply(pending.rpcID, PermissionsRequestApprovalResponse{
			Permissions: permissions,
			Scope:       "turn",
		})
	}
	value := "decline"
	if approved {
		value = "accept"
	}
	return s.rpc.SendServerReply(pending.rpcID, ApprovalDecisionResult{Decision: value})
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
	if pending.kind == codexAskMCPElicitationForm || pending.kind == codexAskMCPElicitationURL {
		response, err := mcpElicitationResponse(pending, decision)
		if err != nil {
			s.restoreCodexAsk(askID, pending)
			return err
		}
		if err := s.rpc.SendServerReply(pending.rpcID, response); err != nil {
			s.restoreCodexAsk(askID, pending)
			return err
		}
		return nil
	}
	if decision.Cancelled {
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			reason = "user cancelled input request"
		}
		if err := s.rpc.SendServerError(pending.rpcID, -32001, reason, nil); err != nil {
			s.interactions.mu.Lock()
			pending.timer = time.AfterFunc(pending.timeout, func() { s.expireCodexAsk(askID) })
			s.interactions.asks[askID] = pending
			s.interactions.mu.Unlock()
			return err
		}
		return nil
	}
	answers := codexAskAnswers(pending, decision)
	result := ToolRequestUserInputResponse{Answers: make(map[string]ToolRequestUserInputAnswer, len(pending.questionIDs))}
	for index, questionID := range pending.questionIDs {
		result.Answers[questionID] = ToolRequestUserInputAnswer{Answers: answers[index]}
	}
	if err := s.rpc.SendServerReply(pending.rpcID, result); err != nil {
		s.interactions.mu.Lock()
		pending.timer = time.AfterFunc(pending.timeout, func() { s.expireCodexAsk(askID) })
		s.interactions.asks[askID] = pending
		s.interactions.mu.Unlock()
		return err
	}
	return nil
}

func mcpElicitationResponse(pending pendingCodexAsk, decision proto.PromptForUserChoiceDecisionPayload) (MCPServerElicitationResponse, error) {
	if decision.Cancelled {
		return MCPServerElicitationResponse{Action: "cancel"}, nil
	}
	if pending.kind == codexAskMCPElicitationURL {
		return MCPServerElicitationResponse{Action: "accept"}, nil
	}
	answers := codexAskAnswers(pending, decision)
	content := make(map[string]any, len(pending.mcpFields))
	for index, field := range pending.mcpFields {
		if index >= len(answers) || len(answers[index]) == 0 {
			continue
		}
		values := answers[index]
		resolved := make([]string, 0, len(values))
		for _, value := range values {
			if mapped, ok := field.OptionValue[value]; ok {
				value = mapped
			}
			if value != "" {
				resolved = append(resolved, value)
			}
		}
		if len(resolved) == 0 {
			continue
		}
		switch field.Type {
		case "boolean":
			value, err := stdstrconv.ParseBool(resolved[0])
			if err != nil {
				return MCPServerElicitationResponse{}, fmt.Errorf("MCP elicitation %q requires true or false", field.ID)
			}
			content[field.ID] = value
		case "integer":
			value, err := stdstrconv.ParseInt(resolved[0], 10, 64)
			if err != nil {
				return MCPServerElicitationResponse{}, fmt.Errorf("MCP elicitation %q requires an integer", field.ID)
			}
			content[field.ID] = value
		case "number":
			value, err := stdstrconv.ParseFloat(resolved[0], 64)
			if err != nil {
				return MCPServerElicitationResponse{}, fmt.Errorf("MCP elicitation %q requires a number", field.ID)
			}
			content[field.ID] = value
		case "array":
			content[field.ID] = resolved
		default:
			content[field.ID] = resolved[0]
		}
	}
	return MCPServerElicitationResponse{Action: "accept", Content: content}, nil
}

func codexAskAnswers(pending pendingCodexAsk, decision proto.PromptForUserChoiceDecisionPayload) [][]string {
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
	result := make([][]string, len(pending.questionIDs))
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
		result[index] = values
	}
	return result
}

func (s *Session) restoreCodexAsk(askID string, pending pendingCodexAsk) {
	s.interactions.mu.Lock()
	pending.timer = time.AfterFunc(pending.timeout, func() { s.expireCodexAsk(askID) })
	s.interactions.asks[askID] = pending
	s.interactions.mu.Unlock()
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
		_ = s.sendCodexPermissionReply(pending, false)
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
		if pending.kind == codexAskMCPElicitationForm || pending.kind == codexAskMCPElicitationURL {
			_ = s.rpc.SendServerReply(pending.rpcID, MCPServerElicitationResponse{Action: "cancel"})
		} else {
			_ = s.rpc.SendServerError(pending.rpcID, -32001, "input request timed out", nil)
		}
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
