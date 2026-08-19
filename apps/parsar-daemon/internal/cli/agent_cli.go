// Agent CLI preflight: one table describing every engine the daemon can
// drive, its heartbeat capability descriptor, and how to probe its binary.
// Split out of connect.go so adding an engine touches one table instead of
// appending another copy of the probe/report block.
package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/codex"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/deepseekharness"
	opencodeagent "github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/opencode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/pi"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// agentCLIDiscovery is the daemon startup snapshot advertised in heartbeat.
type agentCLIDiscovery struct {
	ClaudeCode      proto.SupportedAgentKind
	OpenCode        proto.SupportedAgentKind
	Codex           proto.SupportedAgentKind
	Pi              proto.SupportedAgentKind
	DeepseekHarness proto.SupportedAgentKind
}

type versionCheck func(context.Context, string) (string, error)

type agentCLIChecks struct {
	ClaudeCode      versionCheck
	OpenCode        versionCheck
	Codex           versionCheck
	Pi              versionCheck
	DeepseekHarness versionCheck
}

func defaultAgentCLIChecks() agentCLIChecks {
	return agentCLIChecks{
		ClaudeCode:      claudecode.CheckCLIAvailable,
		OpenCode:        opencodeagent.CheckCLIAvailable,
		Codex:           codex.CheckCLIAvailable,
		Pi:              pi.CheckCLIAvailable,
		DeepseekHarness: deepseekharness.CheckCLIAvailable,
	}
}

// agentCLIProbe is one engine's preflight: where its descriptor lives, how
// to detect the binary, and the operator-facing strings used to report it.
type agentCLIProbe struct {
	slot        *proto.SupportedAgentKind
	check       versionCheck
	fallback    versionCheck
	label       string
	versionCmd  string
	notFoundErr error
	installURL  string
}

func preflightAgentCLIs(rc *runContext) (agentCLIDiscovery, error) {
	return discoverAgentCLIs(rc, defaultAgentCLIChecks())
}

func discoverAgentCLIs(rc *runContext, checks agentCLIChecks) (agentCLIDiscovery, error) {
	out := agentCLIDescriptors()
	probes := []agentCLIProbe{
		{
			slot:        &out.ClaudeCode,
			check:       checks.ClaudeCode,
			fallback:    claudecode.CheckCLIAvailable,
			label:       "Claude Code",
			versionCmd:  "claude --version",
			notFoundErr: claudecode.ErrCLINotFound,
			installURL:  claudecode.InstallURL,
		},
		{
			slot:        &out.OpenCode,
			check:       checks.OpenCode,
			fallback:    opencodeagent.CheckCLIAvailable,
			label:       "OpenCode",
			versionCmd:  "opencode --version",
			notFoundErr: opencodeagent.ErrCLINotFound,
			installURL:  opencodeagent.InstallURL,
		},
		{
			slot:        &out.Codex,
			check:       checks.Codex,
			fallback:    codex.CheckCLIAvailable,
			label:       "Codex",
			versionCmd:  "codex --version",
			notFoundErr: codex.ErrCLINotFound,
			installURL:  codex.InstallURL,
		},
		{
			slot:        &out.Pi,
			check:       checks.Pi,
			fallback:    pi.CheckCLIAvailable,
			label:       "pi",
			versionCmd:  "pi --version",
			notFoundErr: pi.ErrCLINotFound,
			installURL:  pi.InstallURL,
		},
		{
			slot:        &out.DeepseekHarness,
			check:       checks.DeepseekHarness,
			fallback:    deepseekharness.CheckCLIAvailable,
			label:       "DeepSeek Harness",
			versionCmd:  "dsh --version",
			notFoundErr: deepseekharness.ErrCLINotFound,
			installURL:  deepseekharness.InstallURL,
		},
	}

	available := 0
	for _, probe := range probes {
		if runAgentCLIProbe(rc, probe) {
			available++
		}
	}
	if available == 0 {
		return out, fmt.Errorf("connect: no supported agent CLI available (install Claude Code, OpenCode, Codex, pi, or DeepSeek Harness)")
	}
	return out, nil
}

// runAgentCLIProbe fills the descriptor in place and reports the outcome to
// the operator. Returns whether the CLI is usable.
func runAgentCLIProbe(rc *runContext, probe agentCLIProbe) bool {
	check := probe.check
	if check == nil {
		check = probe.fallback
	}
	ctx, cancel := context.WithTimeout(context.Background(), cliVersionTimeout)
	version, err := check(ctx, "")
	cancel()

	switch {
	case err == nil:
		probe.slot.Available = true
		probe.slot.Version = version
		fmt.Fprintf(rc.stdout, "%s preflight ok (%s)\n", probe.label, version)
		return true
	case errors.Is(err, probe.notFoundErr):
		fmt.Fprintf(rc.stderr, "parsar-daemon: %s CLI not found on PATH; %s unavailable.\n", probe.label, probe.slot.Kind)
		fmt.Fprintf(rc.stderr, "  Install instructions: %s\n", probe.installURL)
	default:
		fmt.Fprintf(rc.stderr, "parsar-daemon: `%s` failed; %s unavailable: %v\n", probe.versionCmd, probe.slot.Kind, err)
		fmt.Fprintf(rc.stderr, "  Re-install or upgrade: %s\n", probe.installURL)
	}
	return false
}

// agentCLIDescriptors is the capability contract the server reads from the
// heartbeat. Availability and version are filled in by the probes.
func agentCLIDescriptors() agentCLIDiscovery {
	return agentCLIDiscovery{
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
				Streaming:   true,
				Permissions: true,
				Usage:       true,
				Resume:      true,
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
		DeepseekHarness: proto.SupportedAgentKind{
			Kind: "deepseek_harness",
			// `dsh --profile headless` is the harness's only supported
			// automation surface: it prints the final assistant text and
			// exits, with no event stream, token accounting, resume flag,
			// or approval channel to advertise.
			Capabilities: proto.AgentKindCapabilities{},
		},
	}
}

func registerAgentKinds(registry *agent.Registry, agentCLIs agentCLIDiscovery) {
	registry.RegisterKind(agentCLIs.ClaudeCode, claudecode.Factory)
	registry.RegisterKind(agentCLIs.OpenCode, opencodeagent.Factory)
	registry.RegisterKind(agentCLIs.Codex, codex.Factory)
	registry.RegisterKind(agentCLIs.Pi, pi.Factory)
	registry.RegisterKind(agentCLIs.DeepseekHarness, deepseekharness.Factory)
}
