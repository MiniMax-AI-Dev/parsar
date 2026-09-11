package cli

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/mcode"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func discoverMCode(rc *runContext, check func(context.Context, string) (string, error)) proto.SupportedAgentKind {
	if check == nil {
		check = mcode.CheckCLIAvailable
	}
	result := proto.SupportedAgentKind{Kind: "mcode", Capabilities: proto.AgentKindCapabilities{Streaming: true, Permissions: true, Resume: true}}
	ctx, cancel := context.WithTimeout(context.Background(), cliVersionTimeout)
	defer cancel()
	version, err := check(ctx, "")
	if err != nil {
		fmt.Fprintf(rc.stderr, "parsar-daemon: mcode unavailable: %v\n  Install: npm install -g @minimax-ai/code@0.3.11\n", err)
		return result
	}
	result.Available, result.Version = true, version
	fmt.Fprintf(rc.stdout, "mcode preflight ok (%s)\n", version)
	return result
}
