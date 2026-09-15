package proto

// TypeSubagentIdentity carries verified native identity facts, not public lifecycle.
const TypeSubagentIdentity = "subagent_identity"

// SubagentIdentityPayload is scoped by the authenticated Run envelope. The service
// supplies its Session, project, device and public identity; none comes from here.
type SubagentIdentityPayload struct {
	NativeID        string `json:"native_id"`
	ParentNativeID  string `json:"parent_native_id"`
	NativeCreatedAt int64  `json:"native_created_at"`
	ParentTurnID    string `json:"parent_turn_id"`
	SourceItemID    string `json:"source_item_id"`
}
