package deepseekharness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/enginehost"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// serverSession drives one prompt against the resident dsh /api server.
//
// Why this exists next to the headless session: the headless surface runs
// a fresh dsh per prompt and therefore cannot continue a conversation,
// which is why the server injects prior turns for it. The resident server
// keeps dsh's own session log, so a turn continues by prompting the same
// session id — warm from memory, or loaded from disk after a restart. It
// also streams, which headless does not.
//
// Frame ownership follows the agent.Session contract: this session writes
// upstream frames on out and closes out exactly once, after the terminal
// done frame. The engine process is NOT owned here — it belongs to the
// enginehost lease, which is released when the run ends.
type serverSession struct {
	runID     string
	sessionID string
	// isNewSession records whether this run opened the dsh session. It
	// decides nothing about the turn itself, but a failed first turn must
	// not be reported as a resumable session id.
	isNewSession bool

	cfg  sessionConfig
	api  *apiClient
	down *enginehost.Downlink
	out  chan<- proto.Envelope

	// The engine is reached through these three rather than through the
	// lease itself, so a test can drive a real gateway over httptest
	// without launching a dsh process. release is called exactly once,
	// when the run ends; diagnostics explains a mid-turn engine death.
	engineExited <-chan struct{}
	release      func()
	diagnostics  func() string

	seq atomic.Uint64

	// answer is the assembled reply. It is built from assistant/message
	// events, not from the streamed deltas, because a retried step
	// re-streams and would otherwise be counted twice.
	answer strings.Builder
	usage  proto.Usage
	// lastRetryFailure is diagnostic only. A scheduled retry may recover, so
	// it is surfaced only when the enclosing turn ultimately ends in error.
	lastRetryFailure string

	cancelOnce   sync.Once
	closeOutOnce sync.Once
	cancelled    atomic.Bool
}

var _ agent.Session = (*serverSession)(nil)

// newServerSession acquires the resident engine, attaches to its event
// downlink, submits the turn, and starts pumping frames.
//
// Order matters: the downlink is attached BEFORE the prompt is submitted.
// dsh starts emitting as soon as it accepts the turn, and a downlink
// opened afterwards would miss the opening frames of a fast turn.
func newServerSession(parent context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope, cfg sessionConfig) (agent.Session, error) {
	if out == nil {
		return nil, errors.New("deepseekharness: nil out channel")
	}
	if cfg.logger == nil {
		cfg.logger = obslog.Bg()
	}
	if cfg.binary == "" {
		cfg.binary = defaultBinary
	}

	launch, err := buildServerLaunch(req)
	if err != nil {
		return nil, err
	}
	launch.Binary = cfg.binary
	if err := materializeManagedSkills(parent, launch, req.AgentOptions["skills"]); err != nil {
		return nil, err
	}

	lease, err := enginehost.Acquire(parent, launch.spec())
	if err != nil {
		return nil, fmt.Errorf("deepseekharness: start resident dsh server: %w", err)
	}

	s := &serverSession{
		runID:        req.RunID,
		cfg:          cfg,
		api:          newAPIClient(enginehost.NewClient(lease.BaseURL(), 0)),
		out:          out,
		engineExited: lease.Exited(),
		release:      lease.Release,
		diagnostics:  lease.Diagnostics,
	}

	if err := s.attachAndPrompt(parent, req, launch.WorkDir); err != nil {
		lease.Release()
		return nil, err
	}
	go s.pump()
	return s, nil
}

func (s *serverSession) attachAndPrompt(ctx context.Context, req proto.PromptRequestPayload, workDir string) error {
	down, err := s.api.transport.Dial(ctx, eventsMuxPath)
	if err != nil {
		return fmt.Errorf("deepseekharness: attach event downlink: %w (engine output: %s)", err, s.diagnostics())
	}
	s.down = down

	s.sessionID = strings.TrimSpace(req.AgentSessionID)
	if s.sessionID == "" {
		// A fresh session is rooted at the run's workspace. Resumed
		// sessions carry their own cwd in the log, so cwd is not resent.
		id, err := s.api.CreateSession(ctx, workDir)
		if err != nil {
			down.Close()
			return err
		}
		s.sessionID = id
		s.isNewSession = true
	}

	content, err := promptContent(req)
	if err != nil {
		down.Close()
		return err
	}
	if err := s.api.Prompt(ctx, s.sessionID, content); err != nil {
		down.Close()
		return err
	}
	return nil
}

