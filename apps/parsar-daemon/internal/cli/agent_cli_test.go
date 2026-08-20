package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/codex"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/deepseekharness"
	opencodeagent "github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/opencode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/pi"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// missingChecks stubs every engine as absent. Tests override the engines
// they care about — leaving a field nil would probe the host's real CLI and
// make the outcome depend on the developer's machine.
func missingChecks() agentCLIChecks {
	return agentCLIChecks{
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
		DeepseekHarness: func(context.Context, string) (string, error) {
			return "", deepseekharness.ErrCLINotFound
		},
	}
}

func TestDiscoverAgentCLIsAllowsOpenCodeWithoutClaude(t *testing.T) {
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	rc := &runContext{stdout: stdout, stderr: stderr}
	checks := missingChecks()
	checks.OpenCode = func(context.Context, string) (string, error) {
		return "opencode 1.4.3", nil
	}
	got, err := discoverAgentCLIs(rc, checks)
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
	if got.DeepseekHarness.Available {
		t.Fatalf("DeepseekHarness.Available = true, want false: %#v", got.DeepseekHarness)
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
	if !strings.Contains(stderr.String(), "deepseek_harness unavailable") {
		t.Fatalf("stderr missing DeepSeek Harness unavailable line: %q", stderr.String())
	}
}

func TestDiscoverAgentCLIsAllowsDeepseekHarnessAlone(t *testing.T) {
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	rc := &runContext{stdout: stdout, stderr: stderr}
	checks := missingChecks()
	checks.DeepseekHarness = func(context.Context, string) (string, error) {
		return "dsh 0.1.0", nil
	}
	got, err := discoverAgentCLIs(rc, checks)
	if err != nil {
		t.Fatalf("discoverAgentCLIs: %v", err)
	}
	if !got.DeepseekHarness.Available || got.DeepseekHarness.Version != "dsh 0.1.0" {
		t.Fatalf("DeepseekHarness descriptor = %#v", got.DeepseekHarness)
	}
	// The headless surface streams nothing, reports no tokens, and has no
	// resume flag or approval channel — advertising any of them would make
	// the server wait for frames that never arrive.
	if got.DeepseekHarness.Capabilities != (proto.AgentKindCapabilities{}) {
		t.Fatalf("DeepseekHarness capabilities = %#v, want none", got.DeepseekHarness.Capabilities)
	}
	if !strings.Contains(stdout.String(), "DeepSeek Harness preflight ok") {
		t.Fatalf("stdout missing DeepSeek Harness ok line: %q", stdout.String())
	}
}

func TestDiscoverAgentCLIsAllMissingFails(t *testing.T) {
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	rc := &runContext{stdout: stdout, stderr: stderr}
	got, err := discoverAgentCLIs(rc, missingChecks())
	if err == nil {
		t.Fatalf("expected error when all CLIs missing, got descriptors %#v", got)
	}
	if !strings.Contains(err.Error(), "no supported agent CLI") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ClaudeCode.Available || got.OpenCode.Available || got.Codex.Available || got.Pi.Available || got.DeepseekHarness.Available {
		t.Fatalf("available descriptors after missing CLIs: %#v", got)
	}
}

func TestDiscoverAgentCLIsAllAvailable(t *testing.T) {
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
		DeepseekHarness: func(context.Context, string) (string, error) {
			return "dsh 0.1.0", nil
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
	if !got.Codex.Capabilities.Streaming || !got.Codex.Capabilities.Permissions || !got.Codex.Capabilities.Resume {
		t.Fatalf("Codex capabilities = %#v (want Streaming+Permissions+Resume)", got.Codex.Capabilities)
	}
	if !got.Pi.Available || got.Pi.Version != "pi 0.1.0" {
		t.Fatalf("Pi descriptor = %#v", got.Pi)
	}
	if !got.Pi.Capabilities.Streaming || !got.Pi.Capabilities.Usage || !got.Pi.Capabilities.Resume || got.Pi.Capabilities.Permissions {
		t.Fatalf("Pi capabilities = %#v (want Streaming+Usage+Resume, no Permissions)", got.Pi.Capabilities)
	}
	if !got.DeepseekHarness.Available || got.DeepseekHarness.Version != "dsh 0.1.0" {
		t.Fatalf("DeepseekHarness descriptor = %#v", got.DeepseekHarness)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestDiscoverAgentCLIsReportsBrokenCLIWithVersionCommand(t *testing.T) {
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	rc := &runContext{stdout: stdout, stderr: stderr}
	checks := missingChecks()
	checks.DeepseekHarness = func(context.Context, string) (string, error) {
		return "", context.DeadlineExceeded
	}
	if _, err := discoverAgentCLIs(rc, checks); err == nil {
		t.Fatal("expected error when every CLI is unusable")
	}
	if !strings.Contains(stderr.String(), "`dsh --version` failed") {
		t.Fatalf("stderr missing broken-CLI line: %q", stderr.String())
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
				Streaming:   true,
				Permissions: true,
				Usage:       true,
				Resume:      true,
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
		DeepseekHarness: proto.SupportedAgentKind{
			Kind:      "deepseek_harness",
			Available: true,
			Version:   "dsh 0.1.0",
		},
	})

	kinds := reg.SupportedAgentKinds()
	if len(kinds) != 5 {
		t.Fatalf("SupportedAgentKinds len = %d, want 5: %#v", len(kinds), kinds)
	}
	// Sorted: claude_code, codex, deepseek_harness, opencode, pi.
	want := []string{"claude_code", "codex", "deepseek_harness", "opencode", "pi"}
	for i, kind := range want {
		if kinds[i].Kind != kind {
			t.Fatalf("SupportedAgentKinds sort = %#v", kinds)
		}
	}
	if !kinds[0].Available || kinds[0].Version != "claude 2.0.0" || !kinds[0].Capabilities.Permissions {
		t.Fatalf("claude descriptor not preserved: %#v", kinds[0])
	}
	if !kinds[1].Available || kinds[1].Version != "codex 0.141.0" || !kinds[1].Capabilities.Resume {
		t.Fatalf("codex descriptor not preserved: %#v", kinds[1])
	}
	if !kinds[2].Available || kinds[2].Version != "dsh 0.1.0" || kinds[2].Capabilities.Resume {
		t.Fatalf("deepseek_harness descriptor not preserved: %#v", kinds[2])
	}
	if kinds[3].Available || kinds[3].Version != "missing" || !kinds[3].Capabilities.Streaming || !kinds[3].Capabilities.Usage {
		t.Fatalf("opencode descriptor not preserved: %#v", kinds[3])
	}
	if !kinds[4].Available || kinds[4].Version != "pi 0.1.0" || !kinds[4].Capabilities.Resume || kinds[4].Capabilities.Permissions {
		t.Fatalf("pi descriptor not preserved: %#v", kinds[4])
	}
	for _, kind := range want {
		if _, err := reg.Resolve(kind); err != nil {
			t.Fatalf("%s factory not registered: %v", kind, err)
		}
	}
}

func TestDeepseekHarnessCapabilitiesFollowTheRunLocation(t *testing.T) {
	// dsh's two automation surfaces are not equivalent, and the adapter can
	// only use the resident-server one inside a sandbox. The descriptor has
	// to say so, because the server keys its conversation-history injection
	// off Resume: advertising Resume=true on the headless surface would
	// silently drop continuity.
	t.Setenv("IS_SANDBOX", "")
	local := deepseekHarnessCapabilities()
	if local != (proto.AgentKindCapabilities{}) {
		t.Errorf("local device capabilities = %+v, want none", local)
	}

	t.Setenv("IS_SANDBOX", "1")
	sandbox := deepseekHarnessCapabilities()
	if !sandbox.Streaming || !sandbox.Usage || !sandbox.Resume {
		t.Errorf("sandbox capabilities = %+v, want streaming, usage and resume", sandbox)
	}
	// The generated profile pins the unattended preset, so dsh rejects
	// escalation itself instead of asking; there is no approver on this
	// path and claiming otherwise would surface dead permission cards.
	if sandbox.Permissions {
		t.Error("the resident-server path has no approver and must not claim permissions")
	}

	if got := agentCLIDescriptors().DeepseekHarness.Capabilities; got != sandbox {
		t.Errorf("descriptor table does not use the run-location capabilities: %+v", got)
	}
}
