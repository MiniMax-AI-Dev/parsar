package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// Prepare connects the native harness without creating a thread or starting model
// work. req supplies configuration and a stable state key, but no RunID or Prompt.
// owner owns the entire harness lifetime, including the eventual Session; it must
// not be a disposable readiness-request context. Initialization/readiness use their
// existing operation-local deadlines. Close an unused preparation explicitly.
func Prepare(owner context.Context, req proto.PromptRequestPayload) (*Prepared, error) {
	return newPreparation(owner, req, defaultSessionConfig())
}

func newSession(parent context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope, cfg sessionConfig) (*Session, error) {
	if out == nil {
		return nil, errors.New("codex: nil out channel")
	}
	runID, prompt := req.RunID, req.Prompt
	req.AgentStateKey = effectiveAgentStateKey(req)
	req.RunID, req.Prompt = "", ""
	prepared, err := newPreparation(parent, req, cfg)
	if err != nil {
		return nil, err
	}
	defer prepared.Close()
	return prepared.start(parent, runID, prompt, out)
}

func newPreparation(parent context.Context, req proto.PromptRequestPayload, cfg sessionConfig) (*Prepared, error) {
	if req.RunID != "" || req.Prompt != "" {
		return nil, errors.New("codex: preparation does not accept a run identity or prompt")
	}
	if cfg.logger == nil {
		cfg.logger = obslog.Bg()
	}
	if cfg.codexBinary == "" {
		cfg.codexBinary = defaultBinary()
	}
	if cfg.killTimeout <= 0 {
		cfg.killTimeout = rpcKillTimeout
	}
	functions, err := prepareFunctionTools(req.FunctionTools)
	if err != nil {
		return nil, err
	}
	if err := validateRemoteEnvironmentRequest(req); err != nil {
		return nil, err
	}
	req.AgentStateKey = effectiveAgentStateKey(req)

	req.AgentOptions = executionOptions(req)
	plan, skillRoot, err := prepareSessionPlan(parent, req, cfg)
	if err != nil {
		return nil, err
	}

	cancelCtx, cancelFn := context.WithCancel(parent)

	rpcCfg := JSONRPCConfig{
		Binary:          cfg.codexBinary,
		EnableFeatures:  plan.EnableFeatures,
		DisableFeatures: plan.DisableFeatures,
		Cwd:             plan.Cwd,
		Env:             append(os.Environ(), plan.Env...),
		LogTag:          "codex-preparation",
		Logger:          cfg.logger,
	}
	for _, kv := range plan.ExtraConfig {
		rpcCfg.ExtraArgs = append(rpcCfg.ExtraArgs, "-c", kv[0]+"="+kv[1])
	}
	harness, err := configurePrivateHarness(&rpcCfg, cfg.harnessBinary, req.RemoteEnvironment)
	if err != nil {
		cancelFn()
		plan.Cleanup()
		return nil, err
	}

	rpc := NewJSONRPCClient(rpcCfg)
	defer harness.releaseWith(rpc)

	s := &Session{
		functions:                 functions,
		observeMessages:           req.ObserveMessages,
		observeTools:              req.ObserveTools,
		observeToolObservations:   req.ObserveToolObservations,
		observeSubagentIdentities: req.ObserveSubagentIdentities && !req.DisableSubagents,
		cfg:                       cfg,
		rpc:                       rpc,
		harness:                   harness,
		cancelCtx:                 cancelCtx,
		cancelFn:                  cancelFn,
		waitDone:                  make(chan struct{}),
		cleanup:                   sync.OnceFunc(plan.Cleanup),
		bufs:                      NewItemBuffers(),
		resolvedModel:             plan.Model,
		interactions:              newPendingCodexInteractions(),
	}
	plan.Cleanup = s.cleanup

	initParams := InitializeParams{
		ClientInfo:   InitializeClientInfo{Name: "parsar-daemon", Version: "0.0.0"},
		Capabilities: &InitializeCapabilities{ExperimentalAPI: true},
	}
	if _, err := rpc.Start(cancelCtx, initParams); err != nil {
		cancelFn()
		plan.Cleanup()
		return nil, fmt.Errorf("codex: rpc start: %w", err)
	}
	if err := harness.verify(); err != nil {
		cancelFn()
		_ = rpc.Close()
		plan.Cleanup()
		return nil, err
	}
	if req.DisableExecutionEnvironment {
		if err := verifyNoExecutionEnvironment(cancelCtx, rpc); err != nil {
			cancelFn()
			_ = rpc.Close()
			plan.Cleanup()
			return nil, err
		}
	}
	if req.RemoteEnvironment != nil {
		if err := verifyRemoteEnvironment(cancelCtx, rpc); err != nil {
			cancelFn()
			_ = rpc.Close()
			plan.Cleanup()
			return nil, err
		}
	}
	if plan.mcpHTTPServers != nil {
		if err := verifyMCPHTTPConfig(cancelCtx, rpc, plan); err != nil {
			cancelFn()
			_ = rpc.Close()
			plan.Cleanup()
			return nil, err
		}
	}
	if skillRoot != "" {
		if err := setSkillExtraRoots(cancelCtx, rpc, []string{skillRoot}); err != nil {
			cancelFn()
			_ = rpc.Close()
			plan.Cleanup()
			return nil, fmt.Errorf("codex: register skill root: %w", err)
		}
	}

	p := &Prepared{
		session: s, plan: plan, remote: req.RemoteEnvironment != nil,
		resumeID: req.AgentSessionID, strictResume: req.StrictResume,
		transferred: make(chan struct{}),
	}
	go p.watchOwner()
	return p, nil
}