// promptContent renders the turn's text and image attachments into the
// gateway's content-part shape. dsh accepts a narrower set of media types
// than Parsar carries, so an attachment it cannot represent is dropped
// with a warning rather than failing the turn.
func promptContent(req proto.PromptRequestPayload) ([]promptContentPart, error) {
	text := strings.TrimSpace(req.Prompt)
	if text == "" {
		return nil, errors.New("deepseekharness: empty prompt")
	}
	if system := systemPreamble(req.AgentOptions); system != "" {
		// The gateway has no system-prompt seam, so an injected system
		// prompt rides at the head of the turn text, as on the headless
		// path.
		text = system + "\n\n" + text
	}
	parts := []promptContentPart{{Type: "text", Text: text}}
	for _, att := range req.Attachments {
		if !isSupportedImageMedia(att.MIME) {
			continue
		}
		parts = append(parts, promptContentPart{
			Type:      "image",
			MediaType: att.MIME,
			Data:      att.DataBase64,
		})
	}
	return parts, nil
}

// supportedImageMedia is the gateway's accepted raster set. A media type
// outside it is rejected by the request schema, which would fail the whole
// turn over an attachment.
var supportedImageMedia = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
	"image/gif":  true,
}

func isSupportedImageMedia(mime string) bool {
	return supportedImageMedia[strings.ToLower(strings.TrimSpace(mime))]
}

func systemPreamble(opts map[string]any) string {
	if override := stringOpt(opts, "override_system_prompt"); override != "" {
		return override
	}
	return stringOpt(opts, "system_prompt")
}

// pump reads the downlink until the turn ends, translating events into
// upstream frames, then emits the terminal frames and closes out.
func (s *serverSession) pump() {
	defer s.closeOut()
	defer s.release()
	defer s.down.Close()

	var (
		turnEnded bool
		failure   string
	)
	frames := s.down.Frames()
loop:
	for {
		select {
		case raw, ok := <-frames:
			if !ok {
				if err := s.down.Err(); err != nil && failure == "" {
					failure = fmt.Sprintf("deepseek-harness: event stream ended: %v", err)
				}
				break loop
			}
			event, matched := decodeFrame(raw, s.sessionID)
			if !matched {
				continue
			}
			done, reason := s.handleEvent(event)
			if reason != "" && failure == "" {
				failure = reason
			}
			if done {
				turnEnded = true
				break loop
			}
		case <-s.engineExited:
			// The engine died mid-turn. Its remaining frames will never
			// arrive, so the run has to fail rather than wait.
			if failure == "" {
				failure = fmt.Sprintf("deepseek-harness: engine exited mid-turn: %s", s.diagnostics())
			}
			break loop
		}
	}

	// Cancellation outranks whatever the stream reported: Cancel closes the
	// downlink, so a cancelled run always also sees the stream end, and
	// reporting that instead would hide the actual cause.
	if s.cancelled.Load() {
		failure = "deepseek-harness: cancelled"
	}
	if !turnEnded && failure == "" {
		failure = "deepseek-harness: event stream closed before the turn ended"
	}
	s.emitTerminal(failure)
}

