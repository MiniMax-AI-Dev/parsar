package v1

import "encoding/json"

// EnvironmentInfo contains safe installed metadata; populated installation variants remain unsupported.
type EnvironmentInfo struct {
	ID      string            `json:"id" binding:"required"`
	Object  string            `json:"object" binding:"required" enums:"agent.environment"`
	Type    string            `json:"type" binding:"required" enums:"openai_hosted,self_hosted"`
	Status  string            `json:"status" binding:"required" enums:"pending,connected,disconnected,expired,failed"`
	Files   []json.RawMessage `json:"files" binding:"required" swaggertype:"array,object"`
	Plugins []json.RawMessage `json:"plugins" binding:"required" swaggertype:"array,object"`
	Skills  []json.RawMessage `json:"skills" binding:"required" swaggertype:"array,object"`
}
