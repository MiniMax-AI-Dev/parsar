package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// sessionConfig customises Factory for tests (alternative binary path,
// alternative logger, shorter SIGTERM→SIGKILL escalation).
type sessionConfig struct {
	// claudeBinary defaults to "claude" so os/exec resolves via PATH.
	claudeBinary string

	// extraArgs are appended after BuildArgs' output. Tests use this
	// for the os/exec helper-process pattern.
	extraArgs []string

	// killTimeout is how long Cancel waits for SIGTERM to drain
	// before SIGKILL. 3s in production; tests pin it short.
	killTimeout time.Duration

	// askTimeout bounds how long the daemon waits for the human to answer a
	// permission or prompt_for_user_choice (AskUserQuestion). 10 minutes in
	// production; tests pin it short so timeout paths are exercisable.
	// Zero disables the timer — leftover scaffolding for very early
	// adapter wiring; production always picks defaultAskTimeout.
	askTimeout time.Duration

	logger *slog.Logger
}

const defaultAskTimeout = 10 * time.Minute

func defaultConfig() sessionConfig {
	return sessionConfig{
		claudeBinary: "claude",
		killTimeout:  3 * time.Second,
		askTimeout:   defaultAskTimeout,
		logger:       obslog.Bg(),
	}
}

// Factory implements agent.Factory for agent_kind="claude_code".
// Register during daemon startup:
//
//	registry.Register("claude_code", claudecode.Factory)
func Factory(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
	return newSession(ctx, req, out, defaultConfig())
}

// Session wraps a single `claude` CLI subprocess.
type Session struct {
	runID string
	cfg   sessionConfig

	proc    *clirunner.Process
	stdin   io.WriteCloser
	stdinMu sync.Mutex

	pending    *pendingTable
	askPending *pendingAskTable
	translator *translator

	out          chan<- proto.Envelope
	closeOutOnce sync.Once
	outMu        sync.RWMutex
	outClosed    bool

	// cancelCtx is a child of parent ctx so Session.Cancel can signal
	// everyone without racing router shutdown.
	cancelCtx context.Context

	cancelOnce sync.Once

	// interactionTimersMu guards permission and AskUserQuestion watchdogs.
	// Timeout callbacks and human responses race through atomic pending
	// tables, so only one response can reach Claude Code.
	interactionTimersMu sync.Mutex
	interactionTimers   map[string]*time.Timer

	// latestSessionID is the most recent upstream session id seen on a
	// system-init / result frame. The cancel-path done envelope reads
	// it back so the server can RememberSession even when claude was
	// killed mid-prompt (without it, the next user message starts a
	// brand-new chat with no --resume).
	latestSessionIDMu sync.Mutex
	latestSessionID   string

	buildCleanup func()
}

var _ agent.Session = (*Session)(nil)

