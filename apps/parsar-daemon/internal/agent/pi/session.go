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
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/clirunner"
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

	// Materialise pi's managed config and pin --session-dir to the stable
	// conversation/agent/engine state key so --session can resolve reliably.
	provOpts, provErr := applyPiRuntimeState(opts, req.AgentStateKey, req.ConversationID, req.RunID)
	if provErr != nil {
		return nil, fmt.Errorf("pi: apply managed provider: %w", provErr)
	}
	opts = provOpts

	buildRes, err := BuildArgs(req.RunID, req.Prompt, req.WorkDir, opts, req.AgentSessionID)
	if err != nil {
		return nil, fmt.Errorf("pi: build args: %w", err)
	}
	args := append([]string{}, buildRes.Args...)
	args = append(args, cfg.extraArgs...)
	dir := ""
	if buildRes.WorkDir != "" {
		dir = buildRes.WorkDir
	}
	proc, err := clirunner.Start(clirunner.StartOptions{
		Parent:      parent,
		Binary:      cfg.piBinary,
		Args:        args,
		Dir:         dir,
		Env:         append(os.Environ(), buildRes.Env...),
		KillTimeout: cfg.killTimeout,
	})
	if err != nil {
		buildRes.Cleanup()
		return nil, fmt.Errorf("pi: start %q: %w", cfg.piBinary, err)
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
		tx, err := tr.Translate(sc.Bytes())
		if err != nil {
			s.cfg.logger.Warn("pi: translate line", "run_id", s.runID, "err", err)
			continue
		}
		for _, env := range tx.Envelopes {
			select {
			case s.out <- env:
			case <-s.cancelCtx.Done():
				_ = s.proc.Wait()
				return
			}
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		s.cfg.logger.Warn("pi: scan stdout", "run_id", s.runID, "err", err)
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
