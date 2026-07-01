// Package agentdaemon is the server-side AgentConnector that fronts
// the daemon WebSocket gateway. It glues Parsar's PromptInput /
// PromptEvent surface to the per-device WS sessions owned by
// gateway.Registry.
//
// This package implements the upper half (connector → gateway). The
// gateway package owns the lower half (gateway → WS → daemon). The
// only thing this package needs from the daemon side is the protocol
// types in internal/agentdaemon/proto.
//
// agent_kind is resolved from the agent config and validated against
// the selected daemon session's heartbeat-advertised capabilities before
// sending prompt_request. Older daemons that only expose the legacy
// claude_available signal are treated as claude_code-only.
package agentdaemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"strings"
	"sync/atomic"
	"time"

	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/binding"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// ConnectorType is the connector_type string this connector handles.
// Re-exported from binding.ConnectorType so a single constant cannot
// drift and callers that only import this package don't need a second
// import for the registry MustRegister call.
const ConnectorType = binding.ConnectorType

// ErrUnsupportedAgentKind is returned when the resolved agent_kind is not
// advertised as available by the selected daemon device.
var ErrUnsupportedAgentKind = errors.New("agent_daemon: unsupported agent_kind")

// Config wires the connector's dependencies. Registry / Binder are
// required; nil triggers a panic in New so a misconfigured boot fails
// loudly.
type Config struct {
	Registry *gateway.Registry

	Binder binding.Binder

	// Sandbox is the lazy-create provider for sandbox-mode
	// agents. The default dispatch path no longer Acquires on
	// ErrNotBound (see acquireSandboxBinding); the provider is kept for
	// a future conversation-scoped ephemeral sandbox feature.
	//
	// Optional: nil falls back to NoopSandboxProvider, which surfaces
	// ErrSandboxProviderDisabled as a clean EventError so deployments
	// without an e2b template still boot.
	Sandbox SandboxProvider

	// OwnerResolver + OwnerPodID enable multi-pod routing. When the
	// conversation binding points at a device owned by another pod,
	// StreamPrompt forwards to Remote instead of falsely reporting the
	// device offline in the local Registry.
	OwnerResolver DeviceOwnerResolver
	OwnerPodID    string
	Remote        RemoteStreamer

	// RemoteSubmit is the Submit-side counterpart of Remote: when the
	// feishu webhook lands on a non-owner pod we POST the decision over
	// to the owner pod's internal Submit endpoint rather than pretend
	// the slot has expired.
	RemoteSubmit RemoteSubmitter

	// SubmitSlots translates a SubmitPermission / SubmitPromptForUserChoice
	// request id back to the owning device. *store.Store reads the
	// device_id stamped into the inflight slot by the outbound driver.
	// Nil disables Submit owner-routing (single-pod behavior).
	SubmitSlots SubmitSlotResolver

	// ModelResolver enables Parsar-managed model injection for
	// daemon-backed agents. Nil preserves BYO daemon behavior for
	// configs without model_id.
	ModelResolver ModelResolver

	// Capabilities optionally enables capability-driven options
	// injection (Skill capability instructions folded into
	// --append-system-prompt). Nil → skip capability resolution.
	Capabilities CapabilityRuntimeStore

	// Secrets decrypts model-provider Secret payloads. Required when
	// ModelResolver is set and a prompt selects a managed model. If nil
	// but MasterKey is provided, New builds one.
	Secrets *secrets.Service

	// MasterKey is turned into Secrets by New when Secrets is nil.
	MasterKey string

	// IMHistoryEndpoint is the public URL of the internal on-demand
	// chat-history endpoint (…/internal/im/history) that the auto-mounted
	// fetch_chat_history MCP tool calls back into. Empty disables the tool
	// injection (the agent simply won't see the tool).
	IMHistoryEndpoint string

	// IMHistoryTokenSigner mints the per-conversation bearer token the
	// auto-mounted fetch_chat_history tool presents to IMHistoryEndpoint. It
	// MUST sign with the same secret the endpoint verifies (both derive from
	// the master key). Nil disables the tool injection.
	IMHistoryTokenSigner func(conversationID string) string

	// ExecutionRecorder persists the per-run execution snapshot. Nil
	// keeps tests on the pre-snapshot behavior.
	ExecutionRecorder ExecutionSnapshotRecorder

	// RunStatusReader gates the session write-back: when the run is
	// cancelled or interrupted, RememberSession is skipped on the done
	// event. Nil → always write back.
	RunStatusReader AgentRunStatusReader

	// SpecMemory, when non-nil, enables SessionStart spec/memory
	// injection: RenderSessionPrompt output is appended onto the
	// agent's system_prompt. Skipped when override_system_prompt is set
	// on the merged config (override wins). Render errors are logged
	// and swallowed so a flaky read can never fail a prompt.
	SpecMemory SpecMemoryInjector

	// OSS, when non-nil, enables capability zip download for plugin and
	// skill types. The interface is narrow (just PresignGet) so
	// cmd/server can pass a *oss.Client without this package importing
	// the SDK. Nil → plugin / skill capabilities are skipped with a log
	// warning.
	OSS OSSPresigner

	// SystemMessages sinks the channel-layer credential nudges produced
	// by ADR-003 soft-degrade — one runtime_error system_message per
	// (capability, kind) pair so the Feishu outbound driver can render
	// a credential-form card.
	//
	// PRODUCTION MUST WIRE THIS: otherwise an agent_daemon-backed run
	// with missing MCP credentials proceeds silently and the user sees
	// a generic "agent finished" without the failing MCP, and the
	// credential-form recovery loop never fires.
	SystemMessages CapabilitySystemMessageStore

	// SandboxBindingReader, when non-nil, lets the connector
	// distinguish spawning / failed / never-attempted sandbox states
	// for the user-facing "no Runtime bound" message instead of using a
	// generic line. Nil keeps legacy behaviour for tests.
	SandboxBindingReader SandboxBindingReader

	// Log is the structured logger; nil falls back to slog.Default().
	Log *slog.Logger
}

