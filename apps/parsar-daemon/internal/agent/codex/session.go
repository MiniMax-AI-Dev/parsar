package codex

import (
	"context"
	"encoding/json"
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
	codexBinary       string
	harnessBinary     string
	permissionProfile string
	logger            *slog.Logger
	killTimeout       time.Duration
}

func defaultSessionConfig() sessionConfig {
	return sessionConfig{
		codexBinary:       defaultBinary(),
		harnessBinary:     os.Getenv("PARSAR_CODEX_HARNESS_BIN"),
		permissionProfile: os.Getenv("PARSAR_CODEX_PERMISSION_PROFILE"),
		logger:            obslog.Bg(),
		killTimeout:       rpcKillTimeout,
	}
}

// Factory implements agent.Factory for agent_kind="codex". Spawns one
// codex app-server child per prompt. The run stream closes when the turn
// completes; the router retains the child until the conversation's idle
// window expires or cancellation shuts it down sooner.
func Factory(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
	return newSession(ctx, req, out, defaultSessionConfig())
}

// Session implements agent.Session. State lifecycle:
//
//  1. Preparation initializes RPC and verifies the selected environment.
//  2. Start transfers that RPC and wires notification/server-request handlers.
//  3. thread/start or thread/resume runs (resume falls back to start).
//  4. turn/start delivers the user prompt; subsequent stream notifications
//     fan out to proto.Envelope via session_items.go.
//  5. turn/completed emits TypeDone + closes out. Cancel can short-cut
//     this by killing the child early.
type Session struct {
	subagents                 *subagentObservations
	observeSubagentIdentities bool
	functions                 *functionCalls
	observeMessages           bool
	observeTools              bool
	observeToolObservations   bool
	runID                     string
	cfg                       sessionConfig
	out                       chan<- proto.Envelope
	rpc                       *JSONRPCClient
	harness                   *privateHarness

	cancelCtx context.Context
	cancelFn  context.CancelFunc

	cancelOnce   sync.Once
	cancelled    atomic.Bool
	terminal     atomic.Bool
	closeOutOnce sync.Once
	outMu        sync.RWMutex
	outClosed    bool
	waitDone     chan struct{}
	cleanup      func()

	threadIDMu sync.Mutex
	threadID   string
	steering   steeringTurn

	deltaSeq    atomic.Uint64
	thinkingSeq atomic.Uint64

	bufs *ItemBuffers

	usageMu             sync.Mutex
	latestUsage         *TurnUsage
	usageTotal          TurnUsage
	usageBaseline       TurnUsage
	usageTurnID         string
	resumeUsageThreadID string
	resumeUsageTotal    *TurnUsage
	resolvedModel       string

	finalTextMu sync.Mutex
	finalText   string
	lastErrText string

	interactions *pendingCodexInteractions
	outcome      cancellationOutcomeState
}

var _ agent.Session = (*Session)(nil)

// SubmitPermission completes the deferred Codex app-server request that
// produced the Parsar permission envelope.
func (s *Session) SubmitPermission(_ context.Context, permID string, decision proto.PermissionDecisionPayload) error {
	return s.submitCodexPermission(permID, decision)
}

// SubmitPromptForUserChoice maps Parsar's header/answer pairs back to
// Codex's question-id keyed requestUserInput response.
func (s *Session) SubmitPromptForUserChoice(_ context.Context, askID string, decision proto.PromptForUserChoiceDecisionPayload) error {
	return s.submitCodexUserInput(askID, decision)
}

// ---------------------------------------------------------------------------
// notification handlers
// ---------------------------------------------------------------------------

func (s *Session) registerHandlers() {
	rpc := s.rpc

	rpc.OnNotification("thread/started", func(_ json.RawMessage) {})
	rpc.OnNotification("turn/started", s.onTurnStarted)
	rpc.OnNotification("turn/completed", s.onTurnCompleted)
	rpc.OnNotification("turn/failed", s.onTurnFailed)
	rpc.OnNotification("item/started", s.onItemStarted)
	rpc.OnNotification("item/updated", func(_ json.RawMessage) {}) // silenced
	rpc.OnNotification("item/completed", s.onItemCompleted)
	rpc.OnNotification("item/agentMessage/delta", s.onAgentDelta)
	rpc.OnNotification("item/commandExecution/outputDelta", s.onCommandOutput)
	rpc.OnNotification("item/reasoning/textDelta", s.onReasoningDelta)
	rpc.OnNotification("item/reasoning/summaryTextDelta", s.onReasoningDelta)
	rpc.OnNotification("thread/tokenUsage/updated", s.onUsageUpdated)
	rpc.OnNotification("error", s.onErrorNotif)

	rpc.OnServerRequest("item/commandExecution/requestApproval", s.handleCodexCommandApproval)
	rpc.OnServerRequest("item/fileChange/requestApproval", s.handleCodexFileApproval)
	rpc.OnServerRequest("item/permissions/requestApproval", s.handleCodexPermissionsApproval)
	// Older app-server releases used this unseparated method name.
	rpc.OnServerRequest("item/permissionsRequestApproval", s.handleCodexPermissionsApproval)
	rpc.OnServerRequest("item/tool/requestUserInput", s.handleCodexUserInput)
	rpc.OnServerRequest("item/tool/call", s.handleFunctionCall)
	rpc.OnServerRequest("mcpServer/elicitation/request", s.handleCodexMCPElicitation)
}

