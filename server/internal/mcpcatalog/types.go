package mcpcatalog

import "github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"

const SchemaVersion = 1

const (
	ClientRegistrationDynamic        = "dynamic"
	ClientRegistrationApprovedClient = "approved-client"
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
	Type               string `json:"type,omitempty"`
	CredentialKind     string `json:"credential_kind,omitempty"`
	ClientRegistration string `json:"client_registration,omitempty"`
}

func (a Authentication) EffectiveType() string {
	if a.Type == "" {
		return "none"
	}
	return a.Type
}

func (a Authentication) EffectiveClientRegistration() string {
	if a.ClientRegistration == "" {
		return ClientRegistrationDynamic
	}
	return a.ClientRegistration
}

func (a Authentication) ConnectionSupported() bool {
	return a.EffectiveType() != "oauth2" || a.EffectiveClientRegistration() == ClientRegistrationDynamic
}

type Publisher struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Server struct {
	Name              string            `json:"name"`
	URL               string            `json:"url,omitempty"`
	Command           string            `json:"command,omitempty"`
	Args              []string          `json:"args,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	StartupTimeoutSec int               `json:"startup_timeout_sec,omitempty"`
}

func (i Item) CanonicalSpec() canonical.Spec {
	env := make(map[string]canonical.EnvValue, len(i.Server.Env))
	for name := range i.Server.Env {
		env[name] = canonical.EnvValue{Mode: canonical.EnvModeLiteral}
	}
	server := canonical.MCPServer{
		Name:              i.Server.Name,
		Transport:         i.Transport,
		URL:               i.Server.URL,
		Command:           i.Server.Command,
		Args:              append([]string(nil), i.Server.Args...),
		Env:               env,
		StartupTimeoutSec: i.Server.StartupTimeoutSec,
	}
	if i.Authentication.EffectiveType() == "oauth2" {
		server.Headers = map[string]canonical.EnvValue{
			"Authorization": {
				Mode:               canonical.EnvModeCredentialRef,
				Prefix:             "Bearer ",
				CredentialKindCode: i.Authentication.CredentialKind,
			},
		}
	}
	return canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP:           &canonical.MCPSpec{Servers: []canonical.MCPServer{server}},
	}
}
