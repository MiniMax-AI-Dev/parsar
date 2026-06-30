package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/codex"
	opencodeagent "github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/opencode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/pi"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/daemonize"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/transport"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

const (
	// cliVersionTimeout caps CLI `--version` preflights so a hung agent
	// binary can't keep `parsar-daemon connect` blocked at startup.
	cliVersionTimeout = 5 * time.Second

	bootstrapTimeout = 10 * time.Second

	killTimeout = 3 * time.Second

	connectInlineURLEnv        = "PARSAR_DAEMON_CONNECT_URL"
	connectInlineTokenEnv      = "PARSAR_DAEMON_CONNECT_TOKEN"
	connectInlineDeviceNameEnv = "PARSAR_DAEMON_CONNECT_DEVICE_NAME"
)

// runConnect dials /agent-daemon/bootstrap, opens /agent-daemon/ws,
// wires the dispatch router, and routes Envelope traffic both ways
// until either SIGINT/SIGTERM or a permanent credential rejection.
//
// `connect --url --token` folds one-shot pairing into the connect step:
// the daemon consumes the pairing token, persists the returned runner
// credential to auth.json, and connects. Subsequent `connect -b`
// invocations reload the persisted profile.
//
// -b re-execs the binary in the background with stdio redirected to
// connect.log and the child PID written to connect.pid. The child
// re-enters runConnect via BackgroundSentinelEnv. When --token is
// supplied, the parent forks before pairing so the one-shot token is
// consumed by the long-lived child. Inline pairing flags are scrubbed
// from child argv and passed via environment to keep the token out of
// process listings.
func runConnect(ctx *runContext, args []string) error {
	fs := newFlagSet("connect")
	var (
		profile    = fs.String("profile", paths.DefaultProfile, "profile name for reading legacy auth.json state or writing pid/log files")
		background = fs.Bool("b", false, "fork into the background; writes connect.pid + connect.log")
		serverURL  = fs.String("url", "", "Parsar server base URL; with --token, pair inline before connecting")
		token      = fs.String("token", "", "pairing token; with --url, connect consumes it without writing auth.json")
		deviceName = fs.String("device-name", "", "human label for inline pairing (defaults to hostname)")
	)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("connect: parse flags: %w", err)
	}
	// Hydrate inline pairing inputs from env in BOTH parent and the
	// re-execed background child. Server-spawned sandboxes pass the
	// token via PARSAR_DAEMON_CONNECT_TOKEN/URL env rather than --url
	// /--token flags; without this hydration before the pre-fork
	// auth.json check below, the parent would take the "rely on
	// auth.json" branch and bail with "not paired". Idempotent —
	// fills only empty flags and unsets the env after consuming.
	loadInlineConnectEnv(serverURL, token, deviceName)
	if err := paths.ValidateProfile(*profile); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	inlinePair := strings.TrimSpace(*serverURL) != "" || strings.TrimSpace(*token) != ""
	if inlinePair {
		if strings.TrimSpace(*serverURL) == "" {
			return fmt.Errorf("connect: --url is required when --token is supplied")
		}
		if strings.TrimSpace(*token) == "" {
			return fmt.Errorf("connect: --token is required when --url is supplied")
		}
	}

	// -b mode: parent forks, child re-enters with sentinel env set
	// and skips this branch. Fork before inline pairing so the
	// one-shot token is consumed by the child that owns the WS loop.
	if *background && !daemonize.IsBackgroundChild() {
		// Validate auth.json exists before forking so the error
		// surfaces in the user's terminal instead of the background
		// child's log.
		if !inlinePair {
			if _, err := auth.Load(*profile); err != nil {
				return fmt.Errorf("connect: %w", err)
			}
		}
		argv := os.Args
		extraEnv := []string(nil)
		if inlinePair {
			argv = scrubInlineConnectArgs(os.Args)
			extraEnv = inlineConnectEnv(*serverURL, *token, *deviceName)
		}
		return spawnBackground(ctx, *profile, argv, extraEnv)
	}

	// Self-check before pairing/loading credentials so a machine with
	// no supported agent CLI fails before consuming a one-shot token.
	agentCLIs, err := preflightAgentCLIs(ctx)
	if err != nil {
		return err
	}

	prof, err := resolveConnectProfile(*profile, *serverURL, *token, *deviceName)
	if err != nil {
		return err
	}

	return mainLoop(ctx, *profile, prof, agentCLIs)
}

