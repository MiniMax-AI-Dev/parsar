package claudesdk

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

var mcpLabel = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
var mcpTool = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

func validateMCP(req proto.PromptRequestPayload) error {
	if req.MCPHTTPServers == nil {
		return nil
	}
	if !req.DisableExecutionEnvironment || req.RemoteEnvironment != nil {
		return fmt.Errorf("claudesdk: HTTP MCP requires environment:none")
	}
	labels := map[string]bool{}
	for _, server := range *req.MCPHTTPServers {
		endpoint, err := url.Parse(server.ServerURL)
		if !mcpLabel.MatchString(server.ServerLabel) || server.ServerLabel == "functions" || labels[server.ServerLabel] ||
			err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" || endpoint.User != nil ||
			strings.ContainsAny(server.ServerURL, "?#") || endpoint.Opaque != "" {
			return fmt.Errorf("claudesdk: unsupported HTTP MCP declaration")
		}
		if server.BearerToken != nil && (endpoint.Scheme != "https" || !agent.ValidMCPHTTPBearerToken(*server.BearerToken)) {
			return fmt.Errorf("claudesdk: unsupported HTTPS MCP bearer credential")
		}
		labels[server.ServerLabel] = true
		if server.AllowedTools != nil {
			for _, name := range *server.AllowedTools {
				if !mcpTool.MatchString(name) {
					return fmt.Errorf("claudesdk: unsupported MCP tool allowlist")
				}
			}
		}
	}
	return nil
}

// Only generated references cross the private bridge; secrets stay in the owned
// process environment and are expanded by the native HTTP client.
type mcpHTTPServer struct {
	ServerLabel       string    `json:"server_label"`
	ServerURL         string    `json:"server_url"`
	AllowedTools      *[]string `json:"allowed_tools"`
	Required          bool      `json:"required,omitempty"`
	BearerTokenEnvVar string    `json:"bearer_token_env_var,omitempty"`
}

func prepareMCPHTTP(declarations *[]proto.MCPHTTPServer) (*[]mcpHTTPServer, []string) {
	if declarations == nil {
		return nil, nil
	}
	servers := make([]mcpHTTPServer, len(*declarations))
	var env []string
	for i, declaration := range *declarations {
		server := mcpHTTPServer{ServerLabel: declaration.ServerLabel, ServerURL: declaration.ServerURL, Required: declaration.Required}
		if declaration.AllowedTools != nil {
			tools := append([]string{}, (*declaration.AllowedTools)...)
			server.AllowedTools = &tools
		}
		if declaration.BearerToken != nil {
			server.BearerTokenEnvVar = "PARSAR_MCP_BEARER_" + rand.Text()
			env = append(env, server.BearerTokenEnvVar+"="+*declaration.BearerToken)
		}
		servers[i] = server
	}
	return &servers, env
}

type mcpState struct {
	calls map[string]proto.ToolObservation
}

func (m *mcpState) receive(event bridgeEvent, start startRequest, emit func(string, any)) error {
	n := event.Observation
	if start.MCPHTTPServers == nil || n == nil || n.Kind != "mcp" || event.ID == "" || !json.Valid(n.Arguments) ||
		!json.Valid(n.Output) || !json.Valid(n.Error) {
		return fmt.Errorf("claudesdk: invalid MCP observation")
	}
	declared := false
	for _, server := range *start.MCPHTTPServers {
		if server.ServerLabel == n.Server && n.Name != "" && (server.AllowedTools == nil || slices.Contains(*server.AllowedTools, n.Name)) {
			declared = true
			break
		}
	}
	previous, exists := m.calls[event.ID]
	if !declared || (event.Stage == "before" && (exists || n.Status != "in_progress")) ||
		(event.Stage == "after" && (!exists || previous.Status != "in_progress" ||
			(n.Status != "completed" && n.Status != "failed" && n.Status != "incomplete") ||
			previous.Server != n.Server || previous.Name != n.Name || !bytes.Equal(previous.Arguments, n.Arguments))) ||
		(event.Stage != "before" && event.Stage != "after") {
		return fmt.Errorf("claudesdk: inconsistent MCP observation")
	}
	m.calls[event.ID] = *n
	if start.observeFunctions {
		emit(proto.TypeToolCall, proto.ToolCallPayload{ID: event.ID, Name: n.Name, Stage: event.Stage, Observation: n})
	}
	return nil
}

func (m *mcpState) complete() bool {
	for _, call := range m.calls {
		if call.Status == "in_progress" {
			return false
		}
	}
	return true
}

func (m *mcpState) close(start startRequest, emit func(string, any)) {
	for id, call := range m.calls {
		if call.Status != "in_progress" {
			continue
		}
		call.Status = "incomplete"
		m.calls[id] = call
		if start.observeFunctions {
			emit(proto.TypeToolCall, proto.ToolCallPayload{ID: id, Name: call.Name, Stage: "after", Observation: &call})
		}
	}
}
