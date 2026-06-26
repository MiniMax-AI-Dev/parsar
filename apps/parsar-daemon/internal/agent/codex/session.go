package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// terminalSendTimeout caps how long the session waits to deliver the
// final done / error envelope on the upstream channel. Matches the
// claudecode + opencode safety net.
const terminalSendTimeout = 2 * time.Second

// sessionConfig is the cross-cutting knob bag — production callers go
// through Factory which uses defaults.
type sessionConfig struct {
	codexBinary string
	logger      *slog.Logger
	killTimeout time.Duration
}

func defaultSessionConfig() sessionConfig {
	return sessionConfig{
		codexBinary: defaultBinary,
		logger:      obslog.Bg(),
		killTimeout: rpcKillTimeout,
	}
}

// Factory implements agent.Factory for agent_kind="codex". Spawns one
// codex app-server child per prompt; the child is torn down when the
// turn completes or the parent context is cancelled.
func Factory(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
	return newSession(ctx, req, out, defaultSessionConfig())
}

// Session implements agent.Session. State lifecycle:
//
//  1. Factory builds it, RPC client is spawned + initialized.
//  2. registerHandlers wires notification + server-request handlers.
//  3. thread/start or thread/resume runs (resume falls back to start).
//  4. turn/start delivers the user prompt; subsequent stream notifications
//     fan out to proto.Envelope via session_items.go.
//  5. turn/completed emits TypeDone + closes out. Cancel can short-cut
//     this by killing the child early.
type Session struct {
	runID string
	cfg   sessionConfig
	out   chan<- proto.Envelope
	rpc   *JSONRPCClient

	cancelCtx context.Context
	cancelFn  context.CancelFunc

	cancelOnce   sync.Once
	closeOutOnce sync.Once
	waitDone     chan struct{}
	cleanup      func()

	threadIDMu sync.Mutex
	threadID   string

	deltaSeq    atomic.Uint64
	thinkingSeq atomic.Uint64

	bufs *ItemBuffers

	usageMu       sync.Mutex
	latestUsage   *TurnUsage
	resolvedModel string

	finalTextMu sync.Mutex
	finalText   string
	lastErrText string
}

var _ agent.Session = (*Session)(nil)

func newSession(parent context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope, cfg sessionConfig) (*Session, error) {
	if out == nil {
		return nil, errors.New("codex: nil out channel")
	}
	if cfg.logger == nil {
		cfg.logger = obslog.Bg()
	}
	if cfg.codexBinary == "" {
		cfg.codexBinary = defaultBinary
	}
	if cfg.killTimeout <= 0 {
		cfg.killTimeout = rpcKillTimeout
	}

	plan, err := BuildSessionPlan(req.RunID, req.WorkDir, req.AgentOptions)
	if err != nil {
		return nil, fmt.Errorf("codex: build session plan: %w", err)
	}

	cancelCtx, cancelFn := context.WithCancel(parent)

	rpcCfg := JSONRPCConfig{
		Binary:          cfg.codexBinary,
		EnableFeatures:  plan.EnableFeatures,
		DisableFeatures: plan.DisableFeatures,
		Cwd:             plan.Cwd,
		Env:             append(os.Environ(), plan.Env...),
		LogTag:          "codex-" + req.RunID,
		Logger:          cfg.logger,
	}
	for _, kv := range plan.ExtraConfig {
		rpcCfg.ExtraArgs = append(rpcCfg.ExtraArgs, "-c", kv[0]+"="+kv[1])
	}

	rpc := NewJSONRPCClient(rpcCfg)

	s := &Session{
		runID:         req.RunID,
		cfg:           cfg,
		out:           out,
		rpc:           rpc,
		cancelCtx:     cancelCtx,
		cancelFn:      cancelFn,
		waitDone:      make(chan struct{}),
		cleanup:       plan.Cleanup,
		bufs:          NewItemBuffers(),
		resolvedModel: plan.Model,
	}
	s.registerHandlers()

	initParams := InitializeParams{
		ClientInfo:   InitializeClientInfo{Name: "parsar-daemon", Version: "0.0.0"},
		Capabilities: &InitializeCapabilities{ExperimentalAPI: true},
	}
	if _, err := rpc.Start(cancelCtx, initParams); err != nil {
		cancelFn()
		plan.Cleanup()
		return nil, fmt.Errorf("codex: rpc start: %w", err)
	}

	// thread/start (or resume) + turn/start happen in the run goroutine
	// so newSession returns quickly; if any of those fail the failure
	// is surfaced as TypeError + TypeDone on out.
	go s.run(plan, req)

	return s, nil
}

