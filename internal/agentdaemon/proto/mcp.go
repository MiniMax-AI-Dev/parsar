package proto

// MCPHTTPServer declares credential-free HTTP tools on the trusted harness host.
// Send only to a peer advertising mcp_http_tools. Required-server initialization,
// other origins/transports and credential injection are separate capabilities.
type MCPHTTPServer struct {
	ServerLabel  string    `json:"server_label"`
	ServerURL    string    `json:"server_url"`
	AllowedTools *[]string `json:"allowed_tools"`
}
