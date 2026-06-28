// Package pi is the agent_kind="pi" adapter. It drives the pi CLI via
// `pi --mode json -p <prompt>`, translating pi's NDJSON event stream on
// stdout into proto.Envelope frames for the dispatch router.
package pi

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
	piBinary    string
	extraArgs   []string
	killTimeout time.Duration
	logger      *slog.Logger
}

func defaultConfig() sessionConfig {
	return sessionConfig{piBinary: defaultBinary, killTimeout: 3 * time.Second, logger: obslog.Bg()}
}

// Factory implements agent.Factory for agent_kind="pi".
func Factory(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
	return newSession(ctx, req, out, defaultConfig())
}

// Session wraps a single `pi --mode json` subprocess.
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
		return nil, errors.New("pi: nil out channel")
	}
	if cfg.logger == nil {
		cfg.logger = obslog.Bg()
	}
	if cfg.piBinary == "" {
		cfg.piBinary = defaultBinary
	}
	if cfg.killTimeout <= 0 {
		cfg.killTimeout = 3 * time.Second
	}

	// pi needs an explicit --skill flag per skill (unlike Claude Code's
	// auto-scan), so installSkills returns dirs even on a cache hit.
	opts := req.AgentOptions
	if rawSkills, ok := opts["skills"]; ok {
		descriptors, decodeWarns := decodeSkillDescriptors(rawSkills)
		for _, w := range decodeWarns {
			cfg.logger.Warn("pi: skill descriptor decode warning", "run_id", req.RunID, "msg", w)
		}
		root, rErr := resolveSkillsRoot(req.ConversationID, req.RunID)
		if rErr != nil {
			return nil, fmt.Errorf("pi: resolve skills root: %w", rErr)
		}
		installRes, iErr := installSkills(parent, cfg.logger, root, descriptors)
		if iErr != nil {
			return nil, fmt.Errorf("pi: install skills: %w", iErr)
		}
		for _, w := range installRes.Warnings {
			cfg.logger.Warn("pi: skill install warning", "run_id", req.RunID, "msg", w)
		}
		if len(installRes.SkillDirs) > 0 {
			opts = cloneAgentOptions(req.AgentOptions)
			opts["skill_dirs"] = mergeSkillDirs(opts["skill_dirs"], installRes.SkillDirs)
		}
	}

	// Without this, pi never sees the proxy base_url + X-Sub-Module header and
	// sends the managed key to api.anthropic.com → 401. Writes models.json and
	// sets PI_CODING_AGENT_DIR in opts["env"] (buildEnv forwards it).
	provOpts, provErr := applyPiManagedProvider(opts, req.ConversationID, req.RunID)
	if provErr != nil {
		return nil, fmt.Errorf("pi: apply managed provider: %w", provErr)
	}
	opts = provOpts

	buildRes, err := BuildArgs(req.RunID, req.Prompt, req.WorkDir, opts, req.ResumeSessionID)
	if err != nil {
		return nil, fmt.Errorf("pi: build args: %w", err)
	}
	cancelCtx, cancelFn := context.WithCancel(parent)

	args := append([]string{}, buildRes.Args...)
	args = append(args, cfg.extraArgs...)
	cmd := exec.CommandContext(cancelCtx, cfg.piBinary, args...)
	// pi has no working-directory flag, so the resolved WorkDir becomes
	// the subprocess cwd.
	if buildRes.WorkDir != "" {
		cmd.Dir = buildRes.WorkDir
	}
	cmd.Env = append(os.Environ(), buildRes.Env...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancelFn()
		buildRes.Cleanup()
		return nil, fmt.Errorf("pi: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancelFn()
		buildRes.Cleanup()
		return nil, fmt.Errorf("pi: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancelFn()
		buildRes.Cleanup()
		return nil, fmt.Errorf("pi: start %q: %w", cfg.piBinary, err)
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
			s.cfg.logger.Warn("pi: translate line", "run_id", s.runID, "err", err)
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
		s.cfg.logger.Warn("pi: scan stdout", "run_id", s.runID, "err", err)
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
		s.cfg.logger.Warn("pi stderr", "run_id", s.runID, "line", line)
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
		s.cfg.logger.Warn("pi: terminal send timed out", "type", env.Type, "run_id", s.runID)
	}
}

func (s *Session) closeOut() { s.closeOutOnce.Do(func() { close(s.out) }) }
