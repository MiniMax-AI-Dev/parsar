package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// SupportsRemoteEnvironment admits the native protocol verified by the placement
// and daemon probes. Each binding still needs a native connection/readiness check.
func SupportsRemoteEnvironment(version string) bool {
	return strings.TrimSpace(version) == "codex-cli 0.153.4"
}

// EnvironmentSelection mirrors the native app-server selection. Resume does not
// retain this selection; send it with every turn/start, including cold resumes.
type EnvironmentSelection struct {
	EnvironmentID         string   `json:"environmentId"`
	Cwd                   string   `json:"cwd"`
	RuntimeWorkspaceRoots []string `json:"runtimeWorkspaceRoots"`
}

func configureRemoteEnvironment(plan *SessionPlan, environment proto.RemoteEnvironment) {
	plan.Env = append(plan.Env,
		"CODEX_EXEC_SERVER_NOISE_REGISTRY_URL="+strings.TrimRight(environment.ConnectionURL, "/"),
		"CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID="+environment.ID,
		"CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN="+environment.ConnectionToken)
	plan.Environments = []EnvironmentSelection{{
		EnvironmentID: "remote", Cwd: environment.WorkspaceDirectory,
		RuntimeWorkspaceRoots: []string{environment.WorkspaceDirectory},
	}}
	plan.ExtraConfig = append(plan.ExtraConfig,
		[2]string{"shell_environment_policy.inherit", `"core"`},
		[2]string{"shell_environment_policy.ignore_default_excludes", "false"})
}

func verifyRemoteEnvironment(parent context.Context, rpc *JSONRPCClient) error {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	status, err := nativeEnvironmentStatus(ctx, rpc, "local")
	if err != nil || status != "unknown" {
		return errors.New("codex: cannot confirm absence of local execution fallback")
	}
	// environment/info establishes the native encrypted connection. Status alone
	// observes a lazy pending environment without connecting or recovering it.
	raw, err := rpc.Request(ctx, "environment/info", map[string]string{"environmentId": "remote"})
	if err != nil {
		return errors.New("codex: remote environment connection failed")
	}
	var info struct {
		Shell struct {
			Path string `json:"path"`
		} `json:"shell"`
	}
	if json.Unmarshal(raw, &info) != nil || info.Shell.Path == "" {
		return errors.New("codex: invalid remote environment information")
	}
	status, err = nativeEnvironmentStatus(ctx, rpc, "remote")
	if err != nil || status != "ready" {
		return errors.New("codex: remote environment is not ready")
	}
	return nil
}