func loadInlineConnectEnv(serverURL, token, deviceName *string) {
	if strings.TrimSpace(*serverURL) == "" {
		*serverURL = os.Getenv(connectInlineURLEnv)
	}
	if strings.TrimSpace(*token) == "" {
		*token = os.Getenv(connectInlineTokenEnv)
	}
	if strings.TrimSpace(*deviceName) == "" {
		*deviceName = os.Getenv(connectInlineDeviceNameEnv)
	}
	_ = os.Unsetenv(connectInlineURLEnv)
	_ = os.Unsetenv(connectInlineTokenEnv)
	_ = os.Unsetenv(connectInlineDeviceNameEnv)
}

func inlineConnectEnv(serverURL, token, deviceName string) []string {
	out := []string{
		connectInlineURLEnv + "=" + serverURL,
		connectInlineTokenEnv + "=" + token,
	}
	if strings.TrimSpace(deviceName) != "" {
		out = append(out, connectInlineDeviceNameEnv+"="+deviceName)
	}
	return out
}

func scrubInlineConnectArgs(argv []string) []string {
	out := make([]string, 0, len(argv))
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case arg == "--url" || arg == "--token" || arg == "--device-name":
			i++
			continue
		case strings.HasPrefix(arg, "--url=") || strings.HasPrefix(arg, "--token=") || strings.HasPrefix(arg, "--device-name="):
			continue
		default:
			out = append(out, arg)
		}
	}
	return out
}

func resolveConnectProfile(profile, serverURL, token, deviceName string) (auth.Profile, error) {
	if strings.TrimSpace(serverURL) == "" && strings.TrimSpace(token) == "" {
		prof, err := auth.Load(profile)
		if err != nil {
			return auth.Profile{}, fmt.Errorf("connect: %w", err)
		}
		return prof, nil
	}

	pairCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prof, _, err := pairProfile(pairCtx, serverURL, token, deviceName)
	if err != nil {
		return auth.Profile{}, fmt.Errorf("connect: pair with server: %w", err)
	}
	if err := auth.Save(profile, prof); err != nil {
		return auth.Profile{}, fmt.Errorf("connect: save auth profile: %w", err)
	}
	return prof, nil
}

// agentCLIDiscovery is the daemon startup snapshot advertised in heartbeat.
type agentCLIDiscovery struct {
	ClaudeCode proto.SupportedAgentKind
	OpenCode   proto.SupportedAgentKind
	Codex      proto.SupportedAgentKind
	Pi         proto.SupportedAgentKind
}

type agentCLIChecks struct {
	ClaudeCode func(context.Context, string) (string, error)
	OpenCode   func(context.Context, string) (string, error)
	Codex      func(context.Context, string) (string, error)
	Pi         func(context.Context, string) (string, error)
}

func defaultAgentCLIChecks() agentCLIChecks {
	return agentCLIChecks{
		ClaudeCode: claudecode.CheckCLIAvailable,
		OpenCode:   opencodeagent.CheckCLIAvailable,
		Codex:      codex.CheckCLIAvailable,
		Pi:         pi.CheckCLIAvailable,
	}
}

func preflightAgentCLIs(rc *runContext) (agentCLIDiscovery, error) {
	return discoverAgentCLIs(rc, defaultAgentCLIChecks())
}

