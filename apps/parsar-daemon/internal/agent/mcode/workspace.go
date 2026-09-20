package mcode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// WorkspaceConfig is frozen deployment input, separate from public Agent options.
type WorkspaceConfig struct {
	Binary, Node, Bridge, Directory, Network, Scratch string
	ProtectedDirs                                     []string
}

func ConfigureLocal(binary, node, bridge, root, workspace, network, staging string) (WorkspaceConfig, error) {
	c := WorkspaceConfig{Binary: binary, Node: node, Bridge: bridge, Directory: workspace, Network: network,
		Scratch:       filepath.Join(root, "runtime", "mcode-tools", "scratch"),
		ProtectedDirs: []string{filepath.Join(root, "parsar-daemon"), filepath.Join(root, "runtime", "mcode"), filepath.Dir(workspace), staging}}
	if network != "enabled" && network != "disabled" {
		return c, fmt.Errorf("mcode: explicit workspace network policy is required")
	}
	for _, path := range []string{binary, node, bridge, root, workspace, staging} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
			return c, fmt.Errorf("mcode: canonical absolute deployment paths are required")
		}
	}
	for _, path := range []string{binary, node, bridge} {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || resolved != path || strings.HasPrefix(path, workspace+"/") || strings.HasPrefix(path, "/workspace/") {
			return c, fmt.Errorf("mcode: runtime programs must be canonical paths outside the workspace")
		}
	}
	bound, err := os.Stat(workspace)
	public, publicErr := os.Stat("/workspace")
	if err != nil || publicErr != nil || !bound.IsDir() || !os.SameFile(bound, public) {
		return c, fmt.Errorf("mcode: public workspace alias does not match the Runtime binding")
	}
	if err := os.MkdirAll(c.Scratch, 0700); err != nil {
		return c, err
	}
	return c, nil
}

func prepareWorkspaceOptions(ctx context.Context, c WorkspaceConfig, req proto.PromptRequestPayload) (launchOptions, error) {
	if !req.StrictResume || req.LocalEnvironment == nil || req.WorkDir != c.Directory || req.DisableExecutionEnvironment || req.LocalEnvironment.NetworkAccess != c.Network || req.RemoteEnvironment != nil || req.WorkspaceReadOnly {
		return launchOptions{}, fmt.Errorf("mcode: execution does not match the dedicated workspace")
	}
	// Reuse public option validation and private Session state provisioning. Native
	// cwd remains private; only the internal MCP worker receives the public workspace.
	private := req
	private.LocalEnvironment, private.WorkDir, private.DisableExecutionEnvironment = nil, "", true
	opts, err := prepareOptionsWithSkills(ctx, private, false)
	if err != nil {
		return opts, err
	}
	if err := localworkspace.VerifySkills(req.LocalEnvironment.Skills); err != nil {
		return opts, err
	}
	if len(req.LocalEnvironment.Skills) > 0 {
		link := filepath.Join(opts.DataDir, "skills")
		if target, err := os.Readlink(link); err == nil {
			if target != localworkspace.SkillDirectory {
				return opts, fmt.Errorf("mcode: unexpected native Skill root")
			}
		} else if !os.IsNotExist(err) {
			return opts, err
		} else if err := os.Symlink(localworkspace.SkillDirectory, link); err != nil {
			return opts, err
		}
	}
	raw, err := os.ReadFile(filepath.Join(opts.DataDir, "config.yaml"))
	if err != nil {
		return opts, err
	}
	var config map[string]any
	if err = json.Unmarshal(raw, &config); err != nil {
		return opts, err
	}
	config["permissionMode"] = "bypassPermissions"
	config["sandbox"] = map[string]bool{"enabled": false}
	raw, err = json.Marshal(config)
	if err != nil {
		return opts, err
	}
	if err = os.WriteFile(filepath.Join(opts.DataDir, "config.yaml"), raw, 0600); err != nil {
		return opts, err
	}
	profile := map[string]any{"workspace": "/workspace", "scratch": c.Scratch, "network": c.Network, "protectedDirs": slices.Clone(c.ProtectedDirs), "skills": len(req.LocalEnvironment.Skills) > 0}
	if req.LocalEnvironment.ToolEnvironment {
		if err := localworkspace.VerifyToolEnvironment(); err != nil {
			return opts, err
		}
		profile["toolEnvironment"] = true
		// Initialization exposes only user env/packages; private staging and
		// daemon/native history remain explicitly denied.
		protected := slices.Clone(c.ProtectedDirs)
		profile["protectedDirs"] = slices.DeleteFunc(protected, func(path string) bool { return path == "/environment" })
	}
	raw, err = json.Marshal(profile)
	if err != nil {
		return opts, err
	}
	path := filepath.Join(opts.DataDir, "workspace-profile.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		return opts, err
	}
	opts.MCP = []map[string]any{{"name": "parsar_workspace", "command": c.Node, "args": []string{c.Bridge, path}, "env": []map[string]string{}}}
	return opts, nil
}
