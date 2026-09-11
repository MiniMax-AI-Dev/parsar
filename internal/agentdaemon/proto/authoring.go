package proto

import "encoding/json"

const (
	TypeAuthoringRequest  = "authoring_request"
	TypeAuthoringResponse = "authoring_response"
	AuthoringContext      = "context"
	AuthoringSkillList    = "skill.list"
	AuthoringSkillRead    = "skill.read"
	AuthoringSkillCreate  = "skill.create"
	AuthoringSkillUpdate  = "skill.update"
	AuthoringPromptRead   = "instructions.read"
	AuthoringPromptWrite  = "instructions.write"
	AuthoringMaxBytes     = 1 << 20
	AuthoringSocketEnv    = "PARSAR_DAEMON_SOCKET"
)

// AuthoringRequestPayload uses Envelope.ID for the active run, never a client-supplied workspace or user.
type AuthoringRequestPayload struct {
	RequestID    string `json:"request_id,omitempty"`
	Operation    string `json:"operation"`
	CapabilityID string `json:"capability_id,omitempty"`
	Content      string `json:"content,omitempty"`
}

type AuthoringResponsePayload struct {
	RequestID string          `json:"request_id,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Error     string          `json:"error,omitempty"`
}
