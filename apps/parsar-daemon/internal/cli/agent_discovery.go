package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudesdk"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/codex"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/mcode"
	opencodeagent "github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/opencode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/pi"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// agentCLIDiscovery is the daemon startup snapshot advertised in heartbeat.
type agentCLIDiscovery struct {
	ClaudeSDK  *claudeSDKDiscovery
	ClaudeCode proto.SupportedAgentKind
	OpenCode   proto.SupportedAgentKind
	Codex      proto.SupportedAgentKind
	Pi         proto.SupportedAgentKind
	MCode      proto.SupportedAgentKind
}

type agentCLIChecks struct {
	ClaudeSDK  func(context.Context, claudesdk.Config) (claudesdk.RuntimeInfo, error)
	ClaudeCode func(context.Context, string) (string, error)
	OpenCode   func(context.Context, string) (string, error)
	Codex      func(context.Context, string) (string, error)
	Pi         func(context.Context, string) (string, error)
	MCode      func(context.Context, string) (string, error)
}

func defaultAgentCLIChecks() agentCLIChecks {
	return agentCLIChecks{
		ClaudeCode: claudecode.CheckCLIAvailable,
		OpenCode:   opencodeagent.CheckCLIAvailable,
		Codex:      codex.CheckCLIAvailable,
		Pi:         pi.CheckCLIAvailable,
		MCode:      mcode.CheckCLIAvailable,
	}
}

func preflightAgentCLIs(rc *runContext, profile string) (agentCLIDiscovery, error) {
	return discoverAgentCLIs(rc, profile, defaultAgentCLIChecks())
}

func discoverAgentCLIs(rc *runContext, profile string, checks agentCLIChecks) (agentCLIDiscovery, error) {
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
				Streaming:            true,
				Permissions:          true,
				Usage:                true,
				Resume:               true,
				Steering:             true,
				DurableTurns:         true,
				DurableInputReceipts: true,
				FunctionTools:        true,
				MCPHTTPTools:         true,
				MCPHTTPBearerAuth:    true,
				MessageItems:         true,
				ToolItems:            true,
				ToolObservations:     true,
				EnvironmentNone:      true,
				WebSearchControl:     true,
				TextVerbosity:        codex.SupportsTextVerbosity,
				ExecutionControls:    codex.SupportsTextVerbosity,
				SubagentControl:      true,
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
		out.Codex.Capabilities.RemoteEnvironment = codex.SupportsRemoteEnvironment(codexVersion)
		out.Codex.Capabilities.MCPHTTPRemoteEnvironment = out.Codex.Capabilities.RemoteEnvironment
		out.Codex.Capabilities.MCPHTTPRemoteBearerAuth = out.Codex.Capabilities.RemoteEnvironment
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

	out.MCode = discoverMCode(rc, checks.MCode)
	out.ClaudeSDK = discoverClaudeSDK(rc, profile, checks.ClaudeSDK)

	if !out.ClaudeCode.Available && !out.OpenCode.Available && !out.Codex.Available && !out.Pi.Available && !out.MCode.Available && (out.ClaudeSDK == nil || !out.ClaudeSDK.Info.Available) {
		return out, fmt.Errorf("connect: no supported agent CLI available (install Claude Code, OpenCode, Codex, pi, or mcode, or configure a Claude SDK runtime)")
	}
	return out, nil
}