// Connector is the AgentConnector implementation for connector_type =
// "agent_daemon". One instance lives for the lifetime of the server
// process; concurrency is delegated to gateway.Registry + binding.Binder.
type Connector struct {
	registry          *gateway.Registry
	binder            binding.Binder
	sandbox           SandboxProvider
	ownerResolver     DeviceOwnerResolver
	ownerPodID        string
	remote            RemoteStreamer
	remoteSubmit      RemoteSubmitter
	submitSlots       SubmitSlotResolver
	modelResolver     ModelResolver
	executionRecorder ExecutionSnapshotRecorder
	runStatus         AgentRunStatusReader
	secrets           *secrets.Service
	capabilities      CapabilityRuntimeStore
	specMemory        SpecMemoryInjector
	oss               OSSPresigner
	systemMessages    CapabilitySystemMessageStore
	sandboxBindings   SandboxBindingReader
	imHistoryEndpoint string
	imHistoryToken    func(conversationID string) string
	log               *slog.Logger
}

// ExecutionSnapshotRecorder is satisfied by *store.Store.
type ExecutionSnapshotRecorder interface {
	RecordAgentRunExecutionSnapshot(ctx context.Context, input store.RecordAgentRunExecutionSnapshotInput) error
}

// AgentRunStatusReader is satisfied by *store.Store. Used by the
// session-write-back guard to skip RememberSession when the run has
// already been cancelled/interrupted. The returned started_at feeds
// binder.RememberSession's CAS guard so stale done events from older
// runs cannot overwrite a fresher session id.
type AgentRunStatusReader interface {
	GetAgentRunStatusAndStartedAt(ctx context.Context, runID string) (status string, startedAt time.Time, err error)
}

// SandboxBindingReader looks up the current sandbox_bindings row for
// a (workspace, agent) tuple. Consulted only on the slow path
// when a sandbox-mode agent has no runtime bound, to distinguish:
//
//   - (zero, false, nil)             no Acquire attempt yet
//   - (status="spawning", true)      Acquire in flight
//   - (status="killed_error", true)  Acquire failed
//   - (status="running", true)       sandbox alive but runtime_id not
//     yet written (Acquire-success → SetAgentRuntime UPDATE
//     race)
//
// *store.Store satisfies this via GetActiveSandboxBindingForAgent.
type SandboxBindingReader interface {
	GetActiveSandboxBindingForAgent(ctx context.Context, workspaceID, agentID string) (store.SandboxBindingRead, bool, error)
}

// SpecMemoryInjector is the narrow surface the connector needs from
// the spec/memory service. Returning "" (with nil error) signals
// "nothing to inject".
type SpecMemoryInjector interface {
	RenderSessionPrompt(ctx context.Context, workspaceID, userID string) (string, error)
}

// New wires the connector. Panics on missing Registry / Binder so the
// misconfiguration surfaces at boot rather than at the first prompt.
func New(cfg Config) *Connector {
	if cfg.Registry == nil {
		panic("agent_daemon connector: Config.Registry is required")
	}
	if cfg.Binder == nil {
		panic("agent_daemon connector: Config.Binder is required")
	}
	if cfg.Sandbox == nil {
		cfg.Sandbox = NoopSandboxProvider{}
	}
	if cfg.Log == nil {
		cfg.Log = obslog.Bg()
	}
	if cfg.Secrets == nil {
		svc, err := newSecretService(cfg.MasterKey)
		if err != nil {
			cfg.Log.Warn("agent_daemon connector: invalid master key; managed model injection disabled", "error", err)
		} else {
			cfg.Secrets = svc
		}
	}
	return &Connector{
		registry:          cfg.Registry,
		binder:            cfg.Binder,
		sandbox:           cfg.Sandbox,
		ownerResolver:     cfg.OwnerResolver,
		ownerPodID:        cfg.OwnerPodID,
		remote:            cfg.Remote,
		remoteSubmit:      cfg.RemoteSubmit,
		submitSlots:       cfg.SubmitSlots,
		modelResolver:     cfg.ModelResolver,
		executionRecorder: cfg.ExecutionRecorder,
		runStatus:         cfg.RunStatusReader,
		secrets:           cfg.Secrets,
		capabilities:      cfg.Capabilities,
		specMemory:        cfg.SpecMemory,
		oss:               cfg.OSS,
		systemMessages:    cfg.SystemMessages,
		sandboxBindings:   cfg.SandboxBindingReader,
		imHistoryEndpoint: cfg.IMHistoryEndpoint,
		imHistoryToken:    cfg.IMHistoryTokenSigner,
		log:               cfg.Log,
	}
}

var _ connector.AgentConnector = (*Connector)(nil)

// Type returns the connector_type this connector handles.
func (c *Connector) Type() string { return ConnectorType }

// Capabilities reports the full feature set the daemon protocol
// supports. Sync is true because Prompt is implemented in terms of
// StreamPrompt.
func (c *Connector) Capabilities() connector.Capabilities {
	return connector.Capabilities{
		Sync:         true,
		Streaming:    true,
		Cancellation: true,
		Permissions:  true,
		Usage:        true,
		Audit:        true,
	}
}

// Prompt is the synchronous request/response path, implemented on top
// of StreamPrompt: accumulate deltas and metadata, return the final
// PromptOutput once the stream closes.
func (c *Connector) Prompt(ctx context.Context, in connector.PromptInput) (connector.PromptOutput, error) {
	events, err := c.StreamPrompt(ctx, in)
	if err != nil {
		return connector.PromptOutput{}, err
	}
	var out connector.PromptOutput
	var deltaBuf string
	var streamErr string
	for ev := range events {
		switch ev.Type {
		case connector.EventDelta:
			deltaBuf += ev.Delta
		case connector.EventError:
			streamErr = ev.Error
		case connector.EventDone:
			if ev.Final != nil {
				out = *ev.Final
			}
		}
	}
	if streamErr != "" {
		return connector.PromptOutput{}, fmt.Errorf("agent_daemon: %s", streamErr)
	}
	// Fall back to locally-accumulated deltas when an older daemon
	// didn't populate Final.Content.
	if out.Content == "" {
		out.Content = deltaBuf
	}
	return out, nil
}

