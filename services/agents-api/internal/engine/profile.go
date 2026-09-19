package engine

import (
	"errors"
	"slices"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

var ErrInvalidInput = errors.New("invalid engine configuration")

// Profile records qualified public behavior, independently of Runtime advertisements.
type Profile struct {
	Placements                                 []string
	WebSearchControl, TextVerbosity, MCPBearer bool
	ValidateConfiguration                      func(agent v1.Agent, environment *v1.Environment, hasDaemon bool) error
	ValidateTools                              func(environment *v1.Environment, hasDaemon bool, functions []proto.FunctionTool, mcp []proto.MCPHTTPServer) error
	ValidateFunctionResult                     func([]proto.FunctionResultContent) error
}

func (p Profile) Accepts(placement string) bool {
	return slices.Contains(p.Placements, placement)
}

// Catalog is immutable after construction. Its zero value selects built-in profiles.
// NewCatalog with an empty map explicitly qualifies no engines.
type Catalog struct {
	profiles map[string]Profile
}

func NewCatalog(profiles map[string]Profile) Catalog {
	c := Catalog{profiles: make(map[string]Profile, len(profiles))}
	for kind, profile := range profiles {
		profile.Placements = slices.Clone(profile.Placements)
		c.profiles[kind] = profile
	}
	return c
}

func (c Catalog) Lookup(kind string) (Profile, bool) {
	if c.profiles == nil {
		c = qualified
	}
	profile, ok := c.profiles[kind]
	profile.Placements = slices.Clone(profile.Placements)
	return profile, ok
}

var qualified = NewCatalog(map[string]Profile{
	"codex":      codexProfile(),
	"claude_sdk": claudeProfile(),
	"mcode":      mcodeProfile(),
})