// newSession is the internal constructor; cfg lets tests inject a fake
// claude binary and a short kill timeout.
func newSession(parent context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope, cfg sessionConfig) (*Session, error) {
	if out == nil {
		return nil, errors.New("claudecode: nil out channel")
	}
	if req.Prompt == "" && len(req.Attachments) == 0 {
		// A pure-image inbound (Feishu user pastes a screenshot
		// without typing) is a valid prompt — Attachments alone
		// drives the turn — and must not 400 here.
		return nil, errors.New("claudecode: empty prompt and no attachments")
	}
	if cfg.logger == nil {
		cfg.logger = obslog.Bg()
	}
	if cfg.claudeBinary == "" {
		cfg.claudeBinary = "claude"
	}
	if cfg.killTimeout <= 0 {
		cfg.killTimeout = 3 * time.Second
	}

	cfg.logger.Info("claudecode: newSession start",
		"run_id", req.RunID, "agent_kind", req.AgentKind,
		"prompt_len", len(req.Prompt), "work_dir", req.WorkDir,
		"has_agent_options", req.AgentOptions != nil,
		"agent_session_id", req.AgentSessionID,
		"claude_binary", cfg.claudeBinary)

	// Install plugins BEFORE BuildArgs so the resolved local paths
	// can be folded into opts["plugin_dirs"]. installPlugins demotes
	// individual plugins to warnings; a hard error (e.g. mkdir fail)
	// aborts the session.
	//
	// sessionWorkDir is reused for cmd.Dir below so plugins land at
	// <sessionWorkDir>/.claude/plugins/ and the claude subprocess sees
	// them at cwd-relative paths.
	sessionWorkDir, err := resolveSessionWorkDir(req.WorkDir, req.ConversationID)
	if err != nil {
		cfg.logger.Error("claudecode: resolveSessionWorkDir failed",
			"run_id", req.RunID, "err", err.Error())
		return nil, fmt.Errorf("claudecode: resolve session workDir: %w", err)
	}
	if sessionWorkDir != req.WorkDir {
		cfg.logger.Info("claudecode: req.WorkDir empty, using resolved session dir",
			"run_id", req.RunID, "session_dir", sessionWorkDir)
	}

	pluginOpts := req.AgentOptions
	if rawPlugins, ok := pluginOpts["plugins"]; ok {
		descriptors, decodeWarns := decodePluginDescriptors(rawPlugins)
		for _, w := range decodeWarns {
			cfg.logger.Warn("claudecode: plugin descriptor decode warning",
				"run_id", req.RunID, "msg", w)
		}
		installRes, err := installPlugins(parent, cfg.logger, sessionWorkDir, descriptors)
		if err != nil {
			cfg.logger.Error("claudecode: installPlugins failed",
				"run_id", req.RunID, "err", err.Error())
			return nil, fmt.Errorf("claudecode: install plugins: %w", err)
		}
		for _, w := range installRes.Warnings {
			cfg.logger.Warn("claudecode: plugin install warning",
				"run_id", req.RunID, "msg", w)
		}
		if len(installRes.PluginDirs) > 0 {
			// Defensive copy so we never mutate the caller's map.
			// Existing plugin_dirs (hand-configured override) wins;
			// capability-resolved dirs append.
			pluginOpts = cloneAgentOptions(req.AgentOptions)
			pluginOpts["plugin_dirs"] = mergePluginDirs(pluginOpts["plugin_dirs"], installRes.PluginDirs)
		}
		cfg.logger.Info("claudecode: plugins installed",
			"run_id", req.RunID,
			"plugin_count", len(descriptors),
			"dir_count", len(installRes.PluginDirs))
	}

	// Skills install to <sessionWorkDir>/.claude/skills/<name>/, which
	// Claude Code auto-scans at startup. No CLI flag, no opts mutation.
	if rawSkills, ok := pluginOpts["skills"]; ok {
		descriptors, decodeWarns := decodeSkillDescriptors(rawSkills)
		for _, w := range decodeWarns {
			cfg.logger.Warn("claudecode: skill descriptor decode warning",
				"run_id", req.RunID, "msg", w)
		}
		installRes, err := installSkills(parent, cfg.logger, sessionWorkDir, descriptors)
		if err != nil {
			cfg.logger.Error("claudecode: installSkills failed",
				"run_id", req.RunID, "err", err.Error())
			return nil, fmt.Errorf("claudecode: install skills: %w", err)
		}
		for _, w := range installRes.Warnings {
			cfg.logger.Warn("claudecode: skill install warning",
				"run_id", req.RunID, "msg", w)
		}
		cfg.logger.Info("claudecode: skills installed",
			"run_id", req.RunID,
			"skill_count", len(descriptors),
			"warn_count", len(installRes.Warnings))
	}

	buildRes, err := BuildArgs(pluginOpts, req.AgentSessionID)
	if err != nil {
		cfg.logger.Error("claudecode: BuildArgs failed", "run_id", req.RunID, "err", err)
		return nil, fmt.Errorf("claudecode: build args: %w", err)
	}
	cfg.logger.Info("claudecode: BuildArgs ok",
		"run_id", req.RunID, "args", buildRes.Args, "env_count", len(buildRes.Env))

	args := append([]string{}, buildRes.Args...)
	args = append(args, cfg.extraArgs...)

	cfg.logger.Info("claudecode: starting subprocess",
		"run_id", req.RunID, "binary", cfg.claudeBinary,
		"args", args, "dir", sessionWorkDir)
	proc, err := clirunner.Start(clirunner.StartOptions{
		Parent:      parent,
		Binary:      cfg.claudeBinary,
		Args:        args,
		Dir:         sessionWorkDir,
		Env:         append(os.Environ(), buildRes.Env...),
		NeedStdin:   true,
		KillTimeout: cfg.killTimeout,
	})
	if err != nil {
		cfg.logger.Error("claudecode: cmd.Start failed",
			"run_id", req.RunID, "binary", cfg.claudeBinary, "err", err)
		buildRes.Cleanup()
		return nil, fmt.Errorf("claudecode: start %q: %w", cfg.claudeBinary, err)
	}
	cfg.logger.Info("claudecode: subprocess started",
		"run_id", req.RunID, "pid", proc.Cmd.Process.Pid)

	pending := newPendingTable()
	askPending := newPendingAskTable()
	s := &Session{
		runID:             req.RunID,
		cfg:               cfg,
		proc:              proc,
		stdin:             proc.Stdin,
		pending:           pending,
		askPending:        askPending,
		translator:        newTranslator(req.RunID, pending, askPending, defaultPermIDMinter, defaultAskIDMinter),
		out:               out,
		cancelCtx:         proc.Context(),
		interactionTimers: make(map[string]*time.Timer),
		buildCleanup:      buildRes.Cleanup,
	}

	// Write the initial user message before launching pumps so the
	// first stdout line corresponds to the prompt we just sent. Best
	// effort: write failure → pump sees EOF and synthesises
	// error+done.
	if msg, err := buildUserMessageWithAttachments(req.Prompt, req.Attachments); err == nil {
		cfg.logger.Info("claudecode: writing initial user message to stdin",
			"run_id", req.RunID, "msg_bytes", len(msg), "attachments", len(req.Attachments))
		if _, werr := s.writeStdin(msg); werr != nil {
			cfg.logger.Warn("claudecode: write initial user message",
				"run_id", req.RunID, "err", werr)
		} else {
			cfg.logger.Info("claudecode: initial user message written ok", "run_id", req.RunID)
		}
	} else {
		cfg.logger.Warn("claudecode: build user message",
			"run_id", req.RunID, "err", err)
	}

	cfg.logger.Info("claudecode: launching stdout/stderr pumps", "run_id", req.RunID)
	go s.pumpStderr(proc.Stderr)
	go s.run(proc.Stdout)

	return s, nil
}