// StreamPrompt is the asynchronous path:
//
//  1. resolve agent_kind from the merged config.
//  2. resolve the conversation → device binding; ErrNotBound surfaces
//     as a clean EventError so the front-end can prompt the user to
//     pick a device.
//  3. look up the live Session in the gateway registry; offline devices
//     surface as EventError("device offline ...").
//  4. subscribe to the runID, send prompt_request, translate every
//     upstream envelope into the matching PromptEvent.
//  5. on ctx cancel, send prompt_cancel before draining.
func (c *Connector) StreamPrompt(ctx context.Context, in connector.PromptInput) (<-chan connector.PromptEvent, error) {
	return c.streamPrompt(ctx, in, true)
}

// StreamPromptLocal executes only against this process' local Registry.
// Internal cross-pod forwarding handlers call this on the owner pod to
// avoid recursively consulting the owner table and forwarding again.
func (c *Connector) StreamPromptLocal(ctx context.Context, in connector.PromptInput) (<-chan connector.PromptEvent, error) {
	return c.streamPrompt(ctx, in, false)
}

func (c *Connector) streamPrompt(ctx context.Context, in connector.PromptInput, allowRemote bool) (<-chan connector.PromptEvent, error) {
	if in.RunID == "" {
		return nil, fmt.Errorf("agent_daemon: PromptInput.RunID is required")
	}
	c.log.Info("agent_daemon: streamPrompt enter",
		"run_id", in.RunID,
		"conversation_id", in.ConversationID,
		"agent_id", in.AgentID,
		"allow_remote", allowRemote)
	agentKind := resolveAgentKind(in)

	agentOptions, err := c.buildAgentOptions(ctx, in)
	if err != nil {
		c.log.Warn("agent_daemon: buildAgentOptions failed", "run_id", in.RunID, "err", err.Error())
		return errorChannel(in.RunID, err.Error()), nil
	}

	bind, err := c.binder.Resolve(ctx, in.ConversationID, in.AgentID)
	if err != nil {
		if errors.Is(err, binding.ErrNotBound) {
			// Lazy-bind: pick up the runtime the user picked in the
			// agent settings page. AgentConfig.device_id is fed
			// by the agents.runtime_id FK (authoritative)
			// merged in by the store in GetAgentRunInvocation; the
			// jsonb mirror exists for downstream readers that already
			// pivot on device_id.
			//
			// Per agent_must_bind_runtime memory: auto-Acquire
			// on the default dispatch path is intentionally OFF —
			// callers must bind a runtime up front. The sandbox
			// provider stays compiled for a future conversation-scoped
			// ephemeral feature.
			if configured, ok := configuredDeviceBinding(in); ok {
				c.log.Info("agent_daemon: lazy-bind conversation from agent runtime binding",
					"run_id", in.RunID,
					"conversation_id", in.ConversationID,
					"device_id", configured.DeviceID)
				bind = configured
				if err := c.binder.Bind(ctx, bind); err != nil {
					c.log.Warn("agent_daemon: persist device binding failed", "run_id", in.RunID, "err", err.Error())
					return errorChannel(in.RunID, "agent_daemon: persist device binding: "+err.Error()), nil
				}
			} else {
				c.log.Warn("agent_daemon: agent has no runtime bound",
					"run_id", in.RunID,
					"conversation_id", in.ConversationID,
					"agent_id", in.AgentID,
					"sandbox_mode_requested", isSandboxMode(in))
				return errorChannel(in.RunID, c.unboundRuntimeMessage(ctx, in)), nil
			}
		} else {
			c.log.Error("agent_daemon: binder.Resolve failed", "run_id", in.RunID, "err", err.Error())
			return nil, fmt.Errorf("agent_daemon: resolve binding: %w", err)
		}
	} else {
		c.log.Info("agent_daemon: binding resolved",
			"run_id", in.RunID,
			"conversation_id", in.ConversationID,
			"device_id", bind.DeviceID)
	}

	if ch, routed, routeErr := c.routeRemoteIfNeeded(ctx, bind, in, allowRemote); routed || routeErr != nil {
		c.log.Info("agent_daemon: remote-routed (returning from streamPrompt)",
			"run_id", in.RunID,
			"device_id", bind.DeviceID,
			"route_err", errString(routeErr))
		return ch, routeErr
	}

	c.log.Info("agent_daemon: local owner — looking up device session",
		"run_id", in.RunID, "device_id", bind.DeviceID)
	sess, err := c.registry.LookupDevice(bind.DeviceID)
	if err != nil {
		c.log.Warn("agent_daemon: device offline (registry LookupDevice failed)",
			"run_id", in.RunID, "device_id", bind.DeviceID, "err", err.Error())
		// Sandbox mode: surface a system message so the user
		// understands why the run failed and what their recovery
		// options are. We DO NOT auto-acquire a fresh sandbox — the
		// existing one may carry installed config / Claude session
		// state, and silently replacing it would discard that without
		// consent. Recovery is explicit (delete + recreate the Agent
		// in the web UI).
		if isSandboxMode(in) && c.systemMessages != nil {
			notice := fmt.Sprintf(
				"⚠️ Sandbox %s is unavailable — likely reclaimed after idle timeout or a network issue. "+
					"Wait a few minutes for it to recover, or delete and recreate the Agent in its settings to reset immediately.",
				bind.DeviceID,
			)
			if _, sysErr := c.systemMessages.CreateSandboxOfflineNotice(ctx, store.CreateSandboxOfflineNoticeInput{
				WorkspaceID:    in.WorkspaceID,
				AgentID:        in.AgentID,
				RunID:          in.RunID,
				ConversationID: in.ConversationID,
				DeviceID:       bind.DeviceID,
				Content:        notice,
			}); sysErr != nil {
				c.log.Warn("agent_daemon: insert sandbox offline notice",
					"err", sysErr, "run_id", in.RunID, "device_id", bind.DeviceID)
			}
			return errorChannel(in.RunID, "agent_daemon sandbox offline (deviceID="+bind.DeviceID+"); see system message for recovery"), nil
		}
		return errorChannel(in.RunID, "agent_daemon device offline (deviceID="+bind.DeviceID+"); waiting for daemon to reconnect"), nil
	}
	if err := c.validateAgentKindForSession(sess, agentKind); err != nil {
		c.log.Warn("agent_daemon: unsupported agent_kind for device",
			"run_id", in.RunID, "device_id", bind.DeviceID, "agent_kind", agentKind, "err", err.Error())
		return errorChannel(in.RunID, err.Error()), nil
	}
	kindInfo, _, _ := sess.AgentKindStatus(agentKind)
	c.recordExecutionSnapshot(ctx, in, bind, agentKind, kindInfo)

	upstream, err := sess.Subscribe(in.RunID)
	if err != nil {
		c.log.Warn("agent_daemon: session.Subscribe failed", "run_id", in.RunID, "err", err.Error())
		return errorChannel(in.RunID, "agent_daemon: subscribe failed: "+err.Error()), nil
	}

	req, err := proto.NewEnvelope(proto.TypePromptRequest, in.RunID, proto.PromptRequestPayload{
		AgentKind:       agentKind,
		ConversationID:  in.ConversationID,
		RunID:           in.RunID,
		Prompt:          in.TriggerMessageContent,
		Attachments:     promptAttachmentsFromStore(in.TriggerAttachments),
		WorkDir:         bind.WorkDir,
		AgentOptions:    agentOptions,
		ResumeSessionID: bind.ClaudeSessionID,
	})
	if err != nil {
		sess.Unsubscribe(in.RunID)
		c.log.Error("agent_daemon: proto.NewEnvelope failed", "run_id", in.RunID, "err", err.Error())
		return nil, fmt.Errorf("agent_daemon: build prompt_request: %w", err)
	}

	c.log.Info("agent_daemon: spawning runStreamLoop (will push prompt_request to daemon)",
		"run_id", in.RunID, "device_id", bind.DeviceID, "agent_kind", agentKind)
	out := make(chan connector.PromptEvent, 32)
	go c.runStreamLoop(ctx, sess, in, bind, req, upstream, out)
	return out, nil
}

