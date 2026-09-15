package v1

import "encoding/json"

// MCPToolInput preserves presence for the pinned MCP configuration union.
type MCPToolInput struct {
	Type             string          `json:"type"`
	ServerLabel      *string         `json:"server_label"`
	Transport        json.RawMessage `json:"transport"`
	AllowedTools     json.RawMessage `json:"allowed_tools"`
	ConnectionOrigin *string         `json:"connection_origin"`
	CredentialID     *string         `json:"credential_id"`
	RequestMetadata  json.RawMessage `json:"request_metadata"`
	Required         json.RawMessage `json:"required"`
}

// MCPTool is the supported HTTP resource shape. Headers belong only to saved
// configuration; effective Session transports expose type and server_url.
type MCPTool struct {
	Type             string                     `json:"type"`
	ServerLabel      string                     `json:"server_label"`
	Transport        MCPHTTPTransport           `json:"transport"`
	AllowedTools     *[]string                  `json:"allowed_tools"`
	ConnectionOrigin string                     `json:"connection_origin"`
	CredentialID     *string                    `json:"credential_id"`
	RequestMetadata  map[string]json.RawMessage `json:"request_metadata"`
	Required         bool                       `json:"required"`
}

type MCPHTTPTransport struct {
	Type      string             `json:"type"`
	ServerURL string             `json:"server_url"`
	Headers   *map[string]string `json:"headers,omitempty"`
}
