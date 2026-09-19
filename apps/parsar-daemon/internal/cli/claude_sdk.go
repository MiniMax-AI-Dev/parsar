package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudesdk"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

const claudeSDKEntrypointEnv = "PARSAR_CLAUDE_SDK_ENTRYPOINT"
const claudeSDKNodeEnv = "PARSAR_CLAUDE_SDK_NODE"

type claudeSDKDiscovery struct {
	Info   proto.SupportedAgentKind
	Config claudesdk.Config
}

func discoverClaudeSDK(rc *runContext, profile string, check func(context.Context, claudesdk.Config) (claudesdk.RuntimeInfo, error)) *claudeSDKDiscovery {
	entrypoint := os.Getenv(claudeSDKEntrypointEnv)
	if entrypoint == "" {
		return nil
	}
	out := &claudeSDKDiscovery{Info: proto.SupportedAgentKind{Kind: "claude_sdk", Capabilities: proto.AgentKindCapabilities{
		Streaming: true, Usage: true, Resume: true, Steering: true, MessageItems: true,
		ToolObservations: true, EnvironmentNone: true, SubagentControl: true,
		DurableTurns: true, DurableInputReceipts: true, FunctionTools: true, ExecutionControls: true,
	}}}
	fail := func(err error) *claudeSDKDiscovery {
		fmt.Fprintf(rc.stderr, "parsar-daemon: configured Claude SDK runtime unavailable: %v\n", err)
		return out
	}
	if !filepath.IsAbs(entrypoint) {
		return fail(fmt.Errorf("%s must be absolute", claudeSDKEntrypointEnv))
	}
	profileDir, err := paths.ProfileDir(profile)
	if err != nil {
		return fail(err)
	}
	if !filepath.IsAbs(profileDir) {
		return fail(fmt.Errorf("Claude SDK state requires an absolute PARSAR_HOME"))
	}
	node := os.Getenv(claudeSDKNodeEnv)
	if node == "" {
		node = "node"
	}
	node, err = exec.LookPath(node)
	if err != nil {
		return fail(fmt.Errorf("Claude SDK Node executable is unavailable"))
	}
	node, err = filepath.Abs(node)
	if err != nil {
		return fail(err)
	}
	out.Config = claudesdk.Config{Node: node, Entrypoint: entrypoint, StateDir: filepath.Join(profileDir, "runtime", "claude-sdk")}
	if mode := os.Getenv("PARSAR_CLAUDE_SDK_WORKSPACE"); mode != "" {
		if mode != "managed" {
			return fail(fmt.Errorf("unsupported Claude SDK workspace profile"))
		}
		binding, err := localworkspace.Load()
		if err != nil || binding == nil {
			return fail(fmt.Errorf("Claude SDK workspace requires a dedicated local Runtime binding"))
		}
		root, err := paths.Root()
		if err != nil {
			return fail(err)
		}
		out.Config.Node, err = filepath.EvalSymlinks(node)
		if err != nil {
			return fail(err)
		}
		out.Config, err = claudesdk.ConfigureLocal(out.Config, root, os.Getenv("PARSAR_RUNTIME_WORKSPACE"), binding.NetworkAccess(), os.Getenv("PARSAR_RUNTIME_STAGING"))
		if err != nil {
			return fail(err)
		}
	}
	if check == nil {
		check = claudesdk.CheckRuntime
	}
	info, err := check(context.Background(), out.Config)
	if err != nil {
		return fail(err)
	}
	if out.Config.Workspace != nil {
		if !info.SupportsLocalRuntime() {
			return fail(fmt.Errorf("Claude SDK bundle does not support the local Runtime contract"))
		}
		caps := &out.Info.Capabilities
		caps.EnvironmentNone, caps.FunctionTools = false, false
		caps.Preparation, caps.LocalEnvironment, caps.LocalEnvironmentNetworkPolicy = true, true, true
		caps.WorkspaceReadPreparation, caps.NativeSessionRecovery = true, true
	}
	out.Info.Available, out.Info.Version = true, info.SDK
	out.Info.Capabilities.MCPHTTPTools = info.SupportsHTTPMCP()
	out.Info.Capabilities.MCPHTTPBearerAuth = info.SupportsHTTPMCPBearer()
	if out.Config.Workspace != nil {
		out.Info.Capabilities.MCPHTTPTools, out.Info.Capabilities.MCPHTTPBearerAuth = false, false
	}
	fmt.Fprintf(rc.stdout, "Claude SDK preflight ok (SDK %s, %s)\n", info.SDK, info.Native)
	return out
}

func registerClaudeSDK(registry *agent.Registry, discovery *claudeSDKDiscovery) {
	if discovery == nil {
		return
	}
	factory := claudesdk.NewFactory(discovery.Config)
	if !discovery.Info.Available {
		factory = func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
			return nil, fmt.Errorf("claude_sdk: configured runtime is unavailable")
		}
	}
	registry.RegisterKind(discovery.Info, factory)
	if discovery.Info.Available && discovery.Info.Capabilities.LocalEnvironment {
		registry.RegisterPreparation("claude_sdk", true, claudesdk.NewPreparationFactory(discovery.Config))
	}
}
