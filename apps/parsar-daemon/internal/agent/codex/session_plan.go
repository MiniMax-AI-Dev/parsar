package codex

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func prepareSessionPlan(ctx context.Context, req proto.PromptRequestPayload, cfg sessionConfig) (SessionPlan, string, error) {
	profile, err := managedPermissionProfile(req, cfg)
	if err != nil {
		return SessionPlan{}, "", err
	}
	if req.WorkspaceReadOnly {
		plan, err := workspaceReadPlan(req)
		return plan, "", err
	}
	mcpServers, err := publicMCPHTTPServers(req)
	if err != nil {
		return SessionPlan{}, "", err
	}
	plan, err := BuildSessionPlan(req.RunID, req.AgentStateKey, req.WorkDir, req.AgentOptions)
	if err != nil {
		return SessionPlan{}, "", fmt.Errorf("codex: build session plan: %w", err)
	}
	if profile != "" {
		plan.Sandbox = ""
		plan.Permissions = profile
		plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"default_permissions", tomlQuoteString(profile)})
		configureRestrictedShellEnvironment(&plan)
		// Native login-shell snapshots live outside the managed tool filesystem.
		plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"features.shell_snapshot", "false"})
	}

	if req.LocalEnvironment != nil && req.LocalEnvironment.ToolEnvironment {
		if err := localworkspace.VerifyToolEnvironment(); err != nil {
			plan.Cleanup()
			return SessionPlan{}, "", err
		}
		plan.Env = append(plan.Env, "PARSAR_RUNTIME_TOOL_ENV=1")
		plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"features.hooks", "true"})
	}

	if req.DisableSubagents {
		disableSubagents(&plan)
	}
	mcpBearerEnv := prepareMCPHTTPBearer(mcpServers, req.MCPHTTPServers)
	if mcpServers != nil {
		if err := configureMCPHTTP(&plan, mcpServers); err != nil {
			plan.Cleanup()
			return SessionPlan{}, "", err
		}
	}

	if stringOpt(req.AgentOptions, "model_verbosity") != "" {
		if err := prepareModelVerbosity(ctx, cfg.codexBinary, &plan); err != nil {
			plan.Cleanup()
			return SessionPlan{}, "", err
		}
	}

	if req.DisableExecutionEnvironment {
		plan.Env = append(plan.Env, "CODEX_EXEC_SERVER_URL=none")
	}
	skillRoot := ""
	if !req.DisableExecutionEnvironment && req.RemoteEnvironment == nil {
		skillRoot, err = prepareManagedSkills(ctx, cfg.logger, req)
	}
	if err != nil {
		plan.Cleanup()
		return SessionPlan{}, "", err
	}

	if req.RemoteEnvironment != nil {
		configureRemoteEnvironment(&plan, *req.RemoteEnvironment)
	}
	plan.Env = append(plan.Env, mcpBearerEnv...)
	return plan, skillRoot, nil
}