func (c *Connector) recordExecutionSnapshot(ctx context.Context, in connector.PromptInput, bind binding.Binding, agentKind string, info store.AgentDaemonSupportedAgentKind) {
	if c.executionRecorder == nil {
		return
	}
	runtimeMode := "local"
	if isSandboxMode(in) {
		runtimeMode = "sandbox"
	}
	workDir := strings.TrimSpace(bind.WorkDir)
	if workDir == "" {
		workDir = firstConfigString(in.AgentConfig, "work_dir", "workdir", "working_directory")
	}
	if err := c.executionRecorder.RecordAgentRunExecutionSnapshot(ctx, store.RecordAgentRunExecutionSnapshotInput{
		RunID:            in.RunID,
		ConnectorType:    ConnectorType,
		RuntimeID:        bind.DeviceID,
		DeviceID:         bind.DeviceID,
		AgentKind:        agentKind,
		RuntimeMode:      runtimeMode,
		WorkingDirectory: workDir,
		ManagedModelID:   resolveModelID(in),
		Capabilities:     capabilitiesFromAgentKind(info),
	}); err != nil {
		c.log.Warn("agent_daemon: record execution snapshot failed", "run_id", in.RunID, "device_id", bind.DeviceID, "err", err.Error())
	}
}

func capabilitiesFromAgentKind(info store.AgentDaemonSupportedAgentKind) map[string]bool {
	out := map[string]bool{
		"streaming":    info.Capabilities.Streaming,
		"cancellation": true,
		"permissions":  info.Capabilities.Permissions,
		"usage":        info.Capabilities.Usage,
		"resume":       info.Capabilities.Resume,
		"artifacts":    false,
	}
	if info.Kind == "" {
		return nil
	}
	return out
}

func firstConfigString(config map[string]any, keys ...string) string {
	for _, key := range keys {
		if raw, ok := config[key].(string); ok {
			if value := strings.TrimSpace(raw); value != "" {
				return value
			}
		}
	}
	return ""
}

