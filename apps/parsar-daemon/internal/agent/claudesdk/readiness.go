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
	process, err := clirunner.Start(clirunner.StartOptions{
		Parent: ctx, Binary: binary,
		Args:            []string{filepath.Join(filepath.Dir(config.Entrypoint), "runtime_check.js"), config.Entrypoint},
		Dir:             filepath.Dir(config.Entrypoint),
		Env:             append(append([]string{}, os.Environ()...), config.Env...),
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