func discoverAgentCLIs(rc *runContext, checks agentCLIChecks) (agentCLIDiscovery, error) {
	if checks.ClaudeCode == nil {
		checks.ClaudeCode = claudecode.CheckCLIAvailable
	}
	if checks.OpenCode == nil {
		checks.OpenCode = opencodeagent.CheckCLIAvailable
	}
	if checks.Codex == nil {
		checks.Codex = codex.CheckCLIAvailable
	}
	if checks.Pi == nil {
		checks.Pi = pi.CheckCLIAvailable
	}
	out := agentCLIDiscovery{
		ClaudeCode: proto.SupportedAgentKind{
			Kind: "claude_code",
			Capabilities: proto.AgentKindCapabilities{
				Streaming:   true,
				Permissions: true,
				Usage:       true,
				Resume:      true,
			},
		},
		OpenCode: proto.SupportedAgentKind{
			Kind: "opencode",
			Capabilities: proto.AgentKindCapabilities{
				Streaming: true,
				Usage:     true,
			},
		},
		Codex: proto.SupportedAgentKind{
			Kind: "codex",
			Capabilities: proto.AgentKindCapabilities{
				// First-cut adapter runs silent (no permission cards),
				// but streaming + usage + resume are all wired via
				// the JSON-RPC notification stream + thread/resume RPC.
				Streaming: true,
				Usage:     true,
				Resume:    true,
			},
		},
		Pi: proto.SupportedAgentKind{
			Kind: "pi",
			Capabilities: proto.AgentKindCapabilities{
				// pi runs --no-approve, so no permission cards; streaming,
				// usage, and --session resume are all wired.
				Streaming: true,
				Usage:     true,
				Resume:    true,
			},
		},
	}

	claudeCtx, cancelClaude := context.WithTimeout(context.Background(), cliVersionTimeout)
	claudeVersion, claudeErr := checks.ClaudeCode(claudeCtx, "")
	cancelClaude()
	if claudeErr == nil {
		out.ClaudeCode.Available = true
		out.ClaudeCode.Version = claudeVersion
		fmt.Fprintf(rc.stdout, "Claude Code preflight ok (%s)\n", claudeVersion)
	} else if errors.Is(claudeErr, claudecode.ErrCLINotFound) {
		fmt.Fprintln(rc.stderr, "parsar-daemon: Claude Code CLI not found on PATH; claude_code unavailable.")
		fmt.Fprintf(rc.stderr, "  Install instructions: %s\n", claudecode.InstallURL)
	} else {
		fmt.Fprintf(rc.stderr, "parsar-daemon: `claude --version` failed; claude_code unavailable: %v\n", claudeErr)
		fmt.Fprintf(rc.stderr, "  Re-install or upgrade: %s\n", claudecode.InstallURL)
	}

	opencodeCtx, cancelOpenCode := context.WithTimeout(context.Background(), cliVersionTimeout)
	opencodeVersion, opencodeErr := checks.OpenCode(opencodeCtx, "")
	cancelOpenCode()
	if opencodeErr == nil {
		out.OpenCode.Available = true
		out.OpenCode.Version = opencodeVersion
		fmt.Fprintf(rc.stdout, "OpenCode preflight ok (%s)\n", opencodeVersion)
	} else if errors.Is(opencodeErr, opencodeagent.ErrCLINotFound) {
		fmt.Fprintln(rc.stderr, "parsar-daemon: OpenCode CLI not found on PATH; opencode unavailable.")
		fmt.Fprintf(rc.stderr, "  Install instructions: %s\n", opencodeagent.InstallURL)
	} else {
		fmt.Fprintf(rc.stderr, "parsar-daemon: `opencode --version` failed; opencode unavailable: %v\n", opencodeErr)
		fmt.Fprintf(rc.stderr, "  Re-install or upgrade: %s\n", opencodeagent.InstallURL)
	}

	codexCtx, cancelCodex := context.WithTimeout(context.Background(), cliVersionTimeout)
	codexVersion, codexErr := checks.Codex(codexCtx, "")
	cancelCodex()
	if codexErr == nil {
		out.Codex.Available = true
		out.Codex.Version = codexVersion
		fmt.Fprintf(rc.stdout, "Codex preflight ok (%s)\n", codexVersion)
	} else if errors.Is(codexErr, codex.ErrCLINotFound) {
		fmt.Fprintln(rc.stderr, "parsar-daemon: Codex CLI not found on PATH; codex unavailable.")
		fmt.Fprintf(rc.stderr, "  Install instructions: %s\n", codex.InstallURL)
	} else {
		fmt.Fprintf(rc.stderr, "parsar-daemon: `codex --version` failed; codex unavailable: %v\n", codexErr)
		fmt.Fprintf(rc.stderr, "  Re-install or upgrade: %s\n", codex.InstallURL)
	}

	piCtx, cancelPi := context.WithTimeout(context.Background(), cliVersionTimeout)
	piVersion, piErr := checks.Pi(piCtx, "")
	cancelPi()
	if piErr == nil {
		out.Pi.Available = true
		out.Pi.Version = piVersion
		fmt.Fprintf(rc.stdout, "pi preflight ok (%s)\n", piVersion)
	} else if errors.Is(piErr, pi.ErrCLINotFound) {
		fmt.Fprintln(rc.stderr, "parsar-daemon: pi CLI not found on PATH; pi unavailable.")
		fmt.Fprintf(rc.stderr, "  Install instructions: %s\n", pi.InstallURL)
	} else {
		fmt.Fprintf(rc.stderr, "parsar-daemon: `pi --version` failed; pi unavailable: %v\n", piErr)
		fmt.Fprintf(rc.stderr, "  Re-install or upgrade: %s\n", pi.InstallURL)
	}

	if !out.ClaudeCode.Available && !out.OpenCode.Available && !out.Codex.Available && !out.Pi.Available {
		return out, fmt.Errorf("connect: no supported agent CLI available (install Claude Code, OpenCode, Codex, or pi)")
	}
	return out, nil
}