func (s *Session) onTurnStarted(raw json.RawMessage) {
	var p TurnStartedNotification
	if json.Unmarshal(raw, &p) == nil {
		s.beginRootTurn(p.ThreadID, p.Turn.ID)
	}
}

func (s *Session) onReasoningDelta(raw json.RawMessage) {
	var p AgentMessageDeltaNotification
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	if !s.isRootTurn(p.ThreadID, p.TurnID) || p.Delta == "" || p.ItemID == "" {
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

func (s *Session) onTurnCompleted(raw json.RawMessage) {
	s.outcome.notificationMu.Lock()
	defer s.outcome.notificationMu.Unlock()
	var p TurnCompletedNotification
	if json.Unmarshal(raw, &p) != nil || !s.isRootTurn(p.ThreadID, p.Turn.ID) {
		return
	}
	s.stopSteering()

	usage := p.Turn.Usage
	if usage == nil {
		s.usageMu.Lock()
		usage = s.latestUsage
		s.usageMu.Unlock()
	}
	if usage != nil {
		s.usageMu.Lock()
		s.latestUsage = usage
		s.usageMu.Unlock()
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
		s.finishAfterTerminal()
		return
	}
	s.emitDone(finalText, usage)
	s.finishAfterTerminal()
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
	if json.Unmarshal(raw, &p) != nil || !s.isRootTurn(p.ThreadID, p.Turn.ID) {
		return
	}
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
	s.finishAfterTerminal()
}

func (s *Session) onErrorNotif(raw json.RawMessage) {
	var p ErrorNotification
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	if !s.isRootTurn(p.ThreadID, p.TurnID) {
		return
	}
	if p.Error != nil {
		p.Message = turnErrorMessage(p.Error)
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
	if !s.terminal.CompareAndSwap(false, true) {
		return
	}
	s.stopSteering()
	doneMeta := map[string]any{}
	if tid := s.currentThreadID(); tid != "" {
		doneMeta[proto.DoneMetaAgentSessionID] = tid
		doneMeta[proto.DoneMetaAgentSessionType] = "codex_thread"
	}
	payload := proto.DonePayload{Content: content, Metadata: doneMeta}
	if usage != nil {
		payload.Usage = s.usagePayload(*usage)
	}
	payload = s.rememberOutcome(payload)
	env, err := proto.NewEnvelope(proto.TypeDone, s.runID, payload)
	if err != nil {
		return
	}
	s.sendTerminal(env)
}

func (s *Session) emitUsage(u TurnUsage) {
	env, err := proto.NewEnvelope(proto.TypeUsage, s.runID, proto.UsagePayload{
		Usage: s.usagePayload(u),
	})
	if err != nil {
		return
	}
	s.trySend(env)
}

func (s *Session) emitTerminal(message string, asError bool) {
	if !s.terminal.CompareAndSwap(false, true) {
		return
	}
	s.stopSteering()
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
	var events []proto.Envelope
	if asError {
		env, err := proto.NewEnvelope(proto.TypeError, s.runID, proto.ErrorPayload{Error: message})
		if err == nil {
			events = append(events, env)
		}
	}
	doneMeta := map[string]any{}
	if tid := s.currentThreadID(); tid != "" {
		doneMeta[proto.DoneMetaAgentSessionID] = tid
		doneMeta[proto.DoneMetaAgentSessionType] = "codex_thread"
	}
	payload := proto.DonePayload{
		Content:  message,
		Metadata: doneMeta,
	}
	payload = s.rememberOutcome(payload)
	env, err := proto.NewEnvelope(proto.TypeDone, s.runID, payload)
	if err != nil {
		return
	}
	s.sendTerminal(append(events, env)...)
}

func (s *Session) closeOut() {
	s.stopSteering()
	s.closeOutOnce.Do(func() {
		s.outMu.Lock()
		s.outClosed = true
		close(s.out)
		s.outMu.Unlock()
	})
}

func (s *Session) finishAfterTerminal() {
	if s.subagents == nil {
		s.closeOut()
	}
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
	if s.threadID == "" {
		s.threadID = id
	}
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
