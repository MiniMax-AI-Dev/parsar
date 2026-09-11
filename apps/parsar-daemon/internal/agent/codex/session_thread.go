package codex

import "encoding/json"

func (s *Session) startThread(plan SessionPlan) error {
	params := ThreadStartParams{
		Cwd:                   plan.Cwd,
		Model:                 plan.Model,
		ModelProvider:         plan.ModelProvider,
		ApprovalPolicy:        plan.ApprovalPolicy,
		Sandbox:               plan.Sandbox,
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
	raw, err := s.rpc.Request(s.cancelCtx, "thread/start", params)
	if err != nil {
		return err
	}
	var res ThreadStartResult
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &res)
	}
	if res.Thread.ID != "" {
		s.setThreadID(res.Thread.ID)
	}
	if res.Model != "" {
		s.resolvedModel = res.Model
	}
	return nil
}

func (s *Session) resumeThread(threadID string, plan SessionPlan) error {
	raw, err := s.rpc.Request(s.cancelCtx, "thread/resume", ThreadResumeParams{
		ThreadID: threadID, ApprovalPolicy: plan.ApprovalPolicy, Sandbox: plan.Sandbox,
		DeveloperInstructions: plan.SystemPrompt,
	})
	if err != nil {
		return err
	}
	var res ThreadStartResult
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &res)
	}
	id := res.Thread.ID
	if id == "" {
		id = threadID
	}
	s.setThreadID(id)
	if res.Model != "" {
		s.resolvedModel = res.Model
	}
	return nil
}
