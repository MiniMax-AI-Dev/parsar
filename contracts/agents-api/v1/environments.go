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

// EnvironmentNetwork is the effective hosted network policy.
type EnvironmentNetwork struct {
	Access         string   `json:"access" binding:"required" enums:"enabled,disabled,restricted"`
	AllowedDomains []string `json:"allowed_domains" binding:"required"`
}

// EnvironmentNetworkInput preserves the optional request domain list.
type EnvironmentNetworkInput struct {
	Access         string   `json:"access" binding:"required" enums:"enabled,disabled,restricted"`
	AllowedDomains []string `json:"allowed_domains,omitempty" extensions:"x-nullable"`
}

type EnvironmentPackages struct {
	NPM    []string `json:"npm" binding:"required"`
	Python []string `json:"python" binding:"required"`
	System []string `json:"system" binding:"required"`
}
