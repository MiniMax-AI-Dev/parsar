package cli

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/codex"
	opencodeagent "github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/opencode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/pi"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestScrubInlineConnectArgsRemovesTokenURLAndDeviceName(t *testing.T) {
	got := scrubInlineConnectArgs([]string{
		"parsar-daemon", "connect",
		"--url", "https://parsar.example.com",
		"--token=rtk_secret",
		"--device-name", "dev-1",
		"-b",
		"--profile", "sandbox",
	})
	want := []string{"parsar-daemon", "connect", "-b", "--profile", "sandbox"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scrubInlineConnectArgs() = %#v, want %#v", got, want)
	}
}

func TestLoadInlineConnectEnvFillsMissingValuesAndUnsets(t *testing.T) {
	t.Setenv(connectInlineURLEnv, "https://parsar.example.com")
	t.Setenv(connectInlineTokenEnv, "rtk_secret")
	t.Setenv(connectInlineDeviceNameEnv, "dev-1")

	serverURL, token, deviceName := "", "", ""
	loadInlineConnectEnv(&serverURL, &token, &deviceName)

	if serverURL != "https://parsar.example.com" || token != "rtk_secret" || deviceName != "dev-1" {
		t.Fatalf("loaded values = (%q, %q, %q)", serverURL, token, deviceName)
	}
	if got := inlineConnectEnvValue(connectInlineTokenEnv); got != "" {
		t.Fatalf("%s still set to %q", connectInlineTokenEnv, got)
	}
}

func inlineConnectEnvValue(key string) string { return os.Getenv(key) }

// Regression: pre-fork auth.json check used to run BEFORE env-to-flag
// hydration, so sandboxes passing the token via env bailed with
// "not paired". loadInlineConnectEnv now runs first.
func TestLoadInlineConnectEnvHydratesParentProcessFlags(t *testing.T) {
	t.Setenv(connectInlineURLEnv, "https://parsar.example.com")
	t.Setenv(connectInlineTokenEnv, "rtk_secret")

	serverURL, token, deviceName := "", "", ""

	loadInlineConnectEnv(&serverURL, &token, &deviceName)

	// Same predicate runConnect uses to decide whether to skip the
	// pre-fork auth.json check.
	inlinePair := strings.TrimSpace(serverURL) != "" || strings.TrimSpace(token) != ""
	if !inlinePair {
		t.Fatalf("inlinePair=false after env hydration; serverURL=%q token=%q", serverURL, token)
	}
}

