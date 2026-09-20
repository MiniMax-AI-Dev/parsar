package v1

import "encoding/json"

// CreateAgentRequest describes reusable configuration, not an execution request.
// MCP/web-search tools and model-derived reasoning defaults remain incomplete.
type CreateAgentRequest struct {
	XAgentsCore  *AgentsCore          `json:"x_agents_core,omitempty" extensions:"x-nullable"`
	Model        *string              `json:"model" binding:"required"`
	Name         *string              `json:"name,omitempty" extensions:"x-nullable" maxLength:"128"`
	Instructions *string              `json:"instructions,omitempty" extensions:"x-nullable"`
	Metadata     map[string]*string   `json:"metadata,omitempty" swaggertype:"object,string" extensions:"x-nullable"`
	MultiAgent   json.RawMessage      `json:"multi_agent,omitempty" swaggertype:"object" extensions:"x-nullable"`
	Reasoning    *Reasoning           `json:"reasoning,omitempty" extensions:"x-nullable"`
	ServiceTier  *string              `json:"service_tier,omitempty" enums:"auto,default,flex,priority,fast" extensions:"x-nullable"`
	Text         *SavedAgentTextInput `json:"text,omitempty" extensions:"x-nullable"`
	Tools        []json.RawMessage    `json:"tools,omitempty" swaggertype:"array,object" extensions:"x-nullable"`
}

// UpdateAgentRequest replaces supplied fields and preserves omitted fields.
type UpdateAgentRequest struct {
	XAgentsCore  *AgentsCore          `json:"x_agents_core,omitempty" extensions:"x-nullable"`
	Model        *string              `json:"model,omitempty"`
	Name         *string              `json:"name,omitempty" extensions:"x-nullable" maxLength:"128"`
	Instructions *string              `json:"instructions,omitempty" extensions:"x-nullable"`
	Metadata     map[string]*string   `json:"metadata,omitempty" swaggertype:"object,string" extensions:"x-nullable"`
	MultiAgent   json.RawMessage      `json:"multi_agent,omitempty" swaggertype:"object" extensions:"x-nullable"`
	Reasoning    *Reasoning           `json:"reasoning,omitempty" extensions:"x-nullable"`
	ServiceTier  *string              `json:"service_tier,omitempty" enums:"auto,default,flex,priority,fast" extensions:"x-nullable"`
	Text         *SavedAgentTextInput `json:"text,omitempty" extensions:"x-nullable"`
	Tools        []json.RawMessage    `json:"tools,omitempty" swaggertype:"array,object" extensions:"x-nullable"`
}

type SavedAgentTextInput struct {
	Format    json.RawMessage `json:"format,omitempty" swaggertype:"object" extensions:"x-nullable"`
	Verbosity *string         `json:"verbosity,omitempty" enums:"low,medium,high" extensions:"x-nullable"`
}

type SavedAgentText struct {
	Format    SavedAgentTextFormat `json:"format" binding:"required"`
	Verbosity string               `json:"verbosity" binding:"required" enums:"low,medium,high"`
}

type SavedAgentTextFormat struct {
	Type   string          `json:"type" binding:"required" enums:"text,json_schema"`
	Schema json.RawMessage `json:"schema,omitempty" swaggertype:"object"`
}

// SavedAgentConfiguration excludes resource identity and mutable metadata. It is
// not the immutable effective configuration of an execution Session.
type SavedAgentConfiguration struct {
	XAgentsCore  *AgentsCore       `json:"x_agents_core,omitempty" extensions:"x-nullable"`
	Model        string            `json:"model" binding:"required"`
	Name         *string           `json:"name" extensions:"x-nullable"`
	Instructions *string           `json:"instructions" extensions:"x-nullable"`
	MultiAgent   MultiAgentConfig  `json:"multi_agent" binding:"required"`
	Reasoning    Reasoning         `json:"reasoning" binding:"required"`
	ServiceTier  string            `json:"service_tier" binding:"required" enums:"auto,default,flex,priority,fast"`
	Text         SavedAgentText    `json:"text" binding:"required"`
	Tools        []json.RawMessage `json:"tools" binding:"required" swaggertype:"array,object"`
}

type SavedAgent struct {
	SavedAgentConfiguration
	ID        string            `json:"id" binding:"required"`
	Object    string            `json:"object" binding:"required" enums:"agent"`
	Metadata  map[string]string `json:"metadata" binding:"required"`
	CreatedAt int64             `json:"created_at" binding:"required"`
	UpdatedAt int64             `json:"updated_at" binding:"required"`
}

type SavedAgentList struct {
	Object  string       `json:"object" binding:"required" enums:"list"`
	Data    []SavedAgent `json:"data" binding:"required"`
	HasMore bool         `json:"has_more" binding:"required"`
	FirstID *string      `json:"first_id" extensions:"x-nullable"`
	LastID  *string      `json:"last_id" extensions:"x-nullable"`
}

// AgentDeleted is the pinned successful saved-resource deletion response.
type AgentDeleted struct {
	ID      string `json:"id" binding:"required"`
	Object  string `json:"object" binding:"required" enums:"agent.deleted"`
	Deleted bool   `json:"deleted" binding:"required" enums:"true"`
}
