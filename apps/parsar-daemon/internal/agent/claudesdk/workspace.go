package claudesdk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentskill"
)

// WorkspaceConfig binds one trusted private placement. It does not create an
// isolation boundary or authorize a public Environment. The operator must place
// the entire factory inside the qualified outer mount/process boundary first.
// State directories must already exist, be canonical and be mutually disjoint.
// PublicDirectory may name a second mount of the same workspace inode.
type WorkspaceConfig struct {
	Directory       string
	PublicDirectory string
	NetworkAccess   string
	HomeDir         string
	ScratchDir      string
	ProtectedDirs   []string
	DependencyPath  string
}

type workspaceProfile struct {
	Skills          []agentskill.Metadata `json:"skills,omitempty"`
	ToolEnvironment bool                  `json:"tool_environment,omitempty"`
	Home            string                `json:"home"`
	State           string                `json:"state"`
	Scratch         string                `json:"scratch"`
	ProtectedDirs   []string              `json:"protected_dirs"`
	DependencyPath  string                `json:"dependency_path"`
	EnvNames        []string              `json:"env_names"`
	NetworkAccess   string                `json:"network_access,omitempty"`
}

func prepareWorkspace(config Config, req proto.PromptRequestPayload) (*workspaceProfile, []string, error) {
	if req.DisableExecutionEnvironment || req.RemoteEnvironment != nil || req.MCPHTTPServers != nil {
		return nil, nil, fmt.Errorf("claudesdk: workspace profile does not support the requested execution combination")
	}
	if req.WorkDir != "" && req.WorkDir != config.Workspace.Directory {
		return nil, nil, fmt.Errorf("claudesdk: work_dir conflicts with the trusted workspace binding")
	}
	if req.LocalEnvironment != nil && (config.Workspace.NetworkAccess == "" || req.LocalEnvironment.NetworkAccess != config.Workspace.NetworkAccess) {
		return nil, nil, fmt.Errorf("claudesdk: local Runtime network policy mismatch")
	}
	profile, env, err := workspaceEnvironment(config)
	if err != nil {
		return nil, nil, err
	}
	if req.LocalEnvironment != nil && req.LocalEnvironment.ToolEnvironment {
		if err := localworkspace.VerifyToolEnvironment(); err != nil {
			return nil, nil, err
		}
		profile.ToolEnvironment = true
	}
	if req.LocalEnvironment != nil {
		if err := localworkspace.VerifySkills(req.LocalEnvironment.Skills); err != nil {
			return nil, nil, err
		}
		profile.Skills = req.LocalEnvironment.Skills
	}
	return profile, env, nil
}

func workspaceCwd(w *WorkspaceConfig) string {
	if w.PublicDirectory != "" {
		return w.PublicDirectory
	}
	return w.Directory
}

// Workspace Env is a replacement, unlike the existing none profile's overlay.
// Only explicitly selected provider settings reach either readiness or execution.
func workspaceEnvironment(config Config) (*workspaceProfile, []string, error) {
	fail := func() (*workspaceProfile, []string, error) {
		return nil, nil, fmt.Errorf("claudesdk: invalid trusted workspace configuration")
	}
	w := config.Workspace
	if w == nil || !filepath.IsAbs(config.Node) || !filepath.IsAbs(config.Entrypoint) {
		return fail()
	}
	if w.NetworkAccess != "" && w.NetworkAccess != "disabled" && w.NetworkAccess != "enabled" {
		return fail()
	}
	if w.PublicDirectory != "" {
		actual, err := os.Stat(w.Directory)
		alias, aliasErr := os.Stat(w.PublicDirectory)
		if !canonicalWorkspaceDir(w.Directory) || !canonicalWorkspaceDir(w.PublicDirectory) || err != nil || aliasErr != nil || !os.SameFile(actual, alias) {
			return fail()
		}
	}
	runtimeDir := filepath.Dir(filepath.Dir(config.Entrypoint))
	if config.Entrypoint != filepath.Join(runtimeDir, "dist", "main.js") || !canonicalWorkspaceDir(runtimeDir) {
		return fail()
	}
	// Keep the exact paths used by execution and readiness outside mutable roots.
	// A symlinked entrypoint must not select an unchecked sibling companion.
	codePaths := []string{config.Node, config.Entrypoint, filepath.Join(filepath.Dir(config.Entrypoint), "runtime_check.js")}
	for _, path := range codePaths {
		resolved, err := filepath.EvalSymlinks(path)
		info, statErr := os.Stat(path)
		if err != nil || resolved != path || statErr != nil || !info.Mode().IsRegular() {
			return fail()
		}
	}
	roots := append([]string{workspaceCwd(w), config.StateDir, w.HomeDir, w.ScratchDir}, w.ProtectedDirs...)
	for i, dir := range roots {
		if !canonicalWorkspaceDir(dir) || pathContains(runtimeDir, dir) || pathContains(dir, runtimeDir) {
			return fail()
		}
		for _, previous := range roots[:i] {
			if pathContains(previous, dir) || pathContains(dir, previous) {
				return fail()
			}
		}
		for _, executable := range codePaths {
			if pathContains(dir, executable) {
				return fail()
			}
		}
	}
	managed, err := paths.Root()
	if err != nil {
		return fail()
	}
	managed, err = filepath.EvalSymlinks(managed)
	if err != nil || managed == config.StateDir || !pathContains(managed, config.StateDir) {
		return fail()
	}
	if w.DependencyPath == "" {
		return fail()
	}
	var dependencies []string
	for _, dir := range filepath.SplitList(w.DependencyPath) {
		info, err := os.Stat(dir)
		if !workspacePathSyntax(dir) || err != nil || !info.IsDir() {
			return fail()
		}
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return fail()
		}
		for _, root := range roots {
			if pathContains(root, dir) || pathContains(dir, root) || pathContains(root, resolved) || pathContains(resolved, root) {
				return fail()
			}
		}
		dependencies = append(dependencies, resolved)
	}
	dependencyPath := strings.Join(dependencies, string(os.PathListSeparator))
	profile := &workspaceProfile{Home: w.HomeDir, State: config.StateDir, Scratch: w.ScratchDir,
		ProtectedDirs: append([]string{}, w.ProtectedDirs...), DependencyPath: dependencyPath, EnvNames: []string{}, NetworkAccess: w.NetworkAccess}
	env := []string{"PATH=" + dependencyPath, "HOME=" + w.HomeDir, "TMPDIR=" + w.ScratchDir,
		"CLAUDE_CONFIG_DIR=" + config.StateDir, "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1",
		"DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1"}
	seen := map[string]bool{}
	for _, entry := range config.Env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || seen[name] || strings.ContainsRune(entry, '\x00') || !workspaceEnvName(name) {
			return fail()
		}
		seen[name] = true
		profile.EnvNames = append(profile.EnvNames, name)
		env = append(env, entry)
	}
	return profile, env, nil
}

func workspaceEnvName(name string) bool {
	switch name {
	case "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
		"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY":
		return true
	default:
		return false
	}
}

func canonicalWorkspaceDir(dir string) bool {
	if !workspacePathSyntax(dir) {
		return false
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || resolved != dir {
		return false
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

func workspacePathSyntax(dir string) bool {
	return filepath.IsAbs(dir) && filepath.Clean(dir) == dir && dir != string(filepath.Separator) &&
		!strings.ContainsAny(dir, "*?[]{}():\\") && strings.IndexFunc(dir, func(r rune) bool { return r < 32 || r == 127 }) == -1
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
