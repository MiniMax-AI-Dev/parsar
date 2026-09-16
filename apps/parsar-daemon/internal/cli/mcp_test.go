package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
)

func TestMCPHTTPBearerDiscoveryExcludesUnconfiguredSDK(t *testing.T) {
	t.Setenv(claudeSDKEntrypointEnv, "")
	checks := unavailableCLIChecks()
	checks.Codex = func(context.Context, string) (string, error) { return "codex 0.153.4", nil }
	discovery, err := discoverAgentCLIs(&runContext{stdout: &strings.Builder{}, stderr: &strings.Builder{}}, "test", checks)
	if err != nil {
		t.Fatal(err)
	}
	registry := agent.NewRegistry()
	registerAgentKinds(registry, discovery, "https://service.example")
	for _, kind := range registry.SupportedAgentKinds() {
		if kind.Capabilities.MCPHTTPBearerAuth != (kind.Kind == "codex") {
			t.Fatal("bearer capability missing or advertised for another adapter")
		}
		if kind.Kind == "codex" && (!kind.Available || !kind.Capabilities.MCPHTTPTools || !kind.Capabilities.EnvironmentNone) {
			t.Fatal("bearer capability lacks prerequisite profile")
		}
	}
}

func TestRemoteMCPDiscoveryRequiresPinnedNative(t *testing.T) {
	for _, version := range []string{"codex-cli 0.153.4", "codex-cli 0.153.3", "codex-cli 0.154.0"} {
		checks := unavailableCLIChecks()
		checks.Codex = func(context.Context, string) (string, error) { return version, nil }
		got, err := discoverAgentCLIs(&runContext{stdout: &strings.Builder{}, stderr: &strings.Builder{}}, "test", checks)
		if err != nil {
			t.Fatal(err)
		}
		if got.Codex.Capabilities.MCPHTTPRequired != (version == "codex-cli 0.153.4") || got.Codex.Capabilities.MCPHTTPRemoteEnvironment != (version == "codex-cli 0.153.4") || got.Codex.Capabilities.MCPHTTPRemoteBearerAuth != (version == "codex-cli 0.153.4") {
			t.Fatal("unverified native combination advertised")
		}
		if got.ClaudeCode.Capabilities.MCPHTTPRequired || got.OpenCode.Capabilities.MCPHTTPRequired || got.Pi.Capabilities.MCPHTTPRequired || got.ClaudeCode.Capabilities.MCPHTTPRemoteEnvironment || got.OpenCode.Capabilities.MCPHTTPRemoteEnvironment || got.Pi.Capabilities.MCPHTTPRemoteEnvironment || got.ClaudeCode.Capabilities.MCPHTTPRemoteBearerAuth || got.OpenCode.Capabilities.MCPHTTPRemoteBearerAuth || got.Pi.Capabilities.MCPHTTPRemoteBearerAuth {
			t.Fatal("other engine advertised combination")
		}
	}
}