// writeStdin appends to claude's stdin under a mutex (claude only
// promises NDJSON framing; concurrent writes from SubmitPermission and
// the initial user-message write must not tear).
func (s *Session) writeStdin(b []byte) (int, error) {
	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()
	return s.stdin.Write(b)
}

// Cancel asks the subprocess to stop. SIGTERM, escalating to SIGKILL
// after cfg.killTimeout. Idempotent; the actual teardown (out chan
// close) happens asynchronously via the pump.
func (s *Session) Cancel(_ context.Context) error {
	s.cancelOnce.Do(func() {
		s.stopAllInteractionTimers()
		s.proc.Cancel()
	})
	return nil
}

// stopAllInteractionTimers cancels every outstanding human-response
// watchdog. Called on session Cancel/terminal so timers cannot fire into a
// closed stdin.
func (s *Session) stopAllInteractionTimers() {
	s.interactionTimersMu.Lock()
	timers := s.interactionTimers
	s.interactionTimers = make(map[string]*time.Timer)
	s.interactionTimersMu.Unlock()
	for _, t := range timers {
		t.Stop()
	}
}

// SubmitPermission writes a control_response back to claude for the
// given perm_id. Returns agent.ErrUnknownPermission when permID isn't
// in the pending table.
func (s *Session) SubmitPermission(_ context.Context, permID string, decision proto.PermissionDecisionPayload) error {
	entry, ok := s.pending.Take(permID)
	if !ok {
		return agent.ErrUnknownPermission
	}
	s.stopInteractionTimer(permID)

	var inner map[string]any
	if decision.Approved {
		updatedInput := decision.UpdatedInput
		if updatedInput == nil {
			updatedInput = entry.Input
		}
		inner = map[string]any{
			"behavior":     "allow",
			"updatedInput": updatedInput,
		}
	} else {
		msg := decision.Message
		if msg == "" {
			msg = "denied by operator"
		}
		inner = map[string]any{
			"behavior": "deny",
			"message":  msg,
		}
	}

	body, err := json.Marshal(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": entry.CCRequestID,
			"response":   inner,
		},
	})
	if err != nil {
		return fmt.Errorf("claudecode: marshal control_response: %w", err)
	}
	body = append(body, '\n')

	if _, err := s.writeStdin(body); err != nil {
		// Restore the entry so a transient stdin write failure remains
		// retryable and still has a bounded lifetime.
		s.pending.Record(permID, entry.CCRequestID, entry.Input)
		s.startPermissionTimer(permID)
		return fmt.Errorf("claudecode: write control_response: %w", err)
	}
	return nil
}