func (c *Connector) validateAgentKindForSession(sess *gateway.Session, agentKind string) error {
	agentKind = strings.TrimSpace(agentKind)
	if agentKind == "" {
		return fmt.Errorf("%w: empty", ErrUnsupportedAgentKind)
	}
	info, found, snapshotKnown := sess.AgentKindStatus(agentKind)
	if !found {
		if snapshotKnown {
			return fmt.Errorf("%w: %q not advertised by device %s", ErrUnsupportedAgentKind, agentKind, sess.DeviceID)
		}
		return fmt.Errorf("%w: %q not advertised by device %s yet", ErrUnsupportedAgentKind, agentKind, sess.DeviceID)
	}
	if !info.Available {
		if strings.TrimSpace(info.Version) != "" {
			return fmt.Errorf("%w: %q unavailable on device %s (%s)", ErrUnsupportedAgentKind, agentKind, sess.DeviceID, info.Version)
		}
		return fmt.Errorf("%w: %q unavailable on device %s", ErrUnsupportedAgentKind, agentKind, sess.DeviceID)
	}
	return nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// runStreamLoop drives one prompt's lifecycle. Owns the outbound
// channel and MUST close it on every exit path so the caller never
// hangs.
func (c *Connector) runStreamLoop(
	ctx context.Context,
	sess *gateway.Session,
	in connector.PromptInput,
	bind binding.Binding,
	req proto.Envelope,
	upstream <-chan proto.Envelope,
	out chan<- connector.PromptEvent,
) {
	defer close(out)
	defer sess.Unsubscribe(in.RunID)

	c.log.Info("agent_daemon: writing prompt_request to daemon WS",
		"run_id", in.RunID, "device_id", bind.DeviceID,
		"prompt_bytes", len(in.TriggerMessageContent),
		"attachments_count", len(in.TriggerAttachments))
	if err := sess.Send(ctx, req); err != nil {
		c.log.Error("agent_daemon: WS send prompt_request failed",
			"run_id", in.RunID, "device_id", bind.DeviceID, "err", err.Error())
		out <- connector.PromptEvent{Type: connector.EventError, Error: "agent_daemon: send prompt_request: " + err.Error()}
		out <- connector.PromptEvent{Type: connector.EventDone, Final: &connector.PromptOutput{}}
		return
	}
	c.log.Info("agent_daemon: prompt_request sent to daemon WS",
		"run_id", in.RunID, "device_id", bind.DeviceID)

	var seq uint64
	for {
		select {
		case <-ctx.Done():
			// Caller cancelled — tell the daemon to abort and stop
			// forwarding. Still publish EventError + EventDone so the
			// consumer's range loop terminates.
			cancelEnv, _ := proto.NewEnvelope(proto.TypePromptCancel, in.RunID, proto.PromptCancelPayload{})
			_ = sess.Send(context.Background(), cancelEnv)
			out <- connector.PromptEvent{Type: connector.EventError, Error: "cancelled: " + ctx.Err().Error()}
			out <- connector.PromptEvent{Type: connector.EventDone, Final: &connector.PromptOutput{}}
			return

		case env, ok := <-upstream:
			if !ok {
				// Session closed under us — synthetic error+done
				// were already enqueued by Session.Close before the
				// channel was closed.
				return
			}
			c.handleUpstream(ctx, env, in, bind, &seq, out)
			if env.Type == proto.TypeDone {
				return
			}
		}
	}
}

// handleUpstream translates one daemon envelope into the matching
// PromptEvent(s). EventDelta and EventDone get a monotonic sequence
// minted here; other types stay at Sequence=0 per the
// connector.PromptEvent contract.
func (c *Connector) handleUpstream(
	ctx context.Context,
	env proto.Envelope,
	in connector.PromptInput,
	bind binding.Binding,
	seq *uint64,
	out chan<- connector.PromptEvent,
) {
	switch env.Type {
	case proto.TypeDelta:
		var p proto.DeltaPayload
		if err := env.DecodePayload(&p); err != nil {
			c.log.Warn("agent_daemon: decode delta", "err", err, "run_id", in.RunID)
			return
		}
		out <- connector.PromptEvent{Type: connector.EventDelta, Delta: p.Delta, Sequence: atomic.AddUint64(seq, 1)}

	case proto.TypeThinking:
		var p proto.ThinkingPayload
		if err := env.DecodePayload(&p); err != nil {
			c.log.Warn("agent_daemon: decode thinking", "err", err, "run_id", in.RunID)
			return
		}
		out <- connector.PromptEvent{Type: connector.EventThinking, Thinking: p.Text, Sequence: atomic.AddUint64(seq, 1)}

	case proto.TypeToolCall:
		var p proto.ToolCallPayload
		if err := env.DecodePayload(&p); err != nil {
			c.log.Warn("agent_daemon: decode tool_call", "err", err, "run_id", in.RunID)
			return
		}
		out <- connector.PromptEvent{Type: connector.EventToolCall, Tool: &connector.ToolCallEvent{
			ID: p.ID, Name: p.Name, Stage: p.Stage, Args: p.Args, Result: p.Result,
		}}

	case proto.TypePermissionRequest:
		var p proto.PermissionRequestPayload
		if err := env.DecodePayload(&p); err != nil {
			c.log.Warn("agent_daemon: decode permission_request", "err", err, "run_id", in.RunID)
			return
		}
		out <- connector.PromptEvent{Type: connector.EventPermissionRequest, Permission: &connector.PermissionRequest{
			ID: env.ID, Tool: p.Tool, Title: p.Title, Detail: p.Detail, Payload: p.Payload,
		}}

	case proto.TypePromptForUserChoice:
		var p proto.PromptForUserChoicePayload
		if err := env.DecodePayload(&p); err != nil {
			c.log.Warn("agent_daemon: decode prompt_for_user_choice", "err", err, "run_id", in.RunID)
			return
		}
		// Translate every question through, regardless of whether the
		// payload arrived in the new (Questions) or legacy single-
		// question shape — EffectiveQuestions handles both.
		protoQs := p.EffectiveQuestions()
		questions := make([]connector.PromptForUserChoiceQuestion, 0, len(protoQs))
		for _, q := range protoQs {
			opts := make([]connector.PromptForUserChoiceOption, 0, len(q.Options))
			for _, opt := range q.Options {
				opts = append(opts, connector.PromptForUserChoiceOption{Label: opt.Label, Description: opt.Description})
			}
			questions = append(questions, connector.PromptForUserChoiceQuestion{
				Header:      q.Header,
				Question:    q.Question,
				MultiSelect: q.MultiSelect,
				Options:     opts,
			})
		}
		// AskID lives on the payload, not env.ID. env.ID is the run id so
		// server-side session.dispatch can fan this frame into the run's
		// subscriber channel (same path as delta/tool_call); the daemon
		// then routes SubmitPromptForUserChoice back by AskID via the
		// gateway registry's byAsk index.
		out <- connector.PromptEvent{Type: connector.EventPromptForUserChoice, PromptForUserChoice: &connector.PromptForUserChoiceRequest{
			ID:        p.AskID,
			Questions: questions,
			ToolUseID: p.ToolUseID,
		}}

	case proto.TypePermissionCancel:
		// No EventPermissionCancel exists yet; the gateway already
		// cleared the perm mapping. Log so any pending UI prompt can
		// be reasoned about.
		c.log.Info("agent_daemon: permission cancelled by agent", "perm_id", env.ID, "run_id", in.RunID)

	case proto.TypeUsage:
		var p proto.UsagePayload
		if err := env.DecodePayload(&p); err != nil {
			c.log.Warn("agent_daemon: decode usage", "err", err, "run_id", in.RunID)
			return
		}
		u := usageFromProto(p.Usage)
		out <- connector.PromptEvent{Type: connector.EventUsage, Usage: &u}

	case proto.TypeError:
		var p proto.ErrorPayload
		if err := env.DecodePayload(&p); err != nil {
			c.log.Warn("agent_daemon: decode error", "err", err, "run_id", in.RunID)
			p.Error = "agent_daemon: decode error payload failed"
		}
		out <- connector.PromptEvent{Type: connector.EventError, Error: p.Error}

	case proto.TypeDone:
		var p proto.DonePayload
		if err := env.DecodePayload(&p); err != nil {
			c.log.Warn("agent_daemon: decode done", "err", err, "run_id", in.RunID)
		}
		final := &connector.PromptOutput{
			Content:    p.Content,
			Transcript: p.Transcript,
			Usage:      usageFromProto(p.Usage),
			Metadata:   p.Metadata,
		}
		// Best-effort: persist claude_session_id so the next turn can
		// pass --resume. Errors logged but do not abort the run.
		if claudeSess, ok := p.Metadata["claude_session_id"].(string); ok && claudeSess != "" {
			c.rememberClaudeSession(ctx, in, claudeSess)
		}
		out <- connector.PromptEvent{Type: connector.EventDone, Final: final, Sequence: atomic.AddUint64(seq, 1)}
	}
}

// rememberClaudeSession folds the daemon's done-event claude_session_id
// back into the conversation binding. Stale done events (the run was
// cancelled and a new one already started) are filtered by the binder's
// own CAS guard on session_updated_at — both ours and the next run's
// writebacks carry runStartedAt, the older one loses.
func (c *Connector) rememberClaudeSession(ctx context.Context, in connector.PromptInput, claudeSess string) {
	var runStartedAt time.Time
	if c.runStatus != nil {
		_, startedAt, statusErr := c.runStatus.GetAgentRunStatusAndStartedAt(ctx, in.RunID)
		if statusErr != nil {
			c.log.Warn("agent_daemon: read run status for session write-back",
				"err", statusErr, "run_id", in.RunID)
		} else {
			runStartedAt = startedAt
		}
	}
	if err := c.binder.RememberSession(ctx, in.ConversationID, in.AgentID, claudeSess, runStartedAt); err != nil {
		c.log.Warn("agent_daemon: remember claude_session_id", "err", err, "run_id", in.RunID)
	}
}

// Cancel is the connector-wide "cancel anything in flight" hook. The
// dispatch layer tracks per-conversation runID and calls Abort with
// the specific runID; this stays a no-op so the dispatcher can call
// it unconditionally without racing Abort.
func (c *Connector) Cancel(_ context.Context, _ string) error {
	return nil
}

// Abort sends prompt_cancel for the given runID. Unknown runIDs are
// silently no-op'd — the daemon may have already returned done.
func (c *Connector) Abort(ctx context.Context, in connector.AbortInput) error {
	if in.RunID == "" {
		return nil
	}
	sess := c.registry.LookupRun(in.RunID)
	if sess == nil {
		return nil
	}
	env, err := proto.NewEnvelope(proto.TypePromptCancel, in.RunID, proto.PromptCancelPayload{})
	if err != nil {
		return fmt.Errorf("agent_daemon: build prompt_cancel: %w", err)
	}
	if err := sess.Send(ctx, env); err != nil {
		return fmt.Errorf("agent_daemon: send prompt_cancel: %w", err)
	}
	return nil
}

// SubmitPermission delivers a human verdict to the daemon. In a
// multi-pod deployment the inflight feishu webhook may land on a
// non-owner pod, so we first reverse-look up the request id → device
// id and forward to the owner pod's internal endpoint when needed.
// Unknown perm ids (cancelled or expired) on the owner pod return a
// clear error so the API layer can surface a 410 "no longer pending"
// response.
func (c *Connector) SubmitPermission(ctx context.Context, decision connector.PermissionDecision) error {
	if decision.RequestID == "" {
		return fmt.Errorf("agent_daemon: SubmitPermission requires RequestID")
	}
	if c.submitSlots != nil && c.ownerResolver != nil {
		deviceID, err := c.submitSlots.DeviceIDForPermissionRequest(ctx, decision.RequestID)
		if err != nil {
			// Slot already cleared or DB transient — fall through to
			// local lookup; the in-process registry will reject with a
			// clear "not pending" error if appropriate.
			c.log.Warn("agent_daemon: SubmitPermission slot lookup failed; trying local",
				"request_id", decision.RequestID, "err", err.Error())
		} else if strings.TrimSpace(deviceID) != "" {
			outcome, err := c.resolveOwnerForSubmit(ctx, decision.RequestID, deviceID, "permission")
			if err != nil {
				return err
			}
			if outcome.Remote != nil {
				if c.remoteSubmit == nil {
					return fmt.Errorf("agent_daemon: permission %s owned by remote pod %s but remote submit is not configured", decision.RequestID, outcome.Remote.OwnerPodID)
				}
				return c.remoteSubmit.SubmitPermissionRemote(ctx, *outcome.Remote, decision)
			}
		}
	}
	return c.SubmitPermissionLocal(ctx, decision)
}

// SubmitPermissionLocal runs the SubmitPermission registry-lookup path
// without an owner check. The internal /submit-permission handler calls
// this directly so the remote → owner pod hop does not re-route in a
// loop. External callers should use SubmitPermission instead.
func (c *Connector) SubmitPermissionLocal(ctx context.Context, decision connector.PermissionDecision) error {
	if decision.RequestID == "" {
		return fmt.Errorf("agent_daemon: SubmitPermissionLocal requires RequestID")
	}
	sess, err := c.registry.LookupPermission(decision.RequestID)
	if err != nil {
		return fmt.Errorf("agent_daemon: %w", err)
	}
	env, err := proto.NewEnvelope(proto.TypePermissionDecision, decision.RequestID, proto.PermissionDecisionPayload{
		Approved: decision.Approved,
		Message:  decision.Note,
	})
	if err != nil {
		return fmt.Errorf("agent_daemon: build permission_decision: %w", err)
	}
	if err := sess.Send(ctx, env); err != nil {
		return fmt.Errorf("agent_daemon: send permission_decision: %w", err)
	}
	// Clear the perm mapping so a duplicate SubmitPermission for the
	// same id returns ErrPermissionNotRegistered rather than silently
	// re-sending the verdict.
	c.registry.DetachPermission(decision.RequestID)
	return nil
}

// SubmitPromptForUserChoice delivers the human's pick for an
// outstanding AskUserQuestion to the daemon. Same owner-route → local
// lookup shape as SubmitPermission; cancelled answers (timeout,
// /cancel) ride the same envelope with Cancelled=true so the daemon
// can decide whether to retry the tool or stop.
func (c *Connector) SubmitPromptForUserChoice(ctx context.Context, decision connector.PromptForUserChoiceDecision) error {
	if decision.RequestID == "" {
		return fmt.Errorf("agent_daemon: SubmitPromptForUserChoice requires RequestID")
	}
	if c.submitSlots != nil && c.ownerResolver != nil {
		deviceID, err := c.submitSlots.DeviceIDForPromptForUserChoiceRequest(ctx, decision.RequestID)
		if err != nil {
			c.log.Warn("agent_daemon: SubmitPromptForUserChoice slot lookup failed; trying local",
				"request_id", decision.RequestID, "err", err.Error())
		} else if strings.TrimSpace(deviceID) != "" {
			outcome, err := c.resolveOwnerForSubmit(ctx, decision.RequestID, deviceID, "prompt_for_user_choice")
			if err != nil {
				return err
			}
			if outcome.Remote != nil {
				if c.remoteSubmit == nil {
					return fmt.Errorf("agent_daemon: prompt_for_user_choice %s owned by remote pod %s but remote submit is not configured", decision.RequestID, outcome.Remote.OwnerPodID)
				}
				return c.remoteSubmit.SubmitPromptForUserChoiceRemote(ctx, *outcome.Remote, decision)
			}
		}
	}
	return c.SubmitPromptForUserChoiceLocal(ctx, decision)
}

// SubmitPromptForUserChoiceLocal runs the registry-lookup path without
// an owner check. The internal /submit-prompt-for-user-choice handler
// calls this directly to avoid a remote → owner ping-pong.
func (c *Connector) SubmitPromptForUserChoiceLocal(ctx context.Context, decision connector.PromptForUserChoiceDecision) error {
	if decision.RequestID == "" {
		return fmt.Errorf("agent_daemon: SubmitPromptForUserChoiceLocal requires RequestID")
	}
	sess, err := c.registry.LookupPromptForUserChoice(decision.RequestID)
	if err != nil {
		return fmt.Errorf("agent_daemon: %w", err)
	}
	qas := make([]proto.PromptForUserChoiceQuestionAnswer, 0, len(decision.QuestionAnswers))
	for _, qa := range decision.QuestionAnswers {
		qas = append(qas, proto.PromptForUserChoiceQuestionAnswer{Header: qa.Header, Answer: qa.Answer})
	}
	env, err := proto.NewEnvelope(proto.TypePromptForUserChoiceDecision, decision.RequestID, proto.PromptForUserChoiceDecisionPayload{
		QuestionAnswers: qas,
		Answers:         decision.Answers,
		Cancelled:       decision.Cancelled,
		Reason:          decision.Reason,
	})
	if err != nil {
		return fmt.Errorf("agent_daemon: build prompt_for_user_choice_decision: %w", err)
	}
	if err := sess.Send(ctx, env); err != nil {
		return fmt.Errorf("agent_daemon: send prompt_for_user_choice_decision: %w", err)
	}
	c.registry.DetachPromptForUserChoice(decision.RequestID)
	return nil
}

// Close drops every binding for the conversation so the next prompt
// re-resolves (e.g. user archived and re-opened the conversation).
func (c *Connector) Close(ctx context.Context, conversationID string) error {
	if conversationID == "" {
		return nil
	}
	if err := c.binder.InvalidateConversation(ctx, conversationID); err != nil {
		return fmt.Errorf("agent_daemon: close: %w", err)
	}
	return nil
}

// ----------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------

// resolveAgentKind reads agent_kind from the agent config.
func resolveAgentKind(in connector.PromptInput) string {
	if v, ok := in.AgentConfig["agent_kind"].(string); ok {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return "claude_code"
}

// isSandboxMode reports whether the agent config requested sandbox
// deployment.
func isSandboxMode(in connector.PromptInput) bool {
	mode, _ := in.AgentConfig["daemon_mode"].(string)
	return mode == "sandbox"
}

// unboundRuntimeMessage returns the user-facing string for the
// "agent has no runtime_id" slow path. Sandbox-mode agents
// drill into sandbox_bindings to distinguish spawning vs. failed vs.
// never-attempted so the message matches reality.
//
// SandboxBindingReader is optional; a nil reader falls back to a
// generic "preparing" line under sandbox mode (the settings page
// pointer no longer applies under the eager-Acquire flow).
func (c *Connector) unboundRuntimeMessage(ctx context.Context, in connector.PromptInput) string {
	if !isSandboxMode(in) {
		return "This Agent has no Runtime bound. Pick one in the Agent settings and retry."
	}
	if c.sandboxBindings == nil {
		return "This Agent's sandbox is preparing — please retry shortly."
	}
	bindingRow, found, err := c.sandboxBindings.GetActiveSandboxBindingForAgent(ctx, in.WorkspaceID, in.AgentID)
	if err != nil {
		c.log.Warn("agent_daemon: sandbox binding lookup for unbound-runtime hint failed",
			"err", err, "agent_id", in.AgentID)
		return "This Agent's sandbox is preparing — please retry shortly."
	}
	if !found {
		return "This Agent has no runtime yet — ask an admin to rebuild it."
	}
	switch bindingRow.Status {
	case store.SandboxBindingStatusKilledError:
		return "This Agent's sandbox failed to start — click Rebuild in the Agent settings to retry."
	case store.SandboxBindingStatusKilled, store.SandboxBindingStatusKilledOrphaned:
		return "This Agent's sandbox was reclaimed — click Rebuild in the Agent settings to retry."
	default:
		// spawning / killing / running-but-runtime_id-not-yet-written
		// are all transient.
		return "This Agent's sandbox is starting up (~10s) — please retry shortly."
	}
}

func configuredDeviceBinding(in connector.PromptInput) (binding.Binding, bool) {
	deviceID, _ := in.AgentConfig["device_id"].(string)
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return binding.Binding{}, false
	}
	// store writes "workdir" via ConfigureDevAgentConnector;
	// older jsonb shapes used "work_dir" / "working_directory" — keep
	// all three so legacy rows still bind correctly.
	workDir := firstConfigString(in.AgentConfig, "work_dir", "workdir", "working_directory")
	return binding.Binding{
		ConversationID: in.ConversationID,
		AgentID:        in.AgentID,
		DeviceID:       deviceID,
		AgentKind:      resolveAgentKind(in),
		WorkDir:        workDir,
	}, true
}

// acquireSandboxBinding is the cold-start path: ErrNotBound and the
// agent is configured for sandbox mode. The provider blocks
// until the daemon's WS upgrade lands in gateway.Registry (it owns
// WaitForDevice internally), so by the time this returns the deviceID
// is guaranteed to resolve via registry.LookupDevice.
//
// Per agent_must_bind_runtime memory: this is no longer called by the
// default dispatch path. Kept for a future conversation-scoped
// ephemeral sandbox feature.
func (c *Connector) acquireSandboxBinding(ctx context.Context, in connector.PromptInput) (binding.Binding, error) {
	deviceID, err := c.sandbox.Acquire(ctx, in)
	if err != nil {
		if errors.Is(err, ErrSandboxProviderDisabled) {
			// Clean user-facing message — don't leak the internal env
			// var name; platform owner sees it in the server logs.
			c.log.Warn("agent_daemon: sandbox mode requested but provider not configured",
				"agent_id", in.AgentID)
			return binding.Binding{}, fmt.Errorf("sandbox mode requested but this deployment does not have a sandbox template configured; switch the agent to local-runtime mode or contact the platform owner")
		}
		return binding.Binding{}, fmt.Errorf("sandbox acquire: %w", err)
	}

	b := binding.Binding{
		ConversationID: in.ConversationID,
		AgentID:        in.AgentID,
		DeviceID:       deviceID,
		AgentKind:      resolveAgentKind(in),
		// WorkDir intentionally empty: parsar-daemon resolves a
		// per-conversation scratch dir and uses it for BOTH plugin
		// installs and the subprocess cwd so they stay on the same
		// tree regardless of the sandbox image's WORKDIR.
	}
	if err := c.binder.Bind(ctx, b); err != nil {
		// Don't tear down the sandbox: the next prompt's Acquire will
		// return the cached entry, and Bind may succeed once the DB
		// blip clears.
		c.log.Warn("agent_daemon: persist sandbox binding failed; sandbox kept",
			"err", err, "device_id", deviceID, "agent_id", in.AgentID)
		return binding.Binding{}, fmt.Errorf("persist sandbox binding: %w", err)
	}
	c.log.Info("agent_daemon: sandbox binding established",
		"device_id", deviceID,
		"conversation_id", in.ConversationID,
		"agent_id", in.AgentID)
	return b, nil
}

// errorChannel builds a closed PromptEvent channel that emits one
// EventError + EventDone. Used by StreamPrompt for pre-flight failure
// paths so the caller's range loop terminates without a goroutine.
func errorChannel(_ string, message string) <-chan connector.PromptEvent {
	ch := make(chan connector.PromptEvent, 2)
	ch <- connector.PromptEvent{Type: connector.EventError, Error: message}
	ch <- connector.PromptEvent{Type: connector.EventDone, Final: &connector.PromptOutput{}}
	close(ch)
	return ch
}

// usageFromProto translates wire-stable proto.Usage into the server's
// store.UsageInput. The indirection exists so the proto package can
// stay at module root (importable by apps/parsar-daemon) without depending
// on server/internal/store.
func usageFromProto(u proto.Usage) store.UsageInput {
	return store.UsageInput{
		Provider:     u.Provider,
		Model:        u.Model,
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		CostUSD:      u.CostUSD,
		Raw:          u.Raw,
	}
}

// promptAttachmentsFromStore translates server-side
// store.MessageAttachment into wire-format proto.PromptAttachment.
// Living here keeps server/internal/store out of the proto package
// (apps/parsar-daemon imports proto but MUST NOT import store).
//
// Entries with empty Kind or DataBase64 are dropped defensively
// against future direct-slice callers.
func promptAttachmentsFromStore(src []store.MessageAttachment) []proto.PromptAttachment {
	if len(src) == 0 {
		return nil
	}
	out := make([]proto.PromptAttachment, 0, len(src))
	for _, att := range src {
		if att.Kind == "" || att.DataBase64 == "" {
			continue
		}
		out = append(out, proto.PromptAttachment{
			Kind:       att.Kind,
			MIME:       att.MIME,
			DataBase64: att.DataBase64,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
