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
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
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

	mu          sync.Mutex
	sessions    map[string]*sessionState // RunID → state
	idle        map[string]map[*sessionState]struct{}
	permIndex   map[string]string // permID  → RunID
	askIndex    map[string]string // askID   → RunID
	shutdownCh  chan struct{}     // closed by Shutdown
	shutdownWG  sync.WaitGroup    // waits for all pump goroutines
	idleTimeout time.Duration
	closed      bool
}

// sessionState is the dispatcher's per-run bookkeeping. The agent
// owns the close of out; the dispatcher cancels ctxCancel to wind
// down. traceparent captures the prompt_request's W3C trace so every
// outbound frame stamps env.Trace with the same value, completing
// frontend → server → daemon → agent → server attribution.
type sessionState struct {
	runID       string
	stateKey    string
	session     agent.Session
	out         chan proto.Envelope
	ctxCancel   context.CancelFunc
	pendingIDs  map[string]struct{}
	pendingAsks map[string]struct{}
	traceparent string
	idleTimer   *time.Timer
	idleLease   uint64
	retain      bool
}

// Config is the constructor input. Registry and Sender are required;
// Log is optional (defaults to slog.Default()).
type Config struct {
	Registry    *agent.Registry
	Sender      Sender
	Log         *slog.Logger
	IdleTimeout time.Duration
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
	return &Router{
		registry:    cfg.Registry,
		sender:      cfg.Sender,
		log:         log,
		sessions:    make(map[string]*sessionState),
		idle:        make(map[string]map[*sessionState]struct{}),
		permIndex:   make(map[string]string),
		askIndex:    make(map[string]string),
		shutdownCh:  make(chan struct{}),
		idleTimeout: idleTimeout,
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
	case proto.TypePromptRequest:
		return r.handlePromptRequest(ctx, env)
	case proto.TypePromptCancel:
		return r.handlePromptCancel(ctx, env)
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

// Shutdown cancels every active session and waits for pumps to drain.
// Idempotent. Cancels ctx AND calls Session.Cancel — agents that
// don't watch ctx (e.g. subprocess wrappers that need SIGTERM via
// Cancel) leave their out channel open otherwise and the pump's
// drain blocks forever.
func (r *Router) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	close(r.shutdownCh)
	victims := make([]*sessionState, 0, len(r.sessions))
	for _, s := range r.sessions {
		s.retain = false
		victims = append(victims, s)
	}
	for _, states := range r.idle {
		for s := range states {
			s.retain = false
			if s.idleTimer != nil {
				s.idleTimer.Stop()
			}
			victims = append(victims, s)
		}
	}
	r.idle = make(map[string]map[*sessionState]struct{})
	r.mu.Unlock()

	cancelSessions(ctx, victims, r.log)

	done := make(chan struct{})
	go func() {
		r.shutdownWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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

func (r *Router) handlePromptRequest(callerCtx context.Context, env proto.Envelope) error {
	r.log.InfoContext(callerCtx, "handlePromptRequest: decoding payload", "env_id", env.ID, "env_type", env.Type)
	var req proto.PromptRequestPayload
	if err := env.DecodePayload(&req); err != nil {
		r.log.ErrorContext(callerCtx, "handlePromptRequest: decode failed", "env_id", env.ID, "err", err)
		return fmt.Errorf("dispatch: decode prompt_request: %w", err)
	}
	// Envelope.ID is the run id; payload mirrors it but envelope wins.
	runID := env.ID
	if runID == "" {
		runID = req.RunID
	}
	if runID == "" {
		r.log.ErrorContext(callerCtx, "handlePromptRequest: missing run id")
		return errors.New("dispatch: prompt_request missing run id (Envelope.ID and Payload.RunID both empty)")
	}
	req.RunID = runID
	if req.AgentKind == "" {
		r.log.ErrorContext(callerCtx, "handlePromptRequest: missing agent_kind", "run_id", runID)
		return errors.New("dispatch: prompt_request missing agent_kind")
	}
	r.log.InfoContext(callerCtx, "handlePromptRequest: decoded",
		"run_id", runID, "agent_kind", req.AgentKind,
		"work_dir", req.WorkDir, "prompt_len", len(req.Prompt),
		"has_agent_options", req.AgentOptions != nil,
		"agent_session_id", req.AgentSessionID,
		"agent_state_key", req.AgentStateKey)

	factory, err := r.registry.Resolve(req.AgentKind)
	if err != nil {
		r.log.ErrorContext(callerCtx, "handlePromptRequest: registry.Resolve failed", "run_id", runID, "agent_kind", req.AgentKind, "err", err)
		// Synthesize error+done so the server-side stream closes
		// cleanly instead of waiting for a done that never comes.
		r.emitTerminalError(callerCtx, runID, fmt.Sprintf("unsupported agent_kind %q on this daemon", req.AgentKind))
		return err
	}
	r.log.InfoContext(callerCtx, "handlePromptRequest: factory resolved", "run_id", runID, "agent_kind", req.AgentKind)

	// Lock-protect duplicate-run check + insert so two prompt_requests
	// with the same RunID can't both start sessions.
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		r.log.ErrorContext(callerCtx, "handlePromptRequest: router closed", "run_id", runID)
		return ErrRouterClosed
	}
	if _, dup := r.sessions[runID]; dup {
		r.mu.Unlock()
		r.log.WarnContext(callerCtx, "ignoring duplicate prompt_request", "run_id", runID)
		return nil
	}
	stateKey := sessionStateKey(req)
	r.touchIdleLocked(stateKey)
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	// Re-attach the inbound trace so every log under this run shows
	// the same trace_id as the prompt_request that started it.
	if carrier, ok := obslog.TraceFromContext(callerCtx); ok {
		sessionCtx = obslog.WithTrace(sessionCtx, carrier)
	}
	out := make(chan proto.Envelope, 64)
	state := &sessionState{
		runID:       runID,
		stateKey:    stateKey,
		out:         out,
		ctxCancel:   sessionCancel,
		pendingIDs:  make(map[string]struct{}),
		pendingAsks: make(map[string]struct{}),
		traceparent: env.Trace,
		retain:      stateKey != "",
	}
	r.sessions[runID] = state
	r.mu.Unlock()

	r.log.InfoContext(callerCtx, "handlePromptRequest: calling factory", "run_id", runID)
	sess, err := factory(sessionCtx, req, out)
	if err != nil {
		r.log.ErrorContext(callerCtx, "handlePromptRequest: factory call failed", "run_id", runID, "agent_kind", req.AgentKind, "err", err)
		// Roll back the registration, cancel ctx, surface error+done
		// so the server doesn't hang on a phantom run.
		r.mu.Lock()
		delete(r.sessions, runID)
		r.mu.Unlock()
		sessionCancel()
		r.emitTerminalError(callerCtx, runID, fmt.Sprintf("agent factory failed: %v", err))
		return fmt.Errorf("dispatch: factory %q: %w", req.AgentKind, err)
	}
	r.log.InfoContext(callerCtx, "handlePromptRequest: session created, starting pump", "run_id", runID)
	r.mu.Lock()
	state.session = sess
	r.mu.Unlock()

	r.shutdownWG.Add(1)
	go r.pump(state)
	return nil
}

func (r *Router) handlePromptCancel(ctx context.Context, env proto.Envelope) error {
	r.mu.Lock()
	state, ok := r.sessions[env.ID]
	if ok {
		state.retain = false
	}
	r.mu.Unlock()
	if !ok {
		// Cancelling an unknown / already-finished run is a no-op.
		r.log.InfoContext(ctx, "prompt_cancel for unknown run (no-op)", "run_id", env.ID)
		return nil
	}
	if err := state.session.Cancel(ctx); err != nil {
		r.log.WarnContext(ctx, "session.Cancel failed", "run_id", env.ID, "err", err)
	}
	state.ctxCancel()
	return nil
}

func (r *Router) handlePermissionDecision(ctx context.Context, env proto.Envelope) error {
	if env.ID == "" {
		return errors.New("dispatch: permission_decision missing perm id (Envelope.ID empty)")
	}
	var payload proto.PermissionDecisionPayload
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("dispatch: decode permission_decision: %w", err)
	}

	r.mu.Lock()
	runID, known := r.permIndex[env.ID]
	var state *sessionState
	if known {
		state = r.sessions[runID]
	}
	r.mu.Unlock()

	if !known || state == nil {
		// Server's perm timeout / cancel race; common enough that info
		// is right.
		r.log.InfoContext(ctx, "permission_decision for unknown perm (run gone)", "perm_id", env.ID)
		return nil
	}

	if err := state.session.SubmitPermission(ctx, env.ID, payload); err != nil {
		if errors.Is(err, agent.ErrUnknownPermission) {
			r.log.InfoContext(ctx, "agent reports unknown perm (race with cancel)", "perm_id", env.ID, "run_id", runID)
			return nil
		}
		return fmt.Errorf("dispatch: forward permission_decision perm=%s run=%s: %w", env.ID, runID, err)
	}
	return nil
}

// handlePromptForUserChoiceDecision is the ask-side twin of
// handlePermissionDecision. The server forwards the human's answer
// here; we look up the owning session via askIndex and ask the agent
// to write a matching tool_result back into its CLI.
//
// On both success and ErrUnknownAsk we drop the ask from askIndex /
// pendingAsks so a stale retry can't waste cycles. Timer-fired cancels
// inside the session don't currently call back into the router, so
// those entries linger until cleanupSession — acceptable because they
// can't double-fire (the session's own pendingAskTable.Take already
// guards that); cleanupSession removes the routing entry when the run
// stream closes, even if the underlying CLI remains in the idle pool.
func (r *Router) handlePromptForUserChoiceDecision(ctx context.Context, env proto.Envelope) error {
	if env.ID == "" {
		return errors.New("dispatch: prompt_for_user_choice_decision missing ask id (Envelope.ID empty)")
	}
	var payload proto.PromptForUserChoiceDecisionPayload
	if err := env.DecodePayload(&payload); err != nil {
		return fmt.Errorf("dispatch: decode prompt_for_user_choice_decision: %w", err)
	}

	r.mu.Lock()
	runID, known := r.askIndex[env.ID]
	var state *sessionState
	if known {
		state = r.sessions[runID]
	}
	r.mu.Unlock()

	if !known || state == nil {
		r.log.InfoContext(ctx, "prompt_for_user_choice_decision for unknown ask (run gone)", "ask_id", env.ID)
		return nil
	}

	err := state.session.SubmitPromptForUserChoice(ctx, env.ID, payload)
	// Resolved one way or another — the ask is done from the router's
	// point of view. Drop it from both indices so subsequent retries
	// short-circuit as "run gone" rather than re-entering Submit.
	r.dropAsk(state, env.ID)
	if err != nil {
		if errors.Is(err, agent.ErrUnknownAsk) {
			r.log.InfoContext(ctx, "agent reports unknown ask (race with cancel)", "ask_id", env.ID, "run_id", runID)
			return nil
		}
		return fmt.Errorf("dispatch: forward prompt_for_user_choice_decision ask=%s run=%s: %w", env.ID, runID, err)
	}
	return nil
}

// dropAsk clears askID from both the router-level askIndex and the
// session's pendingAsks set. Safe to call with an askID that's already
// gone — both deletes are no-ops then.
func (r *Router) dropAsk(s *sessionState, askID string) {
	r.mu.Lock()
	delete(r.askIndex, askID)
	delete(s.pendingAsks, askID)
	r.mu.Unlock()
}

func (r *Router) handleDeviceShutdown(ctx context.Context, env proto.Envelope) error {
	var payload proto.DeviceShutdownPayload
	_ = env.DecodePayload(&payload) // body optional
	r.log.InfoContext(ctx, "device_shutdown received, cancelling runs", "reason", payload.Reason, "active_runs", r.ActiveRuns())
	// Snapshot under lock, cancel outside so a slow Session.Cancel
	// can't stall other Handle calls.
	r.mu.Lock()
	victims := make([]*sessionState, 0, len(r.sessions))
	for _, s := range r.sessions {
		s.retain = false
		victims = append(victims, s)
	}
	for _, states := range r.idle {
		for s := range states {
			s.retain = false
			if s.idleTimer != nil {
				s.idleTimer.Stop()
			}
			victims = append(victims, s)
		}
	}
	r.idle = make(map[string]map[*sessionState]struct{})
	r.mu.Unlock()
	cancelSessions(ctx, victims, r.log)
	return nil
}

// cancelSessions cancels ctx AND invokes Session.Cancel so agents that
// don't watch ctx (subprocess wrappers relying on SIGTERM via Cancel)
// actually start their shutdown — otherwise the pump's drain blocks
// forever waiting for an out channel nobody closes.
func cancelSessions(ctx context.Context, sessions []*sessionState, log *slog.Logger) {
	for _, s := range sessions {
		s.ctxCancel()
		if s.session == nil {
			continue
		}
		if err := s.session.Cancel(ctx); err != nil {
			log.Warn("session.Cancel failed", "run_id", s.runID, "err", err)
		}
	}
}

// ---------------------------------------------------------------------
// pump goroutine
// ---------------------------------------------------------------------

// pump forwards every Envelope the session writes onto out to the
// upstream sender, then cleans up when the session closes out.
func (r *Router) pump(s *sessionState) {
	// Logging-only ctx carrying the run's trace; sends use their own
	// ctx tied to shutdownCh.
	pumpCtx := context.Background()
	if s.traceparent != "" {
		if carrier, err := obslog.ParseTraceparent(s.traceparent); err == nil {
			pumpCtx = obslog.WithTrace(pumpCtx, carrier)
		}
	}
	r.log.InfoContext(pumpCtx, "pump: started", "run_id", s.runID)
	defer r.shutdownWG.Done()
	defer r.cleanupSession(s)

	// Long-lived send ctx — must keep forwarding even after the
	// session's ctx is cancelled (session might emit a final "done"
	// in response to cancel). Stops on out close or router shutdown.
	for {
		select {
		case env, ok := <-s.out:
			if !ok {
				r.log.InfoContext(pumpCtx, "pump: out channel closed", "run_id", s.runID)
				return
			}
			r.indexPermissionFrame(s, env)
			r.log.InfoContext(pumpCtx, "pump: forwarding envelope", "run_id", s.runID, "type", env.Type, "env_id", env.ID)
			// Stamp the run's trace onto outbound frames so the
			// gateway attributes daemon-emitted lines to the same
			// trace_id as the original prompt_request.
			if env.Trace == "" && s.traceparent != "" {
				env.Trace = s.traceparent
			}
			// Short-ish send ctx that respects router shutdown — if
			// the transport is wedged we don't want to block forever.
			sendCtx, cancel := context.WithCancel(context.Background())
			stopOnShutdown := make(chan struct{})
			go func() {
				select {
				case <-r.shutdownCh:
					cancel()
				case <-stopOnShutdown:
				}
			}()
			err := r.sender.Send(sendCtx, env)
			close(stopOnShutdown)
			cancel()
			if err != nil {
				// Sender failed — log, ask the session to wind down,
				// but KEEP draining out so the agent's goroutines
				// don't block on a full channel.
				r.log.ErrorContext(pumpCtx, "send envelope failed", "type", env.Type, "run_id", env.ID, "err", err)
				r.mu.Lock()
				s.retain = false
				r.mu.Unlock()
				s.ctxCancel()
				r.drain(s.out)
				return
			}
		case <-r.shutdownCh:
			// Router shutdown — cancel + drain so the session's
			// goroutines unblock and close out cleanly.
			r.mu.Lock()
			s.retain = false
			r.mu.Unlock()
			s.ctxCancel()
			r.drain(s.out)
			return
		}
	}
}

// indexPermissionFrame records / forgets perm and ask ids as the
// agent emits / cancels them.
func (r *Router) indexPermissionFrame(s *sessionState, env proto.Envelope) {
	switch env.Type {
	case proto.TypePermissionRequest:
		if env.ID == "" {
			return
		}
		r.mu.Lock()
		r.permIndex[env.ID] = s.runID
		s.pendingIDs[env.ID] = struct{}{}
		r.mu.Unlock()
	case proto.TypePermissionCancel:
		if env.ID == "" {
			return
		}
		r.mu.Lock()
		delete(r.permIndex, env.ID)
		delete(s.pendingIDs, env.ID)
		r.mu.Unlock()
	case proto.TypePromptForUserChoice:
		// env.ID is the run id (so the server-side dispatch can fan
		// this frame to the run's subscriber); the ask id rides on
		// the payload. Decode just enough to seed the index.
		var p proto.PromptForUserChoicePayload
		if err := env.DecodePayload(&p); err != nil || p.AskID == "" {
			return
		}
		r.mu.Lock()
		r.askIndex[p.AskID] = s.runID
		s.pendingAsks[p.AskID] = struct{}{}
		r.mu.Unlock()
	}
}

// drain consumes everything left on ch until the agent closes it.
// Events are dropped — by the time we're draining, either transport
// is dead or the router is shutting down.
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
