package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
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
// Only the fields needed for Phase 0 are defined.
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
	fmt.Fprintf(ctx.stdout, "plugin %q removed\n", name)
	return nil
}