func TestDiscoverAgentCLIsAllowsOpenCodeWithoutClaude(t *testing.T) {
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	rc := &runContext{stdout: stdout, stderr: stderr}
	got, err := discoverAgentCLIs(rc, agentCLIChecks{
		ClaudeCode: func(context.Context, string) (string, error) {
			return "", claudecode.ErrCLINotFound
		},
		OpenCode: func(context.Context, string) (string, error) {
			return "opencode 1.4.3", nil
		},
		Codex: func(context.Context, string) (string, error) {
			return "", codex.ErrCLINotFound
		},
		Pi: func(context.Context, string) (string, error) {
			return "", pi.ErrCLINotFound
		},
	})
	if err != nil {
		t.Fatalf("discoverAgentCLIs: %v", err)
	}
	if got.ClaudeCode.Available {
		t.Fatalf("ClaudeCode.Available = true, want false: %#v", got.ClaudeCode)
	}
	if !got.OpenCode.Available || got.OpenCode.Version != "opencode 1.4.3" {
		t.Fatalf("OpenCode descriptor = %#v", got.OpenCode)
	}
	if got.Codex.Available {
		t.Fatalf("Codex.Available = true, want false: %#v", got.Codex)
	}
	if got.Pi.Available {
		t.Fatalf("Pi.Available = true, want false: %#v", got.Pi)
	}
	if !got.OpenCode.Capabilities.Streaming || !got.OpenCode.Capabilities.Usage || got.OpenCode.Capabilities.Permissions {
		t.Fatalf("OpenCode capabilities = %#v", got.OpenCode.Capabilities)
	}
	if !strings.Contains(stdout.String(), "OpenCode preflight ok") {
		t.Fatalf("stdout missing OpenCode ok line: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "claude_code unavailable") {
		t.Fatalf("stderr missing Claude unavailable line: %q", stderr.String())
	}
}

func TestDiscoverAgentCLIsBothMissingFails(t *testing.T) {
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	rc := &runContext{stdout: stdout, stderr: stderr}
	got, err := discoverAgentCLIs(rc, agentCLIChecks{
		ClaudeCode: func(context.Context, string) (string, error) {
			return "", claudecode.ErrCLINotFound
		},
		OpenCode: func(context.Context, string) (string, error) {
			return "", opencodeagent.ErrCLINotFound
		},
		Codex: func(context.Context, string) (string, error) {
			return "", codex.ErrCLINotFound
		},
		Pi: func(context.Context, string) (string, error) {
			return "", pi.ErrCLINotFound
		},
	})
	if err == nil {
		t.Fatalf("expected error when all CLIs missing, got descriptors %#v", got)
	}
	if !strings.Contains(err.Error(), "no supported agent CLI") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ClaudeCode.Available || got.OpenCode.Available || got.Codex.Available || got.Pi.Available {
		t.Fatalf("available descriptors after missing CLIs: %#v", got)
	}
}

func TestDiscoverAgentCLIsBothAvailable(t *testing.T) {
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	rc := &runContext{stdout: stdout, stderr: stderr}
	got, err := discoverAgentCLIs(rc, agentCLIChecks{
		ClaudeCode: func(context.Context, string) (string, error) {
			return "claude 2.0.0", nil
		},
		OpenCode: func(context.Context, string) (string, error) {
			return "opencode 1.4.3", nil
		},
		Codex: func(context.Context, string) (string, error) {
			return "codex 0.141.0", nil
		},
		Pi: func(context.Context, string) (string, error) {
			return "pi 0.1.0", nil
		},
	})
	if err != nil {
		t.Fatalf("discoverAgentCLIs: %v", err)
	}
	if !got.ClaudeCode.Available || got.ClaudeCode.Version != "claude 2.0.0" {
		t.Fatalf("ClaudeCode descriptor = %#v", got.ClaudeCode)
	}
	if !got.OpenCode.Available || got.OpenCode.Version != "opencode 1.4.3" {
		t.Fatalf("OpenCode descriptor = %#v", got.OpenCode)
	}
	if !got.Codex.Available || got.Codex.Version != "codex 0.141.0" {
		t.Fatalf("Codex descriptor = %#v", got.Codex)
	}
	if !got.ClaudeCode.Capabilities.Permissions || !got.ClaudeCode.Capabilities.Resume {
		t.Fatalf("ClaudeCode capabilities = %#v", got.ClaudeCode.Capabilities)
	}
	if !got.Codex.Capabilities.Streaming || !got.Codex.Capabilities.Resume || got.Codex.Capabilities.Permissions {
		t.Fatalf("Codex capabilities = %#v (want Streaming+Resume, no Permissions)", got.Codex.Capabilities)
	}
	if !got.Pi.Available || got.Pi.Version != "pi 0.1.0" {
		t.Fatalf("Pi descriptor = %#v", got.Pi)
	}
	if !got.Pi.Capabilities.Streaming || !got.Pi.Capabilities.Usage || !got.Pi.Capabilities.Resume || got.Pi.Capabilities.Permissions {
		t.Fatalf("Pi capabilities = %#v (want Streaming+Usage+Resume, no Permissions)", got.Pi.Capabilities)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRegisterAgentKindsPreservesDescriptors(t *testing.T) {
	reg := agent.NewRegistry()
	registerAgentKinds(reg, agentCLIDiscovery{
		ClaudeCode: proto.SupportedAgentKind{
			Kind:      "claude_code",
			Available: true,
			Version:   "claude 2.0.0",
			Capabilities: proto.AgentKindCapabilities{
				Streaming:   true,
				Permissions: true,
				Usage:       true,
				Resume:      true,
			},
		},
		OpenCode: proto.SupportedAgentKind{
			Kind:      "opencode",
			Available: false,
			Version:   "missing",
			Capabilities: proto.AgentKindCapabilities{
				Streaming: true,
				Usage:     true,
			},
		},
		Codex: proto.SupportedAgentKind{
			Kind:      "codex",
			Available: true,
			Version:   "codex 0.141.0",
			Capabilities: proto.AgentKindCapabilities{
				Streaming: true,
				Usage:     true,
				Resume:    true,
			},
		},
		Pi: proto.SupportedAgentKind{
			Kind:      "pi",
			Available: true,
			Version:   "pi 0.1.0",
			Capabilities: proto.AgentKindCapabilities{
				Streaming: true,
				Usage:     true,
				Resume:    true,
			},
		},
	})

	kinds := reg.SupportedAgentKinds()
	if len(kinds) != 4 {
		t.Fatalf("SupportedAgentKinds len = %d, want 4: %#v", len(kinds), kinds)
	}
	// Sorted: claude_code, codex, opencode, pi.
	if kinds[0].Kind != "claude_code" || kinds[1].Kind != "codex" || kinds[2].Kind != "opencode" || kinds[3].Kind != "pi" {
		t.Fatalf("SupportedAgentKinds sort = %#v", kinds)
	}
	if !kinds[0].Available || kinds[0].Version != "claude 2.0.0" || !kinds[0].Capabilities.Permissions {
		t.Fatalf("claude descriptor not preserved: %#v", kinds[0])
	}
	if !kinds[1].Available || kinds[1].Version != "codex 0.141.0" || !kinds[1].Capabilities.Resume {
		t.Fatalf("codex descriptor not preserved: %#v", kinds[1])
	}
	if kinds[2].Available || kinds[2].Version != "missing" || !kinds[2].Capabilities.Streaming || !kinds[2].Capabilities.Usage {
		t.Fatalf("opencode descriptor not preserved: %#v", kinds[2])
	}
	if !kinds[3].Available || kinds[3].Version != "pi 0.1.0" || !kinds[3].Capabilities.Resume || kinds[3].Capabilities.Permissions {
		t.Fatalf("pi descriptor not preserved: %#v", kinds[3])
	}
	if _, err := reg.Resolve("opencode"); err != nil {
		t.Fatalf("opencode factory not registered: %v", err)
	}
	if _, err := reg.Resolve("codex"); err != nil {
		t.Fatalf("codex factory not registered: %v", err)
	}
	if _, err := reg.Resolve("pi"); err != nil {
		t.Fatalf("pi factory not registered: %v", err)
	}
}

func TestRegisterAgentKindsAdvertisesLazyCodex(t *testing.T) {
	t.Setenv("PARSAR_DAEMON_LAZY_CODEX", "true")
	reg := agent.NewRegistry()
	registerAgentKinds(reg, agentCLIDiscovery{
		ClaudeCode: proto.SupportedAgentKind{Kind: "claude_code"},
		OpenCode:   proto.SupportedAgentKind{Kind: "opencode"},
		Codex: proto.SupportedAgentKind{
			Kind: "codex",
			Capabilities: proto.AgentKindCapabilities{
				Streaming: true,
				Usage:     true,
				Resume:    true,
			},
		},
		Pi: proto.SupportedAgentKind{Kind: "pi"},
	})

	for _, kind := range reg.SupportedAgentKinds() {
		if kind.Kind != "codex" {
			continue
		}
		if !kind.Available {
			t.Fatalf("lazy codex should be advertised as available: %#v", kind)
		}
		if kind.Version != "managed; installs on first use" {
			t.Fatalf("lazy codex version = %q", kind.Version)
		}
		return
	}
	t.Fatal("codex descriptor not registered")
}
