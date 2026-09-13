package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudesdk"
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
	if check == nil {
		check = claudesdk.CheckRuntime
	}
	info, err := check(context.Background(), out.Config)
	if err != nil {
		return fail(err)
	}
	out.Info.Available, out.Info.Version = true, info.SDK
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
}
