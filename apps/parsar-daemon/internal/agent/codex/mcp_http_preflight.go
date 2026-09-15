package codex

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"
)

// config/read loads the same cwd and CLI layers without MCP discovery. Native
// mcpServerStatus/list instead opens eager discovery connections; do not use it
// to decide whether undeclared servers are safe to contact.
func verifyMCPHTTPConfig(ctx context.Context, rpc *JSONRPCClient, plan SessionPlan) error {
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	raw, err := rpc.Request(check, "config/read", map[string]any{"cwd": plan.Cwd, "includeLayers": false})
	if err != nil {
		// Native configuration errors and responses can contain operator secrets.
		return errors.New("codex: cannot verify public MCP configuration")
	}
	if !matchesMCPHTTPConfig(raw, plan.mcpHTTPServers) {
		return errors.New("codex: effective native MCP configuration differs from the public declaration")
	}
	return nil
}

func matchesMCPHTTPConfig(raw json.RawMessage, declared map[string]mcpServerConfig) bool {
	var response struct {
		Config struct {
			Servers         map[string]map[string]any `json:"mcp_servers"`
			Features        map[string]any            `json:"features"`
			CredentialStore string                    `json:"mcp_oauth_credentials_store"`
		} `json:"config"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return false
	}
	config := response.Config
	if config.CredentialStore != "file" || config.Features["plugins"] != false || config.Features["apps"] != false || config.Servers == nil || len(config.Servers) != len(declared) {
		return false
	}
	for name, expected := range declared {
		server, exists := config.Servers[name]
		if !exists || server["url"] != expected.URL || server["environment_id"] != "local" || server["enabled"] != true {
			return false
		}
		delete(server, "url")
		delete(server, "environment_id")
		delete(server, "enabled")
		if expected.EnabledTools != nil {
			// Compare sets: native enabled_tools is an allowlist, not an ordered program.
			actual, ok := server["enabled_tools"].([]any)
			if !ok {
				return false
			}
			want := make(map[string]bool, len(*expected.EnabledTools))
			for _, tool := range *expected.EnabledTools {
				want[tool] = true
			}
			got := make(map[string]bool, len(actual))
			for _, tool := range actual {
				name, ok := tool.(string)
				if !ok {
					return false
				}
				got[name] = true
			}
			if !reflect.DeepEqual(want, got) {
				return false
			}
			delete(server, "enabled_tools")
		}
		// These are the native serializer's inert defaults. Reject all additional
		// settings, including headers, credential helpers, tool policies and filters.
		for key, value := range server {
			switch key {
			case "tool_timeout_sec":
				if value != nil {
					return false
				}
			case "required", "supports_parallel_tool_calls":
				if value != false {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}