func (s *Session) Cancel(_ context.Context) error {
	s.cancelOnce.Do(func() {
		// Best-effort turn/interrupt — codex will translate this into
		// a graceful turn termination. If the RPC is already dead the
		// kill path below cleans up.
		tid := s.currentThreadID()
		if tid != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_, _ = s.rpc.Request(ctx, "turn/interrupt", TurnInterruptParams{ThreadID: tid})
		}
		s.cancelFn()
		_ = s.rpc.Close()
	})
	return nil
}

// SubmitPermission is not wired today: the daemon runs codex under the
// silent granular policy, so codex never raises an approval ServerRequest.
// Returns agent.ErrUnknownPermission so the router knows it's a benign
// pass-through, matching the opencode adapter.
func (s *Session) SubmitPermission(_ context.Context, _ string, _ proto.PermissionDecisionPayload) error {
	return agent.ErrUnknownPermission
}

// SubmitPromptForUserChoice: codex has no AskUserQuestion equivalent
// today, so this is a no-op the dispatcher logs and moves on.
func (s *Session) SubmitPromptForUserChoice(_ context.Context, _ string, _ proto.PromptForUserChoiceDecisionPayload) error {
	return agent.ErrUnknownAsk
}

// ---------------------------------------------------------------------------
// run loop
// ---------------------------------------------------------------------------

func (s *Session) run(plan SessionPlan, req proto.PromptRequestPayload) {
	defer close(s.waitDone)
	defer s.cleanup()
	defer s.closeOut()

	// Resolve thread: resume if possible, else start fresh.
	if strings.TrimSpace(req.ResumeSessionID) != "" {
		if err := s.resumeThread(req.ResumeSessionID); err != nil {
			s.cfg.logger.Warn("codex: thread/resume failed; starting fresh",
				"run_id", s.runID, "thread_id", req.ResumeSessionID, "err", err)
			if err := s.startThread(plan); err != nil {
				s.emitTerminal(fmt.Sprintf("codex: thread/start: %v", err), true)
				return
			}
		}
	} else {
		if err := s.startThread(plan); err != nil {
			s.emitTerminal(fmt.Sprintf("codex: thread/start: %v", err), true)
			return
		}
	}

	// turn/start fires the first prompt. The reply ack is fire-and-forget
	// — streaming events flow via notifications + the eventual
	// turn/completed.
	input := FirstUserInput(req.Prompt)
	if len(input) == 0 {
		s.emitTerminal("codex: empty prompt", true)
		return
	}
	turnCtx, turnCancel := context.WithTimeout(s.cancelCtx, 10*time.Second)
	_, ackErr := s.rpc.Request(turnCtx, "turn/start", TurnStartParams{
		ThreadID: s.currentThreadID(),
		Input:    input,
	})
	turnCancel()
	if ackErr != nil {
		s.cfg.logger.Warn("codex: turn/start ack failed", "run_id", s.runID, "err", ackErr)
		s.emitTerminal(fmt.Sprintf("codex: turn/start: %v", ackErr), true)
		return
	}

	// Block until the RPC child exits or cancellation arrives. The
	// notification handlers will have called s.finalizeTurn by the
	// time we reach here (turn/completed handler emits TypeDone +
	// triggers Close).
	select {
	case <-s.rpc.Done():
	case <-s.cancelCtx.Done():
		_ = s.rpc.Close()
	}
}