// handleEvent translates one durable session event. It returns done=true
// on the frame that ends the turn, and a non-empty reason when the turn
// failed.
func (s *serverSession) handleEvent(event sessionEventPayload) (bool, string) {
	switch event.Event.Type {
	case eventAssistantChunk:
		s.handleChunk(event.Event.Data)
	case eventAssistantMessage:
		var data assistantMessageData
		if err := json.Unmarshal(event.Event.Data, &data); err != nil {
			return false, ""
		}
		if text := data.assistantText(); text != "" {
			if s.answer.Len() > 0 {
				s.answer.WriteByte('\n')
			}
			s.answer.WriteString(text)
		}
	case eventToolCall:
		var data toolCallData
		if err := json.Unmarshal(event.Event.Data, &data); err != nil {
			return false, ""
		}
		s.send(proto.TypeToolCall, proto.ToolCallPayload{
			ID:    data.CallID,
			Name:  data.Name,
			Stage: "before",
			Args:  argsFromJSONString(data.Arguments),
		})
	case eventToolResult:
		var data toolResultData
		if err := json.Unmarshal(event.Event.Data, &data); err != nil {
			return false, ""
		}
		text, isError := data.textFromToolResult()
		s.send(proto.TypeToolCall, proto.ToolCallPayload{
			ID:     data.Message.Source.CallID,
			Stage:  "after",
			Result: map[string]any{"output": text, "is_error": isError},
		})
	case eventApprovalAsked:
		// Reaching this event means the composed permission preset was not
		// the unattended one: with approval "never" dsh rejects such
		// actions itself instead of asking. There is no human on this
		// path, so the run fails loudly rather than hanging.
		return true, "deepseek-harness: engine asked for approval, but this run has no approver"
	case eventAgentError:
		return false, "deepseek-harness: " + truncate(strings.TrimSpace(string(event.Event.Data)), 400)
	case eventLLMRetry:
		var data llmRetryData
		if err := json.Unmarshal(event.Event.Data, &data); err == nil {
			s.lastRetryFailure = data.Failure.summary()
		}
	case eventTurnEnd:
		var data turnEndData
		if err := json.Unmarshal(event.Event.Data, &data); err != nil {
			return true, ""
		}
		if data.Reason.Kind != "completed" {
			reason := data.Reason.Kind
			detail := strings.TrimSpace(data.Reason.Message)
			if detail == "" {
				detail = data.Reason.Error.summary()
			}
			if detail == "" {
				detail = s.lastRetryFailure
			}
			if detail != "" {
				reason += ": " + truncate(detail, 400)
			}
			return true, "deepseek-harness: turn ended without completing (" + reason + ")"
		}
		return true, ""
	}
	return false, ""
}

// handleChunk forwards streamed text and reasoning, and records usage.
// Text and reasoning go to different Parsar frame types so a renderer can
// keep the model's thinking out of the answer.
func (s *serverSession) handleChunk(raw json.RawMessage) {
	var data assistantChunkData
	if err := json.Unmarshal(raw, &data); err != nil {
		return
	}
	switch data.Chunk.Type {
	case chunkTextDelta:
		if data.Chunk.Text == "" {
			return
		}
		s.send(proto.TypeDelta, proto.DeltaPayload{Delta: data.Chunk.Text, Sequence: s.seq.Add(1)})
	case chunkReasoningDelta:
		if data.Chunk.Text == "" {
			return
		}
		s.send(proto.TypeThinking, proto.ThinkingPayload{Text: data.Chunk.Text, Sequence: s.seq.Add(1)})
	case chunkUsage:
		// Usage arrives per model request, so a multi-step turn reports
		// several. They are summed: the run's cost is the whole turn's.
		s.usage.Provider = usageProvider
		s.usage.InputTokens += data.Chunk.Usage.InputTokens
		s.usage.OutputTokens += data.Chunk.Usage.OutputTokens
		if data.Chunk.Usage.CacheReadTokens > 0 {
			if s.usage.Raw == nil {
				s.usage.Raw = map[string]any{}
			}
			prior, _ := s.usage.Raw["cache_read_tokens"].(int32)
			s.usage.Raw["cache_read_tokens"] = prior + data.Chunk.Usage.CacheReadTokens
		}
		s.send(proto.TypeUsage, proto.UsagePayload{Usage: s.usage})
	}
}

// emitTerminal writes the error frame (when the turn failed) and the done
// frame. The session id is persisted only on success: handing back an id
// whose first turn failed would make the next prompt resume a session
// that may not exist on disk.
func (s *serverSession) emitTerminal(failure string) {
	content := strings.TrimSpace(s.answer.String())
	if failure != "" {
		s.send(proto.TypeError, proto.ErrorPayload{Error: failure})
	}
	metadata := map[string]any{"connector_path": "dsh_apiproxy"}
	if failure == "" || !s.isNewSession {
		// A resumed session id stays valid even after a failed turn: it
		// already exists on disk, and forgetting it would strand the
		// conversation on a fresh session.
		metadata[proto.DoneMetaAgentSessionID] = s.sessionID
		metadata[proto.DoneMetaAgentSessionType] = "dsh_session"
	}
	usage := s.usage
	if usage.Provider == "" {
		usage.Provider = usageProvider
	}
	s.send(proto.TypeDone, proto.DonePayload{
		Content:    content,
		Transcript: content,
		Usage:      usage,
		Metadata:   metadata,
	})
}

