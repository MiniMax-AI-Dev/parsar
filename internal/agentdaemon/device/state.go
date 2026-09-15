// Package device defines persistence data shared by daemon gateways and their stores.
package device

import "time"

const OwnerStatusConnected = "connected"
const OwnerStatusDraining = "draining"
const OwnerStatusExpired = "expired"

// Owner is the persisted view of the current
// WebSocket owner for one agent_daemon device. Generation is a fencing
// token: renewal/release paths must carry it so stale pods can't act.
type Owner struct {
	DeviceID       string
	WorkspaceID    string
	OwnerPodID     string
	OwnerURL       string
	Generation     int64
	Status         string
	ConnectedAt    time.Time
	LastSeenAt     time.Time
	LeaseExpiresAt time.Time
	UpdatedAt      time.Time
}

type ClaimOwner struct {
	DeviceID       string
	WorkspaceID    string
	OwnerPodID     string
	OwnerURL       string
	LeaseExpiresAt time.Time
	Now            time.Time
}

type RenewOwner struct {
	DeviceID       string
	OwnerPodID     string
	Generation     int64
	LeaseExpiresAt time.Time
	Now            time.Time
}

type ReleaseOwner struct {
	DeviceID   string
	OwnerPodID string
	Generation int64
}

// HeartbeatStatus is the post-heartbeat liveness the runner uses to
// detect state changes.
type HeartbeatStatus struct {
	Liveness string
	// Deleted is true when the heartbeat UPDATE matched zero rows,
	// meaning the runtime was soft-deleted (or never existed). The
	// gateway uses this to send a permanent WS close frame so the
	// daemon stops reconnecting.
	Deleted bool
}

// KindCapabilities mirrors the daemon heartbeat capability
// shape after gateway-level normalization. Persistence stays separate from wire protocol structs.
type KindCapabilities struct {
	Streaming         bool `json:"streaming,omitempty"`
	Permissions       bool `json:"permissions,omitempty"`
	Usage             bool `json:"usage,omitempty"`
	Resume            bool `json:"resume,omitempty"`
	Steering          bool `json:"steering,omitempty"`
	MessageItems      bool `json:"message_items,omitempty"`
	ToolItems         bool `json:"tool_items,omitempty"`
	ToolObservations  bool `json:"tool_observations,omitempty"`
	EnvironmentNone   bool `json:"environment_none,omitempty"`
	RemoteEnvironment bool `json:"remote_environment,omitempty"`
	Preparation       bool `json:"preparation,omitempty"`
	WebSearchControl  bool `json:"web_search_control,omitempty"`
	// ExecutionControls supports typed search and verbosity controls.
	ExecutionControls        bool `json:"execution_controls,omitempty"`
	TextVerbosity            bool `json:"text_verbosity,omitempty"`
	SubagentControl          bool `json:"subagent_control,omitempty"`
	FunctionTools            bool `json:"function_tools,omitempty"`
	MCPHTTPTools             bool `json:"mcp_http_tools,omitempty"`
	MCPHTTPRemoteEnvironment bool `json:"mcp_http_remote_environment,omitempty"`
	MCPHTTPRemoteBearerAuth  bool `json:"mcp_http_remote_bearer_auth,omitempty"`
	MCPHTTPBearerAuth        bool `json:"mcp_http_bearer_auth,omitempty"`
	DurableInputReceipts     bool `json:"durable_input_receipts,omitempty"`
	DurableTurns             bool `json:"durable_turns,omitempty"`
	WorkspaceAuthoring       bool `json:"workspace_authoring,omitempty"`
}

// SupportedAgentKind is the sanitized runtime.config view
// of one daemon-side agent_kind.
type SupportedAgentKind struct {
	Kind         string           `json:"kind"`
	Available    bool             `json:"available"`
	Version      string           `json:"version,omitempty"`
	Capabilities KindCapabilities `json:"capabilities,omitempty"`
}

// Heartbeat is the WebSocket daemon heartbeat
// payload after gateway normalization.
type Heartbeat struct {
	RuntimeID           string
	DaemonVersion       string
	ActiveRequests      int
	HeartbeatTimestamp  int64
	SupportedAgentKinds []SupportedAgentKind
}