func (s *Session) startThread(plan SessionPlan) error {
	params := ThreadStartParams{
		Cwd:                   plan.Cwd,
		Model:                 plan.Model,
		ModelProvider:         plan.ModelProvider,
		ApprovalPolicy:        plan.ApprovalPolicy,
		Sandbox:               plan.Sandbox,
		DeveloperInstructions: plan.SystemPrompt,
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

func (s *Session) resumeThread(threadID string) error {
	raw, err := s.rpc.Request(s.cancelCtx, "thread/resume", ThreadResumeParams{ThreadID: threadID})
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

// ---------------------------------------------------------------------------
// notification handlers
// ---------------------------------------------------------------------------

func (s *Session) registerHandlers() {
	rpc := s.rpc

	rpc.OnNotification("thread/started", s.onThreadStarted)
	rpc.OnNotification("turn/started", s.onTurnStarted)
	rpc.OnNotification("turn/completed", s.onTurnCompleted)
	rpc.OnNotification("turn/failed", s.onTurnFailed)
	rpc.OnNotification("item/started", s.onItemStarted)
	rpc.OnNotification("item/updated", func(_ json.RawMessage) {}) // silenced
	rpc.OnNotification("item/completed", s.onItemCompleted)
	rpc.OnNotification("item/agentMessage/delta", s.onAgentDelta)
	rpc.OnNotification("item/reasoning/textDelta", s.onReasoningDelta)
	rpc.OnNotification("item/reasoning/summaryTextDelta", s.onReasoningDelta)
	rpc.OnNotification("thread/tokenUsage/updated", s.onUsageUpdated)
	rpc.OnNotification("error", s.onErrorNotif)

	// Approval ServerRequests under silent granular policy should not
	// fire; if codex sends one anyway, auto-accept so the turn proceeds
	// rather than dangling. (TODO: surface as PermissionRequest when
	// the agent admin opts surface_approvals on.)
	autoAccept := func(_ json.RawMessage, _ any) (any, error) {
		return ApprovalDecisionResult{Decision: "accept"}, nil
	}
	rpc.OnServerRequest("item/commandExecution/requestApproval", autoAccept)
	rpc.OnServerRequest("item/fileChange/requestApproval", autoAccept)
	rpc.OnServerRequest("item/permissionsRequestApproval", autoAccept)
	// item/tool/requestUserInput (ARC) under silent policy: decline so
	// codex doesn't block waiting for an answer we can't supply.
	rpc.OnServerRequest("item/tool/requestUserInput", func(_ json.RawMessage, _ any) (any, error) {
		return map[string]any{"answers": map[string]any{}}, nil
	})
}

func (s *Session) onThreadStarted(raw json.RawMessage) {
	var p ThreadStartedNotification
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	if p.Thread.ID != "" {
		s.setThreadID(p.Thread.ID)
	}
}

func (s *Session) onTurnStarted(_ json.RawMessage) {
	// Reset per-turn buffers. The session is per-prompt so this is
	// belt-and-suspenders today, but it keeps the buffer semantics
	// honest when codex emits a fresh turn id mid-session.
	s.bufs = NewItemBuffers()
}

func (s *Session) onAgentDelta(raw json.RawMessage) {
	var p AgentMessageDeltaNotification
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	if p.Delta == "" {
		return
	}
	_ = FoldDeltaIntoBuffer(s.bufs, "agent", p.ItemID, p.Delta)
	seq := s.deltaSeq.Add(1)
	env, err := proto.NewEnvelope(proto.TypeDelta, s.runID, proto.DeltaPayload{Delta: p.Delta, Sequence: seq})
	if err != nil {
		return
	}
	s.trySend(env)
}

func (s *Session) onReasoningDelta(raw json.RawMessage) {
	var p AgentMessageDeltaNotification
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	if p.Delta == "" {
		return
	}
	_ = FoldDeltaIntoBuffer(s.bufs, "reasoning", p.ItemID, p.Delta)
	seq := s.thinkingSeq.Add(1)
	env, err := proto.NewEnvelope(proto.TypeThinking, s.runID, proto.ThinkingPayload{Text: p.Delta, Sequence: seq})
	if err != nil {
		return
	}
	s.trySend(env)
}

func (s *Session) onItemStarted(raw json.RawMessage) {
	var p ItemStartedNotification
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	envs, err := DispatchStartedItem(s.runID, p.Item)
	if err != nil {
		s.cfg.logger.Warn("codex: dispatch started item failed", "run_id", s.runID, "err", err)
		return
	}
	for _, env := range envs {
		s.trySend(env)
	}
}

func (s *Session) onItemCompleted(raw json.RawMessage) {
	var p ItemCompletedNotification
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	envs, text, err := DispatchCompletedItem(s.runID, p.Item, s.bufs)
	if err != nil {
		s.cfg.logger.Warn("codex: dispatch completed item failed", "run_id", s.runID, "err", err)
		return
	}
	for _, env := range envs {
		s.trySend(env)
	}
	if text != "" {
		s.appendFinalText(text)
	}
}

func (s *Session) onUsageUpdated(raw json.RawMessage) {
	var p ThreadTokenUsageUpdatedNotification
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	s.usageMu.Lock()
	u := p.Usage
	s.latestUsage = &u
	s.usageMu.Unlock()
}

func (s *Session) onTurnCompleted(raw json.RawMessage) {
	var p TurnCompletedNotification
	if err := json.Unmarshal(raw, &p); err != nil {
		s.cfg.logger.Warn("codex: turn/completed decode error",
			"run_id", s.runID, "err", err, "raw_len", len(raw))
		s.emitTerminal("codex: turn/completed decode error", true)
		return
	}

	usage := p.Turn.Usage
	if usage == nil {
		s.usageMu.Lock()
		usage = s.latestUsage
		s.usageMu.Unlock()
	}
	if usage != nil {
		s.emitUsage(*usage)
	}

	status := strings.ToLower(p.Turn.Status)
	finalText := s.takeFinalText()
	errText := s.takeLastErrText()
	// Always log the turn outcome — operators need this when a prompt
	// "completes" with no agent message (e.g. codex bailed before the
	// model ran because the sandbox mode was misinterpreted as
	// read-only) so the empty body in the upstream Done frame can be
	// correlated with the turn status that produced it.
	s.cfg.logger.Info("codex: turn/completed",
		"run_id", s.runID,
		"turn_id", p.Turn.ID,
		"status", p.Turn.Status,
		"final_text_len", len(finalText),
		"buffered_err_text_len", len(errText),
		"raw_payload", string(raw))
	if status == "failed" {
		// Body precedence on failure:
		//   1. agent's final text (rare on hard failures but exists for
		//      partial completions that still surface a message)
		//   2. codex's turn.error.message — this is where gateway /
		//      provider errors land (e.g. an upstream gateway's "X-Sub-Module is
		//      not allowed for this API key"). Without forwarding it
		//      the upstream connector reports "empty final output" and
		//      operators can't see why a key was rejected.
		//   3. buffered text from the "error" notification stream
		//      (sandbox warnings, late stream packets) — last because
		//      it's noisier than turn.error.
		body := finalText
		if turnErrMsg := turnErrorMessage(p.Turn.Error); turnErrMsg != "" {
			body = appendOnNewline(body, turnErrMsg)
		}
		if errText != "" {
			body = appendOnNewline(body, errText)
		}
		s.emitTerminal(body, true)
		return
	}
	s.emitDone(finalText, usage)
}

// turnErrorMessage extracts a human-readable error string from
// codex's TurnError. Some gateways (an OpenAI-Responses-style proxy
// is one) JSON-encode the upstream error body and stuff it
// into Message verbatim; in that case unwrap one layer so the
// operator sees the inner code/message instead of escaped JSON.
func turnErrorMessage(te *TurnError) string {
	if te == nil {
		return ""
	}
	raw := strings.TrimSpace(te.Message)
	if raw == "" {
		return ""
	}
	// Try to peel off a {"error":{"code","message","type"}} wrapper.
	var inner struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &inner); err == nil && inner.Error.Message != "" {
		if inner.Error.Code != "" {
			return fmt.Sprintf("%s: %s", inner.Error.Code, inner.Error.Message)
		}
		return inner.Error.Message
	}
	return raw
}

func appendOnNewline(base, extra string) string {
	if base == "" {
		return extra
	}
	if extra == "" {
		return base
	}
	return base + "\n\n" + extra
}

func (s *Session) onTurnFailed(raw json.RawMessage) {
	var p TurnCompletedNotification
	_ = json.Unmarshal(raw, &p)
	// turn/failed carries no payload detail today; the actual cause
	// usually arrived earlier on the "error" notification stream and is
	// already buffered in lastErrText. Log both so post-mortems can
	// correlate the failure to whatever upstream codex saw.
	s.cfg.logger.Warn("codex: turn/failed received",
		"run_id", s.runID,
		"turn_id", p.Turn.ID,
		"turn_status", p.Turn.Status,
		"last_err_text_present", s.peekLastErrText() != "")
	s.emitTerminal("codex: turn failed", true)
}

func (s *Session) onErrorNotif(raw json.RawMessage) {
	var p ErrorNotification
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	if p.Message == "" {
		return
	}
	// Always log: codex's `error` notification is the *only* channel that
	// surfaces gateway / model-provider failures (401 on a custom header,
	// 400 from Azure missing api-version, etc.). Buffering it for the
	// eventual turn/completed message body is correct, but without a log
	// the daemon shows 13s of silence then a TypeError that the upstream
	// can't decode.
	s.cfg.logger.Warn("codex: error notification received",
		"run_id", s.runID,
		"thread_id", s.currentThreadID(),
		"message", p.Message)
	s.finalTextMu.Lock()
	s.lastErrText = p.Message
	s.finalTextMu.Unlock()
}

// peekLastErrText reads lastErrText without consuming it, used by
// loggers that want to record "we have a buffered upstream error"
// without racing with the takeLastErrText path that emitDone uses.
func (s *Session) peekLastErrText() string {
	s.finalTextMu.Lock()
	defer s.finalTextMu.Unlock()
	return s.lastErrText
}

// ---------------------------------------------------------------------------
// envelope emit helpers
// ---------------------------------------------------------------------------

func (s *Session) emitDone(content string, usage *TurnUsage) {
	doneMeta := map[string]any{}
	if tid := s.currentThreadID(); tid != "" {
		// Use the agent-neutral session-id key the binder already
		// understands; codex thread_id and claude session_id share
		// "upstream session id" semantics on the binding layer.
		doneMeta["claude_session_id"] = tid
		doneMeta["codex_thread_id"] = tid
	}
	payload := proto.DonePayload{Content: content, Metadata: doneMeta}
	if usage != nil {
		payload.Usage = proto.Usage{
			Provider:     "openai",
			Model:        s.resolvedModel,
			InputTokens:  int32(usage.InputTokens),
			OutputTokens: int32(usage.OutputTokens),
		}
	}
	env, err := proto.NewEnvelope(proto.TypeDone, s.runID, payload)
	if err != nil {
		return
	}
	s.trySend(env)
}

func (s *Session) emitUsage(u TurnUsage) {
	env, err := proto.NewEnvelope(proto.TypeUsage, s.runID, proto.UsagePayload{
		Usage: proto.Usage{
			Provider:     "openai",
			Model:        s.resolvedModel,
			InputTokens:  int32(u.InputTokens),
			OutputTokens: int32(u.OutputTokens),
		},
	})
	if err != nil {
		return
	}
	s.trySend(env)
}

func (s *Session) emitTerminal(message string, asError bool) {
	// Always log: this is the only place the daemon decides "the prompt is
	// over, here's what went wrong (if anything)". Without this, post-
	// mortem requires correlating server-side TypeError frames against
	// daemon timestamps with no message body anywhere.
	if asError {
		s.cfg.logger.Warn("codex: emitting terminal error",
			"run_id", s.runID,
			"thread_id", s.currentThreadID(),
			"message", message)
	} else {
		s.cfg.logger.Info("codex: emitting terminal done",
			"run_id", s.runID,
			"thread_id", s.currentThreadID(),
			"message_len", len(message))
	}
	if asError {
		env, err := proto.NewEnvelope(proto.TypeError, s.runID, proto.ErrorPayload{Error: message})
		if err == nil {
			s.trySend(env)
		}
	}
	doneMeta := map[string]any{}
	if tid := s.currentThreadID(); tid != "" {
		doneMeta["claude_session_id"] = tid
		doneMeta["codex_thread_id"] = tid
	}
	env, err := proto.NewEnvelope(proto.TypeDone, s.runID, proto.DonePayload{
		Content:  message,
		Metadata: doneMeta,
	})
	if err != nil {
		return
	}
	s.trySend(env)
}

func (s *Session) trySend(env proto.Envelope) {
	select {
	case s.out <- env:
	case <-s.cancelCtx.Done():
	case <-time.After(terminalSendTimeout):
		s.cfg.logger.Warn("codex: out send timed out", "type", env.Type, "run_id", s.runID)
	}
}

func (s *Session) closeOut() {
	s.closeOutOnce.Do(func() { close(s.out) })
}

// ---------------------------------------------------------------------------
// small accessors
// ---------------------------------------------------------------------------

func (s *Session) currentThreadID() string {
	s.threadIDMu.Lock()
	defer s.threadIDMu.Unlock()
	return s.threadID
}

func (s *Session) setThreadID(id string) {
	s.threadIDMu.Lock()
	s.threadID = id
	s.threadIDMu.Unlock()
}

func (s *Session) appendFinalText(text string) {
	s.finalTextMu.Lock()
	if s.finalText != "" {
		s.finalText = s.finalText + "\n\n" + text
	} else {
		s.finalText = text
	}
	s.finalTextMu.Unlock()
}

func (s *Session) takeFinalText() string {
	s.finalTextMu.Lock()
	defer s.finalTextMu.Unlock()
	t := s.finalText
	s.finalText = ""
	return t
}

func (s *Session) takeLastErrText() string {
	s.finalTextMu.Lock()
	defer s.finalTextMu.Unlock()
	e := s.lastErrText
	s.lastErrText = ""
	return e
}
