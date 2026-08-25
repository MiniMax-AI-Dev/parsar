package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

func runPlugin(ctx *runContext, args []string) error {
	if len(args) == 0 {
		printPluginHelp(ctx.stdout)
		return fmt.Errorf("plugin: missing subcommand")
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printPluginHelp(ctx.stdout)
		return nil
	}
	for _, sc := range pluginSubcommands {
		if sc.name == args[0] {
			return sc.run(ctx, args[1:])
		}
	}
	printPluginHelp(ctx.stderr)
	return fmt.Errorf("plugin: unknown subcommand %q", args[0])
}

var pluginSubcommands = []command{
	{name: "add", summary: "Install a plugin bundle from a local directory", run: runPluginAdd},
	{name: "list", summary: "List installed plugin bundles", run: runPluginList},
	{name: "remove", summary: "Remove an installed plugin bundle", run: runPluginRemove},
}

func printPluginHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: parsar plugin <subcommand> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Subcommands:")
	for _, sc := range pluginSubcommands {
		fmt.Fprintf(w, "  %-9s %s\n", sc.name, sc.summary)
	}
}

// ----- plugin add -----------------------------------------------------------

// pluginManifest mirrors the user-authored manifest.json in a plugin directory.
type pluginManifest struct {
	Name        string               `json:"name"`
	Version     string               `json:"version"`
	Description string               `json:"description,omitempty"`
	Author      string               `json:"author,omitempty"`
	Server      *pluginManifestEntry `json:"server,omitempty"`
	Client      *pluginManifestEntry `json:"client,omitempty"`
	Skills      []string             `json:"skills,omitempty"`
	Tools       []string             `json:"tools,omitempty"`
	Hooks       []string             `json:"hooks,omitempty"`
	Credentials []string             `json:"credentials,omitempty"`
}

type pluginManifestEntry struct {
	Entry string   `json:"entry,omitempty"`
	Tools []string `json:"tools,omitempty"`
}

// bundleSkillPayload mirrors canonical.BundleSkill for the API request.
type bundleSkillPayload struct {
	Slug        string `json:"slug"`
	Instruction string `json:"instruction"`
}

func runPluginAdd(ctx *runContext, args []string) error {
	fs := newFlagSet("plugin add")
	jsonOut := fs.Bool("json", false, "emit JSON of the created capability")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("plugin add: parse flags: %w", err)
	}
	remaining := fs.Args()
	if len(remaining) == 0 {
		return fmt.Errorf("plugin add: path to plugin directory is required")
	}
	pluginDir := remaining[0]

	// Read and parse manifest.json
	manifestPath := filepath.Join(pluginDir, "manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("plugin add: read manifest: %w", err)
	}
	var manifest pluginManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("plugin add: parse manifest: %w", err)
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return fmt.Errorf("plugin add: manifest.name is required")
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return fmt.Errorf("plugin add: manifest.version is required")
	}

	// Read skill files and embed content
	skills, err := readPluginSkills(pluginDir, manifest.Skills)
	if err != nil {
		return fmt.Errorf("plugin add: %w", err)
	}

	// Build the canonical_spec
	bundleSpec := map[string]any{
		"name":    manifest.Name,
		"version": manifest.Version,
	}
	if manifest.Description != "" {
		bundleSpec["description"] = manifest.Description
	}
	if manifest.Author != "" {
		bundleSpec["author"] = manifest.Author
	}
	if manifest.Server != nil && manifest.Server.Entry != "" {
		bundleSpec["server_entry"] = manifest.Server.Entry
	}
	if manifest.Client != nil && manifest.Client.Entry != "" {
		bundleSpec["client_entry"] = manifest.Client.Entry
	}
	if len(skills) > 0 {
		bundleSpec["skills"] = skills
	}
	// Collect tools from manifest top-level or server.tools
	tools := manifest.Tools
	if manifest.Server != nil && len(manifest.Server.Tools) > 0 {
		tools = append(tools, manifest.Server.Tools...)
	}
	if len(tools) > 0 {
		bundleSpec["tools"] = tools
	}
	if len(manifest.Hooks) > 0 {
		bundleSpec["hooks"] = manifest.Hooks
	}
	if len(manifest.Credentials) > 0 {
		bundleSpec["credentials"] = manifest.Credentials
	}

	canonicalSpec := map[string]any{
		"schema_version": 1,
		"kind":           "bundle",
		"bundle":         bundleSpec,
	}

	// Build the API request
	reqBody := map[string]any{
		"type":        "bundle",
		"name":        manifest.Name,
		"description": manifest.Description,
		"visibility":  "workspace",
		"version":     manifest.Version,
		"canonical_spec": canonicalSpec,
	}

	// Phase 1: if the plugin has a server entry, copy the plugin directory
	// to the plugins storage dir BEFORE the API call. If copy fails, we
	// leave harmless files on disk rather than a DB record with no loadable
	// server code (which would cause runtime spawn errors).
	if manifest.Server != nil && manifest.Server.Entry != "" {
		if err := copyPluginToStorage(pluginDir, manifest.Name); err != nil {
			return fmt.Errorf("plugin add: copy server files: %w", err)
		}
	}

	// Phase 2: if the plugin has a client entry, build it with esbuild
	// and copy the built bundle to the plugins storage dir.
	if manifest.Client != nil && manifest.Client.Entry != "" {
		if err := buildAndCopyClient(pluginDir, manifest.Name, manifest.Client.Entry); err != nil {
			return fmt.Errorf("plugin add: build client: %w", err)
		}
	}

	cfg, err := ctx.resolveConfig()
	if err != nil {
		return fmt.Errorf("plugin add: %w", err)
	}
	if strings.TrimSpace(cfg.WorkspaceID) == "" {
		return fmt.Errorf("plugin add: PARSAR_WORKSPACE_ID is required")
	}
	var result map[string]any
	if err := newClient(cfg).do(context.Background(), "POST", "/api/v1/workspaces/"+cfg.WorkspaceID+"/capabilities/plugins/install", nil, reqBody, &result); err != nil {
		return fmt.Errorf("plugin add: %w", err)
	}

	if *jsonOut {
		return emitJSON(ctx.stdout, result)
	}
	name := manifest.Name
	if id, ok := result["id"].(string); ok {
		fmt.Fprintf(ctx.stdout, "plugin %q installed (capability_id=%s)\n", name, id)
	} else {
		fmt.Fprintf(ctx.stdout, "plugin %q installed\n", name)
	}
	return nil
}