func (s *serverSession) send(typ string, payload any) {
	env, err := proto.NewEnvelope(typ, s.runID, payload)
	if err != nil {
		s.cfg.logger.Warn("deepseekharness: encode frame", "run_id", s.runID, "type", typ, "err", err)
		return
	}
	select {
	case s.out <- env:
	case <-time.After(2 * time.Second):
		s.cfg.logger.Warn("deepseekharness: frame send timed out", "run_id", s.runID, "type", typ)
	}
}

func (s *serverSession) closeOut() { s.closeOutOnce.Do(func() { close(s.out) }) }

// Cancel aborts the running turn. It cancels the dsh session, never the
// engine process: the resident server is shared, so killing it would
// abort other conversations' turns too.
func (s *serverSession) Cancel(ctx context.Context) error {
	s.cancelOnce.Do(func() {
		s.cancelled.Store(true)
		cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := s.api.Cancel(cancelCtx, s.sessionID); err != nil {
			s.cfg.logger.Warn("deepseekharness: cancel session", "run_id", s.runID, "err", err)
		}
		// The downlink is closed so pump unblocks even if the engine never
		// logs a turn/end for the cancelled turn.
		s.down.Close()
	})
	return nil
}

// SubmitPermission has no counterpart on this path: the profile pins the
// unattended permission preset, so dsh rejects escalation itself and never
// asks. Returning the sentinel keeps the router's race handling intact.
func (s *serverSession) SubmitPermission(context.Context, string, proto.PermissionDecisionPayload) error {
	return agent.ErrUnknownPermission
}

func (s *serverSession) SubmitPromptForUserChoice(context.Context, string, proto.PromptForUserChoiceDecisionPayload) error {
	return agent.ErrUnknownAsk
}

// buildServerLaunch derives the resident server's identity and launch
// inputs from one prompt request. It reuses the headless path's home
// resolution and provider normalisation so the two surfaces agree on
// where a conversation's dsh state lives.
func buildServerLaunch(req proto.PromptRequestPayload) (serverLaunch, error) {
	workDir, err := resolveWorkDir(req.WorkDir)
	if err != nil {
		return serverLaunch{}, err
	}
	provider, hasProvider, err := normaliseProvider(req.AgentOptions["dsh_provider"])
	if err != nil {
		return serverLaunch{}, err
	}
	if hasProvider {
		if err := validateProvider(provider); err != nil {
			return serverLaunch{}, err
		}
	}
	home, err := resolveHome(req.AgentStateKey, req.ConversationID, req.RunID)
	if err != nil {
		return serverLaunch{}, err
	}

	envOpt, err := envMap(req.AgentOptions["env"])
	if err != nil {
		return serverLaunch{}, err
	}
	// Same pinning as the headless path and for the same reason: these
	// three must not be redirected by agent_options.
	envOpt[dshHomeEnvVar] = home
	envOpt[dshPermissionModeEnvVar] = sandboxPermissionMode
	envOpt[dshTelemetryDisabledEnvVar] = "1"
	extra, err := buildEnv(envOpt)
	if err != nil {
		return serverLaunch{}, err
	}
	mcpRows, err := normaliseMCPRows(req.AgentOptions["mcp_servers"], workDir)
	if err != nil {
		return serverLaunch{}, err
	}

	stateKey := strings.TrimSpace(req.AgentStateKey)
	if stateKey == "" {
		stateKey = strings.TrimSpace(req.ConversationID)
	}
	return serverLaunch{
		Home:        home,
		WorkDir:     workDir,
		Provider:    provider,
		HasProvider: hasProvider,
		Model:       stringOpt(req.AgentOptions, "model"),
		ProviderID:  stringOpt(req.AgentOptions, "provider"),
		MCPRows:     mcpRows,
		Env:         append(os.Environ(), extra...),
		StateKey:    stateKey,
	}, nil
}
