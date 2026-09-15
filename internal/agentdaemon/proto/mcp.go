package proto

// MCPHTTPServer declares HTTP tools on the trusted harness host. Send only to a
// peer advertising mcp_http_tools; a transient BearerToken additionally requires
// mcp_http_bearer_auth. Never persist or log this private request as configuration.
type MCPHTTPServer struct {
	ServerLabel  string    `json:"server_label"`
	ServerURL    string    `json:"server_url"`
	AllowedTools *[]string `json:"allowed_tools"`
	BearerToken  *string   `json:"bearer_token,omitempty"`
}