// readPluginSkills reads skill markdown files from the plugin directory
// and returns them as inline payloads for the canonical_spec.
func readPluginSkills(pluginDir string, skillPaths []string) ([]bundleSkillPayload, error) {
	if len(skillPaths) == 0 {
		return nil, nil
	}
	var skills []bundleSkillPayload
	for _, relPath := range skillPaths {
		fullPath := filepath.Join(pluginDir, relPath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("read skill %s: %w", relPath, err)
		}
		// Derive slug from filename: "skills/customer-service.md" → "customer-service"
		base := filepath.Base(relPath)
		slug := strings.TrimSuffix(base, filepath.Ext(base))
		skills = append(skills, bundleSkillPayload{
			Slug:        slug,
			Instruction: string(content),
		})
	}
	return skills, nil
}

// ----- plugin list ----------------------------------------------------------

func runPluginList(ctx *runContext, args []string) error {
	fs := newFlagSet("plugin list")
	jsonOut := fs.Bool("json", false, "emit JSON instead of the table")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("plugin list: parse flags: %w", err)
	}
	cfg, err := ctx.resolveConfig()
	if err != nil {
		return fmt.Errorf("plugin list: %w", err)
	}
	if strings.TrimSpace(cfg.WorkspaceID) == "" {
		return fmt.Errorf("plugin list: PARSAR_WORKSPACE_ID is required")
	}
	var result struct {
		Capabilities []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Type        string `json:"type"`
			Description string `json:"description"`
			Version     string `json:"latest_version"`
		} `json:"capabilities"`
	}
	if err := newClient(cfg).do(context.Background(), "GET", "/api/v1/workspaces/"+cfg.WorkspaceID+"/capabilities?type=bundle", nil, nil, &result); err != nil {
		return fmt.Errorf("plugin list: %w", err)
	}
	if *jsonOut {
		return emitJSON(ctx.stdout, result.Capabilities)
	}
	if len(result.Capabilities) == 0 {
		fmt.Fprintln(ctx.stdout, "(no plugins installed)")
		return nil
	}
	tw := tabwriter.NewWriter(ctx.stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tVERSION\tDESCRIPTION")
	for _, c := range result.Capabilities {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", c.Name, c.Version, truncate(c.Description, 50))
	}
	return tw.Flush()
}

// ----- plugin remove --------------------------------------------------------

