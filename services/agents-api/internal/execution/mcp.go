package execution

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func executionTools(raw []json.RawMessage) ([]proto.FunctionTool, []proto.MCPHTTPServer, error) {
	functions := make([]json.RawMessage, 0, len(raw))
	var servers []proto.MCPHTTPServer
	names := map[string]bool{}
	for _, value := range raw {
		var kind struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(value, &kind) != nil {
			return nil, nil, errors.New("invalid execution tool")
		}
		if kind.Type != "mcp" {
			functions = append(functions, value)
			continue
		}
		var tool v1.MCPTool
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&tool) != nil || strings.TrimSpace(tool.ServerLabel) == "" || names[tool.ServerLabel] || tool.ConnectionOrigin != "service" || tool.Required || len(tool.RequestMetadata) != 0 || tool.Transport.Type != "http" || tool.Transport.Headers != nil {
			return nil, nil, errors.New("unsupported execution MCP configuration")
		}
		u, err := url.Parse(tool.Transport.ServerURL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery {
			return nil, nil, errors.New("unsupported execution MCP URL")
		}
		if tool.AllowedTools != nil {
			for _, name := range *tool.AllowedTools {
				if name == "" {
					return nil, nil, errors.New("invalid execution MCP tool name")
				}
			}
		}
		names[tool.ServerLabel] = true
		servers = append(servers, proto.MCPHTTPServer{ServerLabel: tool.ServerLabel,
			ServerURL: tool.Transport.ServerURL, AllowedTools: tool.AllowedTools})
	}
	resolved, err := functionTools(functions)
	return resolved, servers, err
}
