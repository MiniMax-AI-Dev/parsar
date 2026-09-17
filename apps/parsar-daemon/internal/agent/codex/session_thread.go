package codex

import (
	"encoding/json"
	"fmt"
)

func (s *Session) startThread(plan SessionPlan) error {
	params := ThreadStartParams{
		Cwd:                   plan.Cwd,
		Environments:          plan.Environments,
		Model:                 plan.Model,
		ModelProvider:         plan.ModelProvider,
		ApprovalPolicy:        plan.ApprovalPolicy,
		Sandbox:               plan.Sandbox,
		Permissions:           plan.Permissions,
		DeveloperInstructions: plan.SystemPrompt,
	}
	if s.functions != nil {
		params.DynamicTools = s.functions.definitions
	}
	s.cfg.logger.Info("codex: thread/start request",
		"run_id", s.runID,
		"cwd", params.Cwd,
		"model", params.Model,
		"model_provider", params.ModelProvider,
		"sandbox", string(params.Sandbox),
		"approval_silent", IsSilent(&params.ApprovalPolicy),
		"developer_instructions_len", len(params.DeveloperInstructions))
	_, err := s.rpc.requestWithResult(s.cancelCtx, "thread/start", params, func(raw json.RawMessage) error {
		return s.bindThreadResult(raw, "")
	})
	return err
}

func (s *Session) resumeThread(threadID string, plan SessionPlan) error {
	params := ThreadResumeParams{
		ThreadID: threadID, ApprovalPolicy: plan.ApprovalPolicy, Sandbox: plan.Sandbox,
		Permissions:           plan.Permissions,
		DeveloperInstructions: plan.SystemPrompt,
	}
	if plan.mcpHTTPServers != nil {
		// Resolve the same project configuration checked before native startup.
		params.Cwd = plan.Cwd
	}
	s.usageMu.Lock()
	s.resumeUsageThreadID = threadID
	s.usageMu.Unlock()
	defer func() {
		s.usageMu.Lock()
		s.resumeUsageThreadID, s.resumeUsageTotal = "", nil
		s.usageMu.Unlock()
	}()
	_, err := s.rpc.requestWithResult(s.cancelCtx, "thread/resume", params, func(raw json.RawMessage) error {
		if err := s.bindThreadResult(raw, threadID); err != nil {
			return err
		}
		s.usageMu.Lock()
		if s.resumeUsageTotal != nil {
			s.usageTotal = *s.resumeUsageTotal
		}
		s.resumeUsageThreadID, s.resumeUsageTotal = "", nil
		s.usageMu.Unlock()
		return nil
	})
	return err
}

func (s *Session) bindThreadResult(raw json.RawMessage, expectedID string) error {
	var res ThreadStartResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("codex: decode thread response: %w", err)
	}
	if res.Thread.ID == "" || (expectedID != "" && res.Thread.ID != expectedID) {
		return fmt.Errorf("codex: thread response has missing or mismatched identity")
	}
	if threadID := s.currentThreadID(); threadID != "" && threadID != res.Thread.ID {
		return fmt.Errorf("codex: thread response would replace the root identity")
	}
	s.setThreadID(res.Thread.ID)
	if res.Model != "" {
		s.resolvedModel = res.Model
	}
	return nil
}
