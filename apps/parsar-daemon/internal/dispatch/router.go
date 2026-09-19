// Package dispatch wires inbound WebSocket frames to the agent layer.
// It owns one Session per active RunID, a per-session pump goroutine
// that forwards the agent's events to the transport, and a
// permission_id → run_id index so permission_decision frames route
// back to the right session.
//
// Concurrency: Handle is safe for one goroutine (typically the read
// loop). Each session runs its own goroutine. Internal state is
// mutex-protected.
package dispatch

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// Sender is the subset of transport.Conn the dispatcher needs. Tests
// supply a fake; production wires this to *transport.Conn.Send.
type Sender interface {
	Send(ctx context.Context, env proto.Envelope) error
}

// Router maps inbound Envelopes to agent sessions.
type Router struct {
	registry *agent.Registry
	sender   Sender
	log      *slog.Logger

	mu                  sync.Mutex
	sessions            map[string]*sessionState // RunID → state
	idle                map[string]map[*sessionState]struct{}
	permIndex           map[string]string // permID  → RunID
	askIndex            map[string]string // askID   → RunID
	applied             map[string]appliedInteractionDecision
	shutdownAttempt     *shutdownAttempt
	shutdownCh          chan struct{}  // closed by Shutdown
	shutdownWG          sync.WaitGroup // waits for all pump goroutines
	idleTimeout         time.Duration
	closed              bool
	preparations        map[string]*preparationState
	preparationRequests map[string]*preparationState
	preparationTimeout  time.Duration
	workspaceWrite      *workspaceUpload
	workspaceExport     *workspaceExport
	workspaceReads      map[string]struct{}
	localWorkspace      *localworkspace.Binding
}

type appliedInteractionDecision struct {
	requestID   string
	kind        string
	fingerprint [32]byte
	recordedAt  time.Time
}

// sessionState is the dispatcher's per-run bookkeeping. The agent
// owns the close of out; the dispatcher cancels ctxCancel to wind
// down. traceparent captures the prompt_request's W3C trace so every
// outbound frame stamps env.Trace with the same value, completing
// frontend → server → daemon → agent → server attribution.
type sessionState struct {
	runID               string
	environmentID       string
	stateKey            string
	session             agent.Session
	out                 chan proto.Envelope
	ctx                 context.Context
	ctxCancel           context.CancelFunc
	pendingIDs          map[string]struct{}
	pendingAsks         map[string]struct{}
	traceparent         string
	idleTimer           *time.Timer
	idleLease           uint64
	retain              bool
	releaseOnCompletion bool
	steering            map[string]steeringReceipt
	steerBusy           bool
	steeringClosed      bool
	steeringDone        chan struct{}
	preparedHandoff     *preparedHandoff
}

// Config is the constructor input. Registry and Sender are required;
// Log is optional (defaults to slog.Default()).
type Config struct {
	Registry           *agent.Registry
	Sender             Sender
	Log                *slog.Logger
	IdleTimeout        time.Duration
	PreparationTimeout time.Duration
	LocalWorkspace     *localworkspace.Binding
}

const defaultIdleTimeout = time.Hour

// New returns a Router ready to Handle inbound frames.
func New(cfg Config) (*Router, error) {
	if cfg.Registry == nil {
		return nil, errors.New("dispatch.New: Registry is required")
	}
	if cfg.Sender == nil {
		return nil, errors.New("dispatch.New: Sender is required")
	}
	log := cfg.Log
	if log == nil {
		log = obslog.Bg()
	}
	log = log.With("component", "dispatch")
	idleTimeout := cfg.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleTimeout
	}
	preparationTimeout := cfg.PreparationTimeout
	if preparationTimeout <= 0 || preparationTimeout > 5*time.Minute {
		preparationTimeout = 5 * time.Minute
	}
	return &Router{
		registry:            cfg.Registry,
		sender:              cfg.Sender,
		log:                 log,
		sessions:            make(map[string]*sessionState),
		idle:                make(map[string]map[*sessionState]struct{}),
		permIndex:           make(map[string]string),
		askIndex:            make(map[string]string),
		applied:             make(map[string]appliedInteractionDecision),
		shutdownCh:          make(chan struct{}),
		idleTimeout:         idleTimeout,
		preparations:        make(map[string]*preparationState),
		preparationRequests: make(map[string]*preparationState),
		preparationTimeout:  preparationTimeout,
		localWorkspace:      cfg.LocalWorkspace,
	}, nil
}

