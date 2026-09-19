package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudesdk"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/authoring"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func unavailableCLIChecks() agentCLIChecks {
	missing := func(context.Context, string) (string, error) { return "", errors.New("unavailable test CLI") }
	return agentCLIChecks{ClaudeCode: missing, OpenCode: missing, Codex: missing, Pi: missing, MCode: missing}
}

func TestClaudeSDKDiscoveryAndRegistration(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		configured, ready, legacy bool
	}{
		{"unconfigured", false, false, true}, {"SDK-only", true, true, false},
		{"failed-with-legacy", true, false, true}, {"none-ready", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PARSAR_HOME", root)
			entrypoint := ""
			if tc.configured {
				entrypoint = filepath.Join(root, "replaceable bundle", "dist", "main.js")
			}
			t.Setenv(claudeSDKEntrypointEnv, entrypoint)
			node, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv(claudeSDKNodeEnv, node)
			calls := 0
			checks := unavailableCLIChecks()
			checks.ClaudeSDK = func(_ context.Context, config claudesdk.Config) (claudesdk.RuntimeInfo, error) {
				calls++
				if config.Node != node || config.Entrypoint != entrypoint || config.StateDir != filepath.Join(root, "parsar-daemon", "test", "runtime", "claude-sdk") || config.Env != nil {
					t.Fatalf("readiness configuration differs from operator configuration: %+v", config)
				}
				if !tc.ready {
					return claudesdk.RuntimeInfo{}, errors.New("controlled readiness failure")
				}
				return claudesdk.RuntimeInfo{SDK: "test-sdk", Native: "test-native"}, nil
			}
			if tc.legacy {
				checks.Pi = func(context.Context, string) (string, error) { return "test-pi", nil }
			}
			stdout, stderr := &strings.Builder{}, &strings.Builder{}
			discovery, err := discoverAgentCLIs(&runContext{stdout: stdout, stderr: stderr}, "test", checks)
			if (err == nil) != (tc.ready || tc.legacy) {
				t.Fatalf("startup readiness: %v", err)
			}
			if (calls == 1) != tc.configured {
				t.Fatalf("SDK readiness calls: %d", calls)
			}
			reg := agent.NewRegistry()
			registerAgentKinds(reg, discovery, "https://product.invalid")
			reg = authoringRegistry(reg, authoring.New(nil))
			factory, err := reg.Resolve("claude_sdk")
			if !tc.configured {
				if discovery.ClaudeSDK != nil || err == nil {
					t.Fatal("unconfigured SDK was registered")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			info := discovery.ClaudeSDK.Info
			if info.Available != tc.ready {
				t.Fatal(info)
			}
			for _, registered := range reg.SupportedAgentKinds() {
				if registered.Kind == "claude_sdk" && registered != info {
					t.Fatalf("SDK descriptor changed: %+v", registered)
				}
				if registered.Kind != "claude_sdk" && !registered.Capabilities.WorkspaceAuthoring {
					t.Fatalf("product authoring lost: %+v", registered)
				}
			}
			caps := info.Capabilities
			if caps.WorkspaceAuthoring || caps.Permissions || caps.ToolItems || caps.WebSearchControl || caps.TextVerbosity || !caps.DurableTurns || !caps.DurableInputReceipts || !caps.FunctionTools || !caps.EnvironmentNone {
				t.Fatalf("incorrect SDK capability scope: %+v", caps)
			}
			// Even a ready SDK must not acquire product write access through the wrapper.
			_, err = factory(t.Context(), proto.PromptRequestPayload{RunID: "sdk", Prompt: "hello", WorkspaceAuthoring: true}, make(chan proto.Envelope, 1))
			if err == nil || (!tc.ready && !strings.Contains(err.Error(), "runtime is unavailable")) {
				t.Fatalf("SDK request did not fail closed: %v", err)
			}
		})
	}
}

func TestClaudeSDKInvalidPathsFailBeforeProbe(t *testing.T) {
	for _, relative := range []string{"entrypoint", "home"} {
		t.Run(relative, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PARSAR_HOME", root)
			t.Setenv(claudeSDKEntrypointEnv, filepath.Join(root, "main.js"))
			if relative == "entrypoint" {
				t.Setenv(claudeSDKEntrypointEnv, "main.js")
			} else {
				t.Setenv("PARSAR_HOME", "relative-home")
			}
			out := discoverClaudeSDK(&runContext{stdout: &strings.Builder{}, stderr: &strings.Builder{}}, "default", func(context.Context, claudesdk.Config) (claudesdk.RuntimeInfo, error) {
				t.Fatal("invalid paths reached runtime probe")
				return claudesdk.RuntimeInfo{}, nil
			})
			if out == nil || out.Info.Available {
				t.Fatal("invalid runtime advertised as ready")
			}
		})
	}
}

func TestClaudeSDKMCPFeatureDiscovery(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PARSAR_HOME", root)
	t.Setenv(claudeSDKEntrypointEnv, filepath.Join(root, "main.js"))
	node, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(claudeSDKNodeEnv, node)
	for _, features := range [][]string{nil, {"mcp_http_tools"}, {"mcp_http_bearer_auth"}, {"mcp_http_tools", "mcp_http_bearer_auth"}, {"mcp_http_required"}, {"mcp_http_tools", "mcp_http_required"}} {
		out := discoverClaudeSDK(&runContext{stdout: &strings.Builder{}, stderr: &strings.Builder{}}, "default", func(context.Context, claudesdk.Config) (claudesdk.RuntimeInfo, error) {
			info := claudesdk.RuntimeInfo{SDK: "0.3.269", Native: "2.1.269 (Claude Code)", Features: features}
			return info, nil
		})
		supported := len(features) > 0 && features[0] == "mcp_http_tools"
		if out == nil || !out.Info.Available || out.Info.Capabilities.MCPHTTPTools != supported || out.Info.Capabilities.MCPHTTPBearerAuth != (supported && slices.Contains(features, "mcp_http_bearer_auth")) || out.Info.Capabilities.MCPHTTPRequired != (supported && slices.Contains(features, "mcp_http_required")) {
			t.Fatal("MCP feature discovery widened the runtime profile")
		}
	}
}