// SubmitPromptForUserChoice writes a tool_result back into claude's
// stdin for the AskUserQuestion call the daemon intercepted. Returns
// agent.ErrUnknownAsk when askID isn't in the pending ask table —
// usually a race with Cancel or a duplicate decision from the server.
//
// The reply is shaped as a normal Claude Code tool_result message; the
// model resumes its turn as if the local SDK had executed the tool
// and supplied the human's answer.
//
// Cancelled answers (timeout, operator /cancel) still write back
// is_error=false text — see plan: returning is_error=true would push
// the agent into a "retry the same tool" loop, which is worse UX than
// telling it "the user stopped, fold and report".
func (s *Session) SubmitPromptForUserChoice(_ context.Context, askID string, decision proto.PromptForUserChoiceDecisionPayload) error {
	// Take is atomic read+delete; the timer-fired cancel and a server-
	// delivered answer can both reach here, but only one wins. The
	// loser sees ok=false and returns ErrUnknownAsk — the router logs
	// and moves on.
	entry, ok := s.askPending.Take(askID)
	if !ok {
		return agent.ErrUnknownAsk
	}

	// Drop the timer so its callback (if pending) finds the entry gone
	// and returns silently. Already-fired callbacks lost the Take race
	// above; stop is best-effort either way.
	s.stopAskTimer(askID)

	// Pick the reply shape based on which path recorded the entry.
	// control_request path (CCRequestID set) needs a control_response
	// frame; tool_use path needs a user message with a tool_result block.
	var (
		body    []byte
		buildEr error
	)
	if entry.CCRequestID != "" {
		body, buildEr = buildAskUserControlResponse(entry, decision)
	} else {
		body, buildEr = buildAskUserToolResult(entry, decision)
	}
	if buildEr != nil {
		return fmt.Errorf("claudecode: marshal ask reply: %w", buildEr)
	}
	if _, err := s.writeStdin(body); err != nil {
		// Entry is already gone (Take consumed it). A retry will see
		// ErrUnknownAsk; the session is going to die anyway when stdin
		// errors, so we don't try to restore the entry.
		return fmt.Errorf("claudecode: write ask reply: %w", err)
	}
	return nil
}

// startAskTimer launches a single-shot watchdog that times the human
// out after cfg.askTimeout. On fire, the watchdog submits a Cancelled
// decision against itself so the agent's tool_result lands the same
// way as if the operator had clicked "stop" — no special "timeout"
// branch in SubmitPromptForUserChoice.
func (s *Session) startAskTimer(askID string) {
	if s.cfg.askTimeout <= 0 {
		return
	}
	timer := time.AfterFunc(s.cfg.askTimeout, func() {
		_ = s.SubmitPromptForUserChoice(context.Background(), askID, proto.PromptForUserChoiceDecisionPayload{
			Cancelled: true,
			Reason:    "timeout",
		})
	})
	s.interactionTimersMu.Lock()
	if prev, ok := s.interactionTimers[askID]; ok {
		// Same askID seen twice: cancel the old timer so we don't fire
		// two decisions. Shouldn't happen on a clean stream, but two
		// stdout-pump dispatches with the same env.ID would otherwise
		// race.
		prev.Stop()
	}
	s.interactionTimers[askID] = timer
	s.interactionTimersMu.Unlock()
}

func (s *Session) stopAskTimer(askID string) {
	s.stopInteractionTimer(askID)
}

// startPermissionTimer applies the same bounded human-response window to
// tool approvals. Expiry is an explicit deny, never an implicit allow.
func (s *Session) startPermissionTimer(permID string) {
	if s.cfg.askTimeout <= 0 || permID == "" {
		return
	}
	timer := time.AfterFunc(s.cfg.askTimeout, func() {
		_ = s.SubmitPermission(context.Background(), permID, proto.PermissionDecisionPayload{
			Approved: false,
			Message:  "permission request timed out",
		})
	})
	s.interactionTimersMu.Lock()
	if prev, ok := s.interactionTimers[permID]; ok {
		prev.Stop()
	}
	s.interactionTimers[permID] = timer
	s.interactionTimersMu.Unlock()
}

