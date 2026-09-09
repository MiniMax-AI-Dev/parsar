package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type mcpElicitationParams struct {
	ServerName string `json:"serverName"`
	Mode       string `json:"mode"`
	Message    string `json:"message"`
	Schema     struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	} `json:"requestedSchema"`
}

type mcpElicitationResponse struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content"`
}

func (s *Session) handleCodexMCPElicitation(raw json.RawMessage, rpcID any) (any, error) {
	var params mcpElicitationParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("decode MCP elicitation: %w", err)
	}
	if params.Mode != "form" || params.Schema.Type != "object" || params.Schema.Properties == nil || len(params.Schema.Properties) != 0 || len(params.Schema.Required) != 0 {
		return nil, errors.New("MCP elicitation requires unsupported input; only empty confirmation forms are supported")
	}
	if strings.TrimSpace(params.ServerName) == "" || strings.TrimSpace(params.Message) == "" {
		return nil, errors.New("MCP confirmation requires a server name and message")
	}
	return s.deferCodexPermission(rpcID, codexMCPApproval, "mcp:"+params.ServerName, params.Message, "", raw, nil)
}
