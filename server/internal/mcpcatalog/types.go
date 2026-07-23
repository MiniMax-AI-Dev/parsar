package mcpcatalog

import (
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

const (
	SchemaVersion       = 1
	OAuthCredentialKind = "mcp_oauth"
)

type Catalog struct {
	SchemaVersion int    `json:"schema_version"`
	UpdatedAt     string `json:"updated_at"`
	Items         []Item `json:"items"`
}

type Item struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Publisher      Publisher      `json:"publisher"`
	IconURL        string         `json:"icon_url,omitempty"`
	HomepageURL    string         `json:"homepage_url,omitempty"`
	RepositoryURL  string         `json:"repository_url,omitempty"`
	Verified       bool           `json:"verified"`
	Categories     []string       `json:"categories"`
	FeaturedRank   int            `json:"featured_rank"`
	Version        string         `json:"version"`
	Transport      string         `json:"transport"`
	Authentication Authentication `json:"authentication,omitempty"`
	Server         Server         `json:"server"`
}

type Authentication struct {
	Type string `json:"type,omitempty"`
}

func (a Authentication) EffectiveType() string {
	if a.Type == "" {
		return "none"
	}
	return a.Type
}

type Publisher struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Server struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func (i Item) CanonicalSpec() canonical.Spec {
	server := canonical.MCPServer{
		Name:      i.Server.Name,
		Transport: i.Transport,
		URL:       i.Server.URL,
	}
	if i.Authentication.EffectiveType() == "oauth2" {
		server.Headers = map[string]canonical.EnvValue{
			"Authorization": {
				Mode:               canonical.EnvModeCredentialRef,
				Prefix:             "Bearer ",
				CredentialKindCode: OAuthCredentialKind,
			},
		}
	}
	return canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP:           &canonical.MCPSpec{Servers: []canonical.MCPServer{server}},
	}
}