func (s *Session) stopInteractionTimer(id string) {
	s.interactionTimersMu.Lock()
	timer, ok := s.interactionTimers[id]
	if ok {
		delete(s.interactionTimers, id)
	}
	s.interactionTimersMu.Unlock()
	if ok {
		timer.Stop()
	}
}

// run is the stdout pump. Owns the out channel close.
func (s *Session) run(stdout io.Reader) {
	s.cfg.logger.Info("claudecode: run() stdout pump started", "run_id", s.runID)
	defer s.buildCleanup()
	defer s.closeOut()

	sc := bufio.NewScanner(stdout)
	// Claude can emit very large tool_result lines in one frame; 16MB
	// is far above any practical single-tool output.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	lineCount := 0
	terminal := false
	for sc.Scan() {
		line := sc.Bytes()
		lineCount++
		s.cfg.logger.Info("claudecode: stdout line",
			"run_id", s.runID, "line_num", lineCount, "len", len(line),
			"head", string(line[:min(len(line), 200)]))
		tx, err := s.translator.Translate(line)
		if err != nil {
			s.cfg.logger.Warn("claudecode: translate line",
				"run_id", s.runID, "err", err, "len", len(line))
			continue
		}
		if id := strings.TrimSpace(tx.SessionID); id != "" {
			s.latestSessionIDMu.Lock()
			s.latestSessionID = id
			s.latestSessionIDMu.Unlock()
		}
		s.cfg.logger.Info("claudecode: translated",
			"run_id", s.runID, "line_num", lineCount,
			"envelope_count", len(tx.Envelopes), "terminal", tx.Terminal)
		for _, env := range tx.Envelopes {
			select {
			case s.out <- env:
				s.cfg.logger.Info("claudecode: envelope sent to out",
					"run_id", s.runID, "type", env.Type, "env_id", env.ID)
				if env.Type == proto.TypePromptForUserChoice {
					// Start the human-answer watchdog AFTER the envelope
					// has been handed off — counting the 10-minute window
					// from "router has it" not "we're about to try". A
					// slow consumer that blocked us on the send shouldn't
					// also burn timeout budget the human never saw.
					//
					// Late-answer race: if the router somehow delivers a
					// decision before we finish startAskTimer, the Take
					// inside SubmitPromptForUserChoice still wins
					// exclusively; the timer just becomes a no-op when it
					// fires.
					//
					// Ask id lives on the payload now (env.ID is the run
					// id so server-side dispatch can fan to the run's
					// subscriber); decode just enough to seed the timer.
					var p proto.PromptForUserChoicePayload
					if err := env.DecodePayload(&p); err == nil && p.AskID != "" {
						s.startAskTimer(p.AskID)
					}
				} else if env.Type == proto.TypePermissionRequest {
					var p proto.PermissionRequestPayload
					requestID := ""
					if err := env.DecodePayload(&p); err == nil {
						requestID = strings.TrimSpace(p.RequestID)
					}
					if requestID == "" {
						requestID = strings.TrimSpace(env.ID)
					}
					if requestID != "" {
						s.startPermissionTimer(requestID)
					}
				}
			case <-s.cancelCtx.Done():
				s.cfg.logger.Info("claudecode: cancelled during out send", "run_id", s.runID)
				_ = s.proc.Wait()
				return
			}
		}
		if tx.Terminal {
			terminal = true
			s.stopAllInteractionTimers()
			s.closeOut()
			break
		}
	}
	if err := sc.Err(); err != nil &&
		!errors.Is(err, io.EOF) &&
		!errors.Is(err, context.Canceled) {
		s.cfg.logger.Warn("claudecode: scan stdout",
			"run_id", s.runID, "err", err)
	}
	s.cfg.logger.Info("claudecode: stdout pump exiting",
		"run_id", s.runID, "lines_read", lineCount, "terminal", terminal)

	waitErr := s.proc.Wait()
	s.cfg.logger.Info("claudecode: subprocess exited",
		"run_id", s.runID, "wait_err", waitErr,
		"exit_code", s.proc.Cmd.ProcessState.ExitCode())

	if !terminal {
		s.synthesizeTerminal(waitErr)
	}
}

