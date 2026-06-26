// Package opencode is the agent_kind="opencode" adapter. It drives the
// OpenCode CLI via `opencode run --format json`.
package opencode

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

type sessionConfig struct {
	opencodeBinary string
	extraArgs      []string
	killTimeout    time.Duration
	logger         *slog.Logger
}

func defaultConfig() sessionConfig {
	return sessionConfig{opencodeBinary: defaultBinary, killTimeout: 3 * time.Second, logger: obslog.Bg()}
}

// Factory implements agent.Factory for agent_kind="opencode".
func Factory(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
	return newSession(ctx, req, out, defaultConfig())
}

// Session wraps a single `opencode run` subprocess.
type Session struct {
	runID string
	cfg   sessionConfig

	cmd *exec.Cmd
	out chan<- proto.Envelope

	cancelCtx context.Context
	cancelFn  context.CancelFunc

	cancelOnce   sync.Once
	closeOutOnce sync.Once
	waitDone     chan struct{}
	cleanup      func()

	stderrMu sync.Mutex
	stderr   bytes.Buffer
}

var _ agent.Session = (*Session)(nil)

func newSession(parent context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope, cfg sessionConfig) (*Session, error) {
	if out == nil {
		return nil, errors.New("opencode: nil out channel")
	}
	if cfg.logger == nil {
		cfg.logger = obslog.Bg()
	}
	if cfg.opencodeBinary == "" {
		cfg.opencodeBinary = defaultBinary
	}
	if cfg.killTimeout <= 0 {
		cfg.killTimeout = 3 * time.Second
	}

	buildRes, err := BuildArgs(req.RunID, req.Prompt, req.WorkDir, req.AgentOptions)
	if err != nil {
		return nil, fmt.Errorf("opencode: build args: %w", err)
	}
	cancelCtx, cancelFn := context.WithCancel(parent)

	args := append([]string{}, buildRes.Args...)
	args = append(args, cfg.extraArgs...)
	cmd := exec.CommandContext(cancelCtx, cfg.opencodeBinary, args...)
	if buildRes.WorkDir != "" {
		cmd.Dir = buildRes.WorkDir
	}
	cmd.Env = append(os.Environ(), buildRes.Env...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancelFn()
		buildRes.Cleanup()
		return nil, fmt.Errorf("opencode: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancelFn()
		buildRes.Cleanup()
		return nil, fmt.Errorf("opencode: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancelFn()
		buildRes.Cleanup()
		return nil, fmt.Errorf("opencode: start %q: %w", cfg.opencodeBinary, err)
	}

	s := &Session{
		runID:     req.RunID,
		cfg:       cfg,
		cmd:       cmd,
		out:       out,
		cancelCtx: cancelCtx,
		cancelFn:  cancelFn,
		waitDone:  make(chan struct{}),
		cleanup:   buildRes.Cleanup,
	}
	go s.pumpStderr(stderr)
	go s.run(stdout)
	return s, nil
}

func (s *Session) Cancel(context.Context) error {
	s.cancelOnce.Do(func() {
		if s.cmd.Process == nil {
			return
		}
		_ = s.cmd.Process.Signal(syscall.SIGTERM)
		go func() {
			select {
			case <-s.waitDone:
				return
			case <-time.After(s.cfg.killTimeout):
				_ = s.cmd.Process.Signal(syscall.SIGKILL)
			}
		}()
		s.cancelFn()
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
	defer close(s.waitDone)
	defer s.cleanup()
	defer s.closeOut()

	tr := newTranslator(s.runID)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		tx, err := tr.Translate(sc.Bytes())
		if err != nil {
			s.cfg.logger.Warn("opencode: translate line", "run_id", s.runID, "err", err)
			continue
		}
		for _, env := range tx.Envelopes {
			select {
			case s.out <- env:
			case <-s.cancelCtx.Done():
				_ = s.cmd.Wait()
				return
			}
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		s.cfg.logger.Warn("opencode: scan stdout", "run_id", s.runID, "err", err)
	}

	waitErr := s.cmd.Wait()
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
		s.cfg.logger.Warn("opencode stderr", "run_id", s.runID, "line", line)
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
		s.cfg.logger.Warn("opencode: terminal send timed out", "type", env.Type, "run_id", s.runID)
	}
}

func (s *Session) closeOut() { s.closeOutOnce.Do(func() { close(s.out) }) }
