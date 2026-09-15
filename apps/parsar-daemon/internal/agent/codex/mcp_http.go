package codex

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// A non-nil public declaration owns the complete MCP profile, including an empty
// declaration. Product requests without this field retain their existing options.
func publicMCPHTTPServers(req proto.PromptRequestPayload) (map[string]mcpServerConfig, error) {
	if req.MCPHTTPServers == nil {
		return nil, nil
	}
	if req.DisableExecutionEnvironment == (req.RemoteEnvironment != nil) {
		return nil, errors.New("codex: public HTTP MCP requires environment:none or a remote environment")
	}
	servers := make(map[string]mcpServerConfig, len(*req.MCPHTTPServers))
	for _, declaration := range *req.MCPHTTPServers {
		name := declaration.ServerLabel
		if name == "" || strings.TrimSpace(name) != name || name == "codex_apps" {
			return nil, errors.New("codex: unsupported public MCP server label")
		}
		if _, exists := servers[name]; exists {
			return nil, errors.New("codex: duplicate public MCP server label")
		}
		endpoint, err := url.Parse(declaration.ServerURL)
		if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.Opaque != "" {
			return nil, errors.New("codex: unsupported public MCP server URL")
		}
		if declaration.BearerToken != nil && (endpoint.Scheme != "https" || !validMCPHTTPBearerToken(*declaration.BearerToken)) {
			return nil, errors.New("codex: unsupported HTTPS MCP bearer credential")
		}
		server := mcpServerConfig{Name: name, URL: declaration.ServerURL}
		if declaration.AllowedTools != nil {
			tools := slices.Clone(*declaration.AllowedTools)
			for _, tool := range tools {
				if tool == "" || strings.TrimSpace(tool) != tool {
					return nil, errors.New("codex: invalid public MCP tool allowlist")
				}
			}
			server.EnabledTools = &tools
		}
		servers[name] = server
	}
	return servers, nil
}

func configureMCPHTTP(plan *SessionPlan, servers map[string]mcpServerConfig) error {
	var codexHome string
	for _, entry := range plan.Env {
		if value, ok := strings.CutPrefix(entry, "CODEX_HOME="); ok {
			codexHome = value
		}
	}
	if codexHome == "" || !filepath.IsAbs(codexHome) {
		return errors.New("codex: public MCP requires a private native home")
	}
	// Native OAuth defaults to the global keyring. File mode confines lookup to
	// this owned history directory; never delete existing credentials to admit it.
	if _, err := os.Lstat(filepath.Join(codexHome, ".credentials.json")); !errors.Is(err, os.ErrNotExist) {
		return errors.New("codex: public MCP requires a native home without stored MCP credentials")
	}
	if err := writeCodexMCPConfig(codexHome, servers); err != nil {
		return errors.New("codex: cannot write public MCP configuration")
	}
	if plan.Cwd == "" {
		plan.Cwd = codexHome
	}
	for _, feature := range []string{"plugins", "apps"} {
		plan.EnableFeatures = slices.DeleteFunc(plan.EnableFeatures, func(value string) bool { return value == feature })
		if !slices.Contains(plan.DisableFeatures, feature) {
			plan.DisableFeatures = append(plan.DisableFeatures, feature)
		}
	}
	plan.ExtraConfig = append(plan.ExtraConfig, [2]string{"mcp_oauth_credentials_store", `"file"`})
	plan.mcpHTTPServers = servers
	return nil
}
