package claudesdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/clirunner"
)

// RuntimeInfo describes a successful local probe, not provider authentication or
// execution capability. Versions are checked against the installed pinned manifest.
type RuntimeInfo struct {
	Type     string   `json:"type"`
	Protocol int      `json:"protocol"`
	Node     string   `json:"node"`
	SDK      string   `json:"sdk"`
	MCP      string   `json:"mcp"`
	Native   string   `json:"native"`
	Features []string `json:"features"`
}

func (info RuntimeInfo) SupportsHTTPMCP() bool {
	return slices.Contains(info.Features, "mcp_http_tools")
}

func (info RuntimeInfo) SupportsHTTPMCPBearer() bool {
	return info.SupportsHTTPMCP() && slices.Contains(info.Features, "mcp_http_bearer_auth")
}

func (info RuntimeInfo) supportsWorkspace() bool {
	return slices.Contains(info.Features, "workspace_tools")
}

func (info RuntimeInfo) supportsWorkspacePreparation() bool {
	return info.supportsWorkspace() && slices.Contains(info.Features, "workspace_prepare")
}

func (info RuntimeInfo) supportsWorkspaceCommands() bool {
	return info.supportsWorkspacePreparation() && slices.Contains(info.Features, "workspace_command_observations")
}

func (info RuntimeInfo) SupportsLocalRuntime() bool {
	return info.supportsWorkspaceCommands() && slices.Contains(info.Features, "local_runtime_v1")
}

func (info RuntimeInfo) SupportsWorkspaceFunctions() bool {
	return info.SupportsLocalRuntime() && slices.Contains(info.Features, "workspace_functions")
}

// CheckRuntime checks the packaged companion and exact execution entrypoint.
// It does not create Session state, register an engine or make a model request.
func CheckRuntime(ctx context.Context, config Config) (RuntimeInfo, error) {
	if !filepath.IsAbs(config.Entrypoint) {
		return RuntimeInfo{}, fmt.Errorf("claudesdk: SDK entrypoint must be absolute")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	binary := config.Node
	if binary == "" {
		binary = "node"
	}
	env := append(append([]string{}, os.Environ()...), config.Env...)
	if config.Workspace != nil {
		var err error
		_, env, err = workspaceEnvironment(config)
		if err != nil {
			return RuntimeInfo{}, err
		}
	}
	process, err := clirunner.Start(clirunner.StartOptions{
		Parent: ctx, Binary: binary,
		Args:            []string{filepath.Join(filepath.Dir(config.Entrypoint), "runtime_check.js"), config.Entrypoint},
		Dir:             filepath.Dir(config.Entrypoint),
		Env:             env,
		OwnProcessGroup: true, KillTimeout: 250 * time.Millisecond,
	})
	if err != nil {
		return RuntimeInfo{}, fmt.Errorf("claudesdk: cannot start runtime check: %w", err)
	}
	defer process.Cancel()
	stderrDone := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, process.Stderr); close(stderrDone) }()
	const maxReport = 16 * 1024
	raw, readErr := io.ReadAll(io.LimitReader(process.Stdout, maxReport+1))
	if readErr != nil || len(raw) > maxReport {
		process.Cancel()
	}
	_, _ = io.Copy(io.Discard, process.Stdout)
	<-stderrDone
	waitErr := process.Wait()
	if ctx.Err() != nil {
		return RuntimeInfo{}, fmt.Errorf("claudesdk: runtime check: %w", ctx.Err())
	}
	if readErr != nil || len(raw) > maxReport || waitErr != nil {
		return RuntimeInfo{}, fmt.Errorf("claudesdk: runtime check failed")
	}
	var info RuntimeInfo
	if json.Unmarshal(raw, &info) != nil || info.Type != "runtime_ready" || info.Protocol != 1 ||
		info.Node == "" || info.SDK == "" || info.MCP == "" || info.Native == "" {
		return RuntimeInfo{}, fmt.Errorf("claudesdk: invalid runtime readiness report")
	}
	return info, nil
}