// Handle dispatches one inbound Envelope. Errors are returned for
// programmer-visible problems (bad shape, registry miss); transient
// session-level failures are logged and swallowed.
//
// Adopts env.Trace into ctx so every downstream log under it inherits
// the same trace_id, making a single grep cover both sides.
func (r *Router) Handle(ctx context.Context, env proto.Envelope) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRouterClosed
	}
	r.mu.Unlock()

	ctx = adoptEnvelopeTrace(ctx, env)

	switch env.Type {
	case proto.TypeWorkspaceExport:
		return r.handleWorkspaceExport(ctx, env)
	case proto.TypeWorkspaceWrite:
		return r.handleWorkspaceWrite(ctx, env)
	case proto.TypeWorkspaceRead:
		return r.handleWorkspaceRead(ctx, env)
	case proto.TypeExecutionPrepare:
		return r.handleExecutionPrepare(ctx, env)
	case proto.TypeExecutionStart:
		return r.handleExecutionStart(ctx, env)
	case proto.TypeExecutionRelease:
		return r.handleExecutionRelease(ctx, env)
	case proto.TypePromptRequest:
		return r.handlePromptRequest(ctx, env)
	case proto.TypePromptCancel:
		return r.handlePromptCancel(ctx, env)
	case proto.TypeFunctionResult:
		return r.handleFunctionResult(ctx, env)
	case proto.TypePromptSteer:
		return r.handlePromptSteer(ctx, env)
	case proto.TypePermissionDecision:
		return r.handlePermissionDecision(ctx, env)
	case proto.TypePromptForUserChoiceDecision:
		return r.handlePromptForUserChoiceDecision(ctx, env)
	case proto.TypeDeviceShutdown:
		return r.handleDeviceShutdown(ctx, env)
	default:
		// Unknown types are logged and dropped — keeps the daemon
		// forward-compatible with server-side additions.
		r.log.WarnContext(ctx, "dropping unknown envelope type", "type", env.Type, "id", env.ID)
		return nil
	}
}

// adoptEnvelopeTrace returns a ctx carrying env.Trace's carrier. On
// empty / unparseable trace we mint a fresh one rather than propagate
// a bad trace_id back to the server.
func adoptEnvelopeTrace(ctx context.Context, env proto.Envelope) context.Context {
	if env.Trace != "" {
		if carrier, err := obslog.ParseTraceparent(env.Trace); err == nil {
			return obslog.WithTrace(ctx, carrier)
		}
	}
	ctx, _ = obslog.StartBackgroundTrace(ctx, "daemon.envelope")
	return ctx
}

// ActiveRuns returns the in-flight run count. Wired into the heartbeat
// payload supplier.
func (r *Router) ActiveRuns() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}

var ErrRouterClosed = errors.New("dispatch: router closed")

// ---------------------------------------------------------------------
// per-type handlers
// ---------------------------------------------------------------------

func (r *Router) drain(ch <-chan proto.Envelope) {
	for range ch {
	}
}

// cleanupSession removes the session from registry maps. Called from
// pump's defer so it runs exactly once.
func (r *Router) cleanupSession(s *sessionState) {
	r.mu.Lock()
	delete(r.sessions, s.runID)
	for permID := range s.pendingIDs {
		delete(r.permIndex, permID)
	}
	for askID := range s.pendingAsks {
		delete(r.askIndex, askID)
	}
	if !s.retain || s.session == nil || s.stateKey == "" || r.closed {
		r.mu.Unlock()
		return
	}
	states := r.idle[s.stateKey]
	if states == nil {
		states = make(map[*sessionState]struct{})
		r.idle[s.stateKey] = states
	}
	states[s] = struct{}{}
	r.scheduleIdleLocked(s)
	r.mu.Unlock()
}

func sessionStateKey(req proto.PromptRequestPayload) string {
	return req.AgentStateKey
}

func (r *Router) touchIdleLocked(stateKey string) {
	if stateKey == "" {
		return
	}
	for state := range r.idle[stateKey] {
		r.scheduleIdleLocked(state)
	}
}

func (r *Router) scheduleIdleLocked(state *sessionState) {
	if state.idleTimer != nil {
		state.idleTimer.Stop()
	}
	state.idleLease++
	lease := state.idleLease
	state.idleTimer = time.AfterFunc(r.idleTimeout, func() {
		r.expireIdle(state, lease)
	})
}

func (r *Router) expireIdle(state *sessionState, lease uint64) {
	r.mu.Lock()
	states := r.idle[state.stateKey]
	if _, ok := states[state]; !ok || state.idleLease != lease {
		r.mu.Unlock()
		return
	}
	delete(states, state)
	if len(states) == 0 {
		delete(r.idle, state.stateKey)
	}
	state.retain = false
	r.mu.Unlock()

	state.ctxCancel()
	if err := state.session.Cancel(context.Background()); err != nil {
		r.log.Warn("idle session cancel failed", "run_id", state.runID, "state_key", state.stateKey, "err", err)
	}
}

// emitTerminalError synthesises error + done for a run that couldn't
// even be started. Stamps env.Trace from ctx so the gateway can
// attribute these frames to the same trace_id.
func (r *Router) emitTerminalError(ctx context.Context, runID, msg string) {
	traceparent := ""
	if carrier, ok := obslog.TraceFromContext(ctx); ok {
		traceparent = carrier.String()
	}
	errEnv, err := proto.NewEnvelopeWithTrace(proto.TypeError, runID, proto.ErrorPayload{Error: msg}, traceparent)
	if err == nil {
		if sendErr := r.sender.Send(ctx, errEnv); sendErr != nil {
			r.log.ErrorContext(ctx, "emit terminal error frame failed", "run_id", runID, "err", sendErr)
		}
	}
	doneEnv, err := proto.NewEnvelopeWithTrace(proto.TypeDone, runID, proto.DonePayload{}, traceparent)
	if err == nil {
		if sendErr := r.sender.Send(ctx, doneEnv); sendErr != nil {
			r.log.ErrorContext(ctx, "emit terminal done frame failed", "run_id", runID, "err", sendErr)
		}
	}
}
