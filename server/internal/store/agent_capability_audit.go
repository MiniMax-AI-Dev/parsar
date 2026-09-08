package store

import "time"

// AgentCapabilityAuditInput deliberately excludes configuration and credentials.
type AgentCapabilityAuditInput struct {
	WorkspaceID, AgentID, ActorID string
	Action                        string
	CapabilityID, VersionID       string
	ReferenceID, BuiltinKey       string
	PinningMode                   string
	Enabled                       bool
}

// RecordAgentCapabilityAudit records a successful explicit capability operation.
// ReferenceID preserves the DELETE route's version-or-capability identifier.
func (s *Store) RecordAgentCapabilityAudit(input AgentCapabilityAuditInput) {
	payload := map[string]any{"enabled": input.Enabled}
	for key, value := range map[string]string{
		"capability_id": input.CapabilityID, "capability_version_id": input.VersionID,
		"capability_reference": input.ReferenceID, "builtin_key": input.BuiltinKey,
		"pinning_mode": input.PinningMode,
	} {
		if value != "" {
			payload[key] = value
		}
	}
	s.emitAgentAudit(time.Now().UTC(), input.ActorID, "agent.capability."+input.Action,
		"agent", input.AgentID, input.WorkspaceID, payload)
}