// pumpStderr drains and logs stderr so the subprocess doesn't block
// on a full pipe.
func (s *Session) pumpStderr(stderr io.Reader) {
	s.cfg.logger.Info("claudecode: pumpStderr started", "run_id", s.runID)
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 0, 16*1024), 1<<20)
	lineCount := 0
	for sc.Scan() {
		lineCount++
		s.cfg.logger.Warn("claude stderr",
			"run_id", s.runID, "line", sc.Text())
	}
	s.cfg.logger.Info("claudecode: pumpStderr done", "run_id", s.runID, "lines", lineCount)
}

// synthesizeTerminal emits error+done when the subprocess exited
// without a result frame.
func (s *Session) synthesizeTerminal(waitErr error) {
	msg := "claude_code: subprocess exited without result"
	if waitErr != nil {
		msg = fmt.Sprintf("claude_code: subprocess exited: %v", waitErr)
	}
	if s.cancelCtx.Err() != nil {
		msg = "claude_code: cancelled"
	}
	if errEnv, err := proto.NewEnvelope(proto.TypeError, s.runID, proto.ErrorPayload{Error: msg}); err == nil {
		s.trySend(errEnv)
	}
	if doneEnv, err := proto.NewEnvelope(proto.TypeDone, s.runID, proto.DonePayload{Metadata: s.doneMetaForCancel()}); err == nil {
		s.trySend(doneEnv)
	}
}

// doneMetaForCancel returns the metadata map attached to the cancel-path Done envelope.
func (s *Session) doneMetaForCancel() map[string]any {
	s.latestSessionIDMu.Lock()
	id := s.latestSessionID
	s.latestSessionIDMu.Unlock()
	if id == "" {
		return nil
	}
	return map[string]any{
		proto.DoneMetaAgentSessionID:   id,
		proto.DoneMetaAgentSessionType: "claude_session",
	}
}

func (s *Session) trySend(env proto.Envelope) {
	s.outMu.RLock()
	defer s.outMu.RUnlock()
	if s.outClosed {
		return
	}
	select {
	case s.out <- env:
	case <-time.After(2 * time.Second):
		s.cfg.logger.Warn("claudecode: terminal send timed out",
			"type", env.Type, "run_id", s.runID)
	}
}

func (s *Session) closeOut() {
	s.closeOutOnce.Do(func() {
		s.outMu.Lock()
		s.outClosed = true
		close(s.out)
		s.outMu.Unlock()
	})
}

// resolveSessionWorkDir returns the directory that BOTH plugin installs
// AND the claude_code subprocess cwd share for this run. Keeping them
// on the same tree prevents the bug where the subprocess ran in one
// place (sandbox image WORKDIR) while plugins sat under ~/.parsar/
// — `--plugin-dir` still worked but the agent's own `ls .claude/
// plugins/` self-check answered "no plugins here".
//
// Resolution order:
//
//  1. req.WorkDir wins. Local mode where the operator pinned a project
//     root. Must be absolute (we reject relative paths instead of
//     resolving them against daemon cwd, since the daemon's cwd is
//     not a meaningful anchor for user-facing config) and we mkdir -p
//     so the user can name a path that doesn't exist yet.
//  2. conversationID present → per-conversation scratch dir under
//     daemon HOME (~/.parsar/runtime/claudecode/conv-<id>).
//     Consecutive turns reuse the same .cache-key files. Sandbox-mode
//     default, also the local fallback when work_dir is unbound.
//  3. Both empty → daemon's own cwd (os.Getwd). Backstop matching
//     pre-plugin behavior.
//
// Errors propagate so the eventual "could not extract zip" gets a
// clearer message.
func resolveSessionWorkDir(workDir, conversationID string) (string, error) {
	if trimmed := strings.TrimSpace(workDir); trimmed != "" {
		if !filepath.IsAbs(trimmed) {
			return "", fmt.Errorf("work_dir must be an absolute path, got %q", trimmed)
		}
		if err := os.MkdirAll(trimmed, 0o755); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", trimmed, err)
		}
		return trimmed, nil
	}
	if convID := strings.TrimSpace(conversationID); convID != "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("os.UserHomeDir: %w", err)
		}
		dir := filepath.Join(home, ".parsar", "runtime", "claudecode", "conv-"+convID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("mkdir %s: %w", dir, err)
		}
		return dir, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("os.Getwd: %w", err)
	}
	return cwd, nil
}