func runPluginRemove(ctx *runContext, args []string) error {
	fs := newFlagSet("plugin remove")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("plugin remove: parse flags: %w", err)
	}
	remaining := fs.Args()
	if len(remaining) == 0 {
		return fmt.Errorf("plugin remove: plugin name is required")
	}
	name := remaining[0]
	cfg, err := ctx.resolveConfig()
	if err != nil {
		return fmt.Errorf("plugin remove: %w", err)
	}
	if strings.TrimSpace(cfg.WorkspaceID) == "" {
		return fmt.Errorf("plugin remove: PARSAR_WORKSPACE_ID is required")
	}

	// Resolve plugin name to capability_id via the list endpoint.
	var listResult struct {
		Capabilities []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"capabilities"`
	}
	c := newClient(cfg)
	if err := c.do(context.Background(), "GET", "/api/v1/workspaces/"+cfg.WorkspaceID+"/capabilities?type=bundle", nil, nil, &listResult); err != nil {
		return fmt.Errorf("plugin remove: list plugins: %w", err)
	}
	var capabilityID string
	for _, cap := range listResult.Capabilities {
		if cap.Name == name {
			capabilityID = cap.ID
			break
		}
	}
	if capabilityID == "" {
		return fmt.Errorf("plugin remove: plugin %q not found", name)
	}

	// Delete by capability ID using the existing endpoint.
	if err := c.do(context.Background(), "DELETE", "/api/v1/workspaces/"+cfg.WorkspaceID+"/capabilities/"+capabilityID, nil, nil, nil); err != nil {
		return fmt.Errorf("plugin remove: %w", err)
	}

	// Clean up on-disk plugin files (best-effort; failure is logged but
	// doesn't fail the command since the DB record is already gone).
	if pluginsDir, err := resolvePluginsDir(); err == nil {
		dirName := pluginDirName(name)
		_ = os.RemoveAll(filepath.Join(pluginsDir, dirName))
	}

	fmt.Fprintf(ctx.stdout, "plugin %q removed\n", name)
	return nil
}

// ----- plugin storage -------------------------------------------------------

// resolvePluginsDir determines the plugins storage directory.
// Reads PARSAR_DATA_DIR (same env the server uses), defaults to ~/.parsar.
func resolvePluginsDir() (string, error) {
	dataDir := strings.TrimSpace(os.Getenv("PARSAR_DATA_DIR"))
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		dataDir = filepath.Join(home, ".parsar")
	}
	return filepath.Join(dataDir, "plugins"), nil
}

// pluginDirName converts a bundle name (possibly scoped) to the directory
// name under plugins/. Strips the "@scope/" prefix.
// NOTE: duplicated in server/internal/connector/agentdaemon/capability_runtime.go
// (bundleNameToDirName). Keep both in sync until a shared package is extracted.
func pluginDirName(name string) string {
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

// copyPluginToStorage copies the plugin source directory into
// <plugins_dir>/<dir-name>/, creating the target if needed. Existing
// contents are replaced (simple rm + copy).
func copyPluginToStorage(srcDir, pluginName string) error {
	pluginsDir, err := resolvePluginsDir()
	if err != nil {
		return err
	}
	dirName := pluginDirName(pluginName)
	dstDir := filepath.Join(pluginsDir, dirName)

	// Remove previous install (idempotent upgrade).
	_ = os.RemoveAll(dstDir)

	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("create plugin dir: %w", err)
	}

	return copyDir(srcDir, dstDir)
}

// copyDir recursively copies src into dst. Both must exist.
// Skips node_modules, .git, and symlinks.
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		// Skip symlinks — avoid traversing outside the plugin tree.
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}

		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			// Skip node_modules — never copy dependency trees.
			if entry.Name() == "node_modules" || entry.Name() == ".git" {
				continue
			}
			if err := os.MkdirAll(dstPath, 0o755); err != nil {
				return err
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return err
			}
			// Preserve execute bit for scripts.
			info, _ := entry.Info()
			mode := os.FileMode(0o644)
			if info != nil && info.Mode()&0o111 != 0 {
				mode = 0o755
			}
			if err := os.WriteFile(dstPath, data, mode); err != nil {
				return err
			}
		}
	}
	return nil
}

// ----- client build ---------------------------------------------------------

// buildAndCopyClient builds the plugin's client entry with esbuild and
// copies the output to <plugins_dir>/<dir-name>/dist/client.js.
//
// Requires: node + esbuild available (esbuild is loaded as ESM import in
// the build-client.js script). The build script lives next to plugin-host.
func buildAndCopyClient(pluginDir, pluginName, clientEntry string) error {
	pluginsDir, err := resolvePluginsDir()
	if err != nil {
		return err
	}
	dirName := pluginDirName(pluginName)
	dstDir := filepath.Join(pluginsDir, dirName, "dist")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("create dist dir: %w", err)
	}

	entryPath := filepath.Join(pluginDir, clientEntry)
	outPath := filepath.Join(dstDir, "client.js")

	// Locate the build-client.js script. It lives alongside plugin-host.
	// Try PARSAR_PLUGIN_HOST_PATH directory first, then fallback to relative.
	buildScript := resolveBuildScript()
	if buildScript == "" {
		return fmt.Errorf("cannot locate build-client.js; ensure PARSAR_PLUGIN_HOST_PATH is set")
	}

	// Run: node build-client.js <entry> <outfile>
	cmd := osexec.Command("node", buildScript, entryPath, outPath)
	cmd.Dir = pluginDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("esbuild failed: %s\n%s", err, string(output))
	}
	return nil
}

// resolveBuildScript finds the build-client.js script path.
func resolveBuildScript() string {
	// From PARSAR_PLUGIN_HOST_PATH (same dir as plugin-host/index.js).
	hostPath := strings.TrimSpace(os.Getenv("PARSAR_PLUGIN_HOST_PATH"))
	if hostPath != "" {
		dir := filepath.Dir(hostPath)
		candidate := filepath.Join(dir, "build-client.js")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}
