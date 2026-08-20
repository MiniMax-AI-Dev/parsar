// Package deepseekharness is the agent_kind="deepseek_harness" adapter.
// It drives DeepSeek Harness through its one-shot surface,
// `dsh --profile headless <task>`, which prints the final assistant text
// on stdout and exits non-zero for any turn that did not complete.
//
// The harness exposes no supported machine-readable event stream, resume
// flag, or approval channel for that surface, so this adapter advertises
// neither streaming, usage, resume nor permissions: one prompt is one
// fresh dsh session.
package deepseekharness

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// unsupportedOptions are agent_options Parsar renders for other engines
// that the dsh headless profile has no seam for. They are logged rather
// than dropped silently so an operator can see why a configured
// capability had no effect.
var unsupportedOptions = []string{"mcp_servers", "skills", "skill_dirs", "plugin_dirs"}

type sessionConfig struct {
	binary      string
	extraArgs   []string
	killTimeout time.Duration
	logger      *slog.Logger
}

func defaultConfig() sessionConfig {
	return sessionConfig{binary: defaultBinary, killTimeout: 3 * time.Second, logger: obslog.Bg()}
}

// Factory implements agent.Factory for agent_kind="deepseek_harness".
//
// It picks between the two dsh surfaces by where the daemon runs, because
// the resident-server surface is only acceptable in a sandbox:
//
//   - Sandbox: a resident `dsh --profile parsar-api` bound to a loopback
//     port inside the container. The port does not leave the container, so
//     the gateway's "loopback callers are trusted" model is contained by
//     the sandbox boundary. This surface streams and resumes.
//   - Local device: the one-shot headless CLI. dsh's web server has no
//     authentication of any kind, so opening a port on a developer's own
//     machine would expose an agent runtime with filesystem access to
//     every local process. Continuity on this surface comes from the
//     server injecting prior turns.
func Factory(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
	if RunsResidentServer() {
		return newServerSession(ctx, req, out, defaultConfig())
	}
	return newSession(ctx, req, out, defaultConfig())
}

// RunsResidentServer reports whether this daemon should drive dsh through
// a resident /api server. IS_SANDBOX is set by the sandbox image, so a
// local install never trips it by accident.
func RunsResidentServer() bool {
	return strings.TrimSpace(os.Getenv(sandboxMarkerEnvVar)) != ""
}

// sandboxMarkerEnvVar is baked into the Parsar sandbox image.
const sandboxMarkerEnvVar = "IS_SANDBOX"

// Session wraps a single `dsh --profile headless` subprocess.
type Session struct {
	runID string
	cfg   sessionConfig

	proc *clirunner.Process
	out  chan<- proto.Envelope

	cancelCtx context.Context

	cancelOnce   sync.Once
	closeOutOnce sync.Once
	cleanup      func()

	stderrMu sync.Mutex
	stderr   bytes.Buffer
}

var _ agent.Session = (*Session)(nil)

func newSession(parent context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope, cfg sessionConfig) (*Session, error) {
	if out == nil {
		return nil, errors.New("deepseekharness: nil out channel")
	}
	if cfg.logger == nil {
		cfg.logger = obslog.Bg()
	}
	if cfg.binary == "" {
		cfg.binary = defaultBinary
	}
	if cfg.killTimeout <= 0 {
		cfg.killTimeout = 3 * time.Second
	}
	for _, key := range unsupportedOptions {
		if value, ok := req.AgentOptions[key]; ok && value != nil {
			cfg.logger.Warn("deepseekharness: agent option unsupported by dsh headless, ignored",
				"run_id", req.RunID, "option", key)
		}
	}

	buildRes, err := BuildArgs(req.Prompt, req.WorkDir, req.AgentOptions, StateKeys{
		AgentStateKey:  req.AgentStateKey,
		ConversationID: req.ConversationID,
		RunID:          req.RunID,
	})
	if err != nil {
		return nil, fmt.Errorf("deepseekharness: build args: %w", err)
	}
	args := append([]string{}, buildRes.Args...)
	args = append(args, cfg.extraArgs...)
	proc, err := clirunner.Start(clirunner.StartOptions{
		Parent:      parent,
		Binary:      cfg.binary,
		Args:        args,
		Dir:         buildRes.WorkDir,
		Env:         append(os.Environ(), buildRes.Env...),
		KillTimeout: cfg.killTimeout,
	})
	if err != nil {
		buildRes.Cleanup()
		return nil, fmt.Errorf("deepseekharness: start %q: %w", cfg.binary, err)
	}

	s := &Session{
		runID:     req.RunID,
		cfg:       cfg,
		proc:      proc,
		out:       out,
		cancelCtx: proc.Context(),
		cleanup:   buildRes.Cleanup,
	}
	go s.pumpStderr(proc.Stderr)
	go s.run(proc.Stdout)
	return s, nil
}

func (s *Session) Cancel(context.Context) error {
	s.cancelOnce.Do(func() {
		s.proc.Cancel()
	})
	return nil
}

func (s *Session) SubmitPermission(context.Context, string, proto.PermissionDecisionPayload) error {
	return agent.ErrUnknownPermission
}

func (s *Session) SubmitPromptForUserChoice(context.Context, string, proto.PromptForUserChoiceDecisionPayload) error {
	return agent.ErrUnknownAsk
}

func (s *Session) run(stdout io.Reader) {
	defer s.cleanup()
	defer s.closeOut()

	tr := newTranslator(s.runID)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		tr.appendLine(sc.Text())
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		s.cfg.logger.Warn("deepseekharness: scan stdout", "run_id", s.runID, "err", err)
	}

	waitErr := s.proc.Wait()
	for _, env := range tr.terminalEnvelopes(waitErr, s.stderrString(), s.cancelCtx.Err() != nil) {
		s.trySend(env)
	}
}

func (s *Session) pumpStderr(stderr io.Reader) {
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 0, 16*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		s.stderrMu.Lock()
		if s.stderr.Len() > 0 {
			s.stderr.WriteByte('\n')
		}
		s.stderr.WriteString(line)
		s.stderrMu.Unlock()
		s.cfg.logger.Warn("dsh stderr", "run_id", s.runID, "line", line)
	}
}

func (s *Session) stderrString() string {
	s.stderrMu.Lock()
	defer s.stderrMu.Unlock()
	return s.stderr.String()
}

func (s *Session) trySend(env proto.Envelope) {
	select {
	case s.out <- env:
	case <-time.After(2 * time.Second):
		s.cfg.logger.Warn("deepseekharness: terminal send timed out", "type", env.Type, "run_id", s.runID)
	}
}

func (s *Session) closeOut() { s.closeOutOnce.Do(func() { close(s.out) }) }
