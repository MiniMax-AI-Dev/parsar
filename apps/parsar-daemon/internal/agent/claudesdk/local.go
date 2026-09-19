package claudesdk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConfigureLocal selects the qualified, dedicated Runtime layout. The shared
// localworkspace binding still authorizes every request against its Session.
func ConfigureLocal(config Config, root, workspace, network, staging string) (Config, error) {
	config.StateDir = filepath.Join(root, "runtime", "claude-sdk", "history")
	config.Workspace = &WorkspaceConfig{
		Directory: workspace, PublicDirectory: "/workspace", NetworkAccess: network,
		HomeDir:        filepath.Join(root, "runtime", "claude-sdk", "home"),
		ScratchDir:     filepath.Join(root, "runtime", "claude-sdk", "scratch"),
		ProtectedDirs:  []string{filepath.Join(root, "parsar-daemon"), staging},
		DependencyPath: "/usr/local/bin:/usr/bin:/bin",
	}
	if network != "enabled" && network != "disabled" {
		return Config{}, fmt.Errorf("claudesdk: dedicated Runtime requires an explicit network policy")
	}
	for _, dir := range []string{config.StateDir, config.Workspace.HomeDir, config.Workspace.ScratchDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return Config{}, err
		}
	}
	config.Env = nil
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if workspaceEnvName(name) {
			config.Env = append(config.Env, entry)
		}
	}
	_, _, err := workspaceEnvironment(config)
	return config, err
}
