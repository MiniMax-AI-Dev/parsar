// Package v1 contains the supported wire types from the pinned Agents API.
package v1

import "encoding/json"

// CreateSessionRequest currently supports inline agents without initial input.
type CreateSessionRequest struct {
	Agent       *InlineAgent      `json:"agent" binding:"required"`
	AgentID     *string           `json:"agent_id,omitempty"`
	Environment *Environment      `json:"environment" binding:"required"`
	Input       json.RawMessage   `json:"input,omitempty" swaggertype:"object"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Stream      bool              `json:"stream,omitempty"`
	VaultIDs    []string          `json:"vault_ids,omitempty"`
}

type InlineAgent struct {
	Model        string  `json:"model" binding:"required"`
	Instructions *string `json:"instructions,omitempty"`
}

// Environment currently supports the upstream environment-free configuration.
type Environment struct {
	Type string `json:"type" enums:"none" binding:"required"`
}

type Agent struct {
	ID           string            `json:"id" binding:"required"`
	Instructions *string           `json:"instructions"`
	Model        string            `json:"model" binding:"required"`
	MultiAgent   MultiAgentConfig  `json:"multi_agent" binding:"required"`
	Name         *string           `json:"name"`
	Reasoning    Reasoning         `json:"reasoning" binding:"required"`
	ServiceTier  string            `json:"service_tier" enums:"auto" binding:"required"`
	Text         TextConfig        `json:"text" binding:"required"`
	Tools        []json.RawMessage `json:"tools" swaggertype:"array,object" binding:"required"`
}

type MultiAgentConfig struct {
	Enabled                bool `json:"enabled" binding:"required"`
	MaxConcurrentSubagents *int `json:"max_concurrent_subagents"`
}

type Reasoning struct {
	Effort  *string `json:"effort,omitempty"`
	Summary *string `json:"summary,omitempty"`
}

type TextConfig struct {
	Format    TextFormat `json:"format" binding:"required"`
	Verbosity string     `json:"verbosity" enums:"medium" binding:"required"`
}

type TextFormat struct {
	Type string `json:"type" enums:"text" binding:"required"`
}

type Session struct {
	ID              string            `json:"id" binding:"required"`
	Agent           Agent             `json:"agent" binding:"required"`
	CreatedAt       int64             `json:"created_at" binding:"required"`
	Environment     Environment       `json:"environment" binding:"required"`
	Error           *string           `json:"error"`
	LastActiveAt    int64             `json:"last_active_at" binding:"required"`
	Metadata        map[string]string `json:"metadata" binding:"required"`
	Object          string            `json:"object" enums:"agent.session" binding:"required"`
	RequiredActions []json.RawMessage `json:"required_actions" swaggertype:"array,object" binding:"required"`
	Status          string            `json:"status" enums:"idle" binding:"required"`
	Usage           json.RawMessage   `json:"usage" swaggertype:"object"`
	VaultIDs        []string          `json:"vault_ids" binding:"required"`
}

type SessionList struct {
	Data    []Session `json:"data" binding:"required"`
	HasMore bool      `json:"has_more" binding:"required"`
}

type ErrorResponse struct {
	Error APIError `json:"error" binding:"required"`
}

type APIError struct {
	Message string  `json:"message" binding:"required"`
	Type    string  `json:"type" binding:"required"`
	Code    string  `json:"code" binding:"required"`
	Param   *string `json:"param"`
}