func registerAgentKinds(registry *agent.Registry, agentCLIs agentCLIDiscovery) {
	registry.RegisterKind(agentCLIs.ClaudeCode, claudecode.Factory)
	registry.RegisterKind(agentCLIs.OpenCode, opencodeagent.Factory)
	registry.RegisterKind(agentCLIs.Codex, codex.Factory)
	registry.RegisterKind(agentCLIs.Pi, pi.Factory)
}

// spawnBackground forks the daemon into the background. Parent
// returns after printing the child PID; child re-enters runConnect
// with BackgroundSentinelEnv set so the same mainLoop runs in either
// mode.
func spawnBackground(rc *runContext, profile string, argv []string, extraEnv []string) error {
	logPath, err := paths.LogFile(profile)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	pidPath, err := paths.PIDFile(profile)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	// Refuse to start a second background daemon for the same profile.
	if pid, err := daemonize.ReadPIDFile(pidPath); err == nil {
		return fmt.Errorf("connect: background daemon already running (pid=%d); run `parsar-daemon stop` first", pid)
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, daemonize.ErrStaleOrCorrupt) {
		return fmt.Errorf("connect: check pidfile: %w", err)
	}
	// Stale pidfile → remove so WritePIDFile starts clean.
	_ = daemonize.RemovePIDFile(pidPath)

	if err := daemonize.EnsureLogFile(logPath); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	pid, err := daemonize.Spawn(argv, daemonize.ReExecOptions{
		LogPath:  logPath,
		PIDPath:  pidPath,
		ExtraEnv: extraEnv,
	})
	if err != nil {
		return fmt.Errorf("connect: spawn background: %w", err)
	}

	fmt.Fprintf(rc.stdout, "parsar-daemon: backgrounded (pid=%d)\n", pid)
	fmt.Fprintf(rc.stdout, "  logs : %s\n", logPath)
	fmt.Fprintf(rc.stdout, "  pid  : %s\n", pidPath)
	fmt.Fprintf(rc.stdout, "  stop : parsar-daemon stop --profile %s\n", profile)
	return nil
}

// mainLoop is the daemon body — runs in foreground and in the re-execed
// background process. SIGINT / SIGTERM cancels the root context, which
// unblocks the read pump and any in-flight Send so the daemon exits
// without orphaning agent subprocesses.
func mainLoop(rc *runContext, profile string, prof auth.Profile, agentCLIs agentCLIDiscovery) error {
	// Route through obs/log so daemon log lines pick up the same
	// trace_id / span_id auto-injection as the server side — when the
	// daemon adopts an envelope's trace, every log call under that ctx
	// gets the same trace_id so `grep <trace_id>` finds the line on
	// both ends.
	obslog.Init(obslog.Config{
		Format: "text",
		Level:  slog.LevelInfo,
		Out:    rc.stderr,
	})

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Honour SIGINT / SIGTERM as graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-sigCh:
			obslog.Bg().Info("received signal, shutting down", "signal", sig.String())
			cancel()
		case <-rootCtx.Done():
		}
		signal.Stop(sigCh)
	}()

	bootCtx, bootCancel := context.WithTimeout(rootCtx, bootstrapTimeout)
	boot, err := transport.Bootstrap(bootCtx, prof.ServerURL, prof.RuntimeID, prof.RunnerCredential, Version)
	bootCancel()
	if err != nil {
		return fmt.Errorf("connect: bootstrap: %w", err)
	}
	wsURL, err := transport.DeriveWSURL(*boot, prof.ServerURL)
	if err != nil {
		return fmt.Errorf("connect: derive ws url: %w", err)
	}
	obslog.Bg().Info("bootstrap ok", "device_id", boot.DeviceID, "ws_url", wsURL, "heartbeat_interval", boot.HeartbeatInterval())

	registry := agent.NewRegistry()
	registerAgentKinds(registry, agentCLIs)

	dial := func(ctx context.Context) (*transport.Conn, error) {
		return transport.Dial(ctx, transport.DialOptions{
			WSURL:      wsURL,
			DeviceID:   boot.DeviceID,
			Credential: prof.RunnerCredential,
			// DaemonVersion is the WIRE-PROTOCOL version, not the build
			// tag. proto.VersionCompatible is a strict major.minor
			// match against proto.Version. Build-tag reporting goes
			// in heartbeat's DaemonVersion field.
			DaemonVersion: proto.Version,
		})
	}

	for {
		if err := rootCtx.Err(); err != nil {
			return nil
		}

		conn, err := transport.Reconnect(rootCtx, dial, transport.DefaultBackoff, func(attempt int, lastDelay time.Duration, lastErr error) {
			switch {
			case attempt == 1:
				obslog.Bg().Info("connecting", "ws_url", wsURL)
			case lastErr != nil:
				// Include lastErr so a stuck Reconnect tells the
				// operator WHY ("ws upgrade rejected with 426")
				// instead of just "retry attempt 3 after 4s".
				obslog.Bg().Warn("dial retry", "attempt", attempt, "delay", lastDelay, "err", lastErr)
			default:
				obslog.Bg().Warn("dial retry", "attempt", attempt, "delay", lastDelay)
			}
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			if errors.Is(err, transport.ErrPermanent) {
				return fmt.Errorf("connect: permanent error (re-pair the daemon): %w", err)
			}
			return fmt.Errorf("connect: dial: %w", err)
		}
		obslog.Bg().Info("ws connected", "device_id", conn.DeviceID())

		// pumpConn returns on conn close (peer hangup, transport
		// error, root ctx cancel). Loop back into Reconnect unless
		// root ctx is cancelled.
		pumpErr := pumpConn(rootCtx, conn, registry, boot, agentCLIs)
		if pumpErr != nil {
			obslog.Bg().Warn("ws session ended", "err", pumpErr)
		} else {
			obslog.Bg().Info("ws session ended cleanly")
		}
		_ = conn.Close()

		// Server-initiated clean close (e.g. shutdown) → exit;
		// otherwise loop back and reconnect.
		if rootCtx.Err() != nil {
			return nil
		}
		// Permanent error (e.g. runtime deleted) → exit instead of
		// reconnecting.
		if pumpErr != nil && errors.Is(pumpErr, transport.ErrPermanent) {
			return fmt.Errorf("connect: runtime deleted (re-pair the daemon): %w", pumpErr)
		}
		// Small breather before redialing so a flapping server doesn't
		// get a tight loop of upgrade requests.
		_ = transport.Sleep(rootCtx, 1*time.Second)
	}
}

// pumpConn runs the per-connection workload: a dispatch.Router fed by
// conn.Recv(), heartbeats every boot.HeartbeatInterval(), and a
// graceful router.Shutdown on exit so any in-flight subprocesses get
// SIGTERM.
func pumpConn(parentCtx context.Context, conn *transport.Conn, registry *agent.Registry, boot *transport.BootstrapResponse, agentCLIs agentCLIDiscovery) error {
	router, err := dispatch.New(dispatch.Config{
		Registry: registry,
		Sender:   conn,
		Log:      obslog.Bg(),
	})
	if err != nil {
		return fmt.Errorf("router init: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = router.Shutdown(shutdownCtx)
		cancel()
	}()

	conn.StartHeartbeats(parentCtx, boot.HeartbeatInterval(), func() proto.HeartbeatPayload {
		return proto.HeartbeatPayload{
			Timestamp:           time.Now().Unix(),
			ActiveRequests:      router.ActiveRuns(),
			DaemonVersion:       Version,
			ClaudeAvailable:     agentCLIs.ClaudeCode.Available, // legacy server compatibility
			SupportedAgentKinds: registry.SupportedAgentKinds(),
		}
	}, obslog.Bg().With("component", "heartbeat"))

	obslog.Bg().Info("pumpConn: entering recv loop")
	for {
		select {
		case <-parentCtx.Done():
			obslog.Bg().Warn("pumpConn: parentCtx cancelled", "err", parentCtx.Err())
			return parentCtx.Err()
		case <-conn.Done():
			obslog.Bg().Warn("pumpConn: conn.Done fired", "err", conn.Err())
			return conn.Err()
		case env, ok := <-conn.Recv():
			if !ok {
				obslog.Bg().Warn("pumpConn: recvCh closed", "err", conn.Err())
				return conn.Err()
			}
			obslog.Bg().Info("pumpConn: received envelope, calling router.Handle", "type", env.Type, "id", env.ID)
			if err := router.Handle(parentCtx, env); err != nil {
				obslog.Bg().Error("router.Handle failed", "type", env.Type, "id", env.ID, "err", err)
			} else {
				obslog.Bg().Info("pumpConn: router.Handle ok", "type", env.Type, "id", env.ID)
			}
		}
	}
}
