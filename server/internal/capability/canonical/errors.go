package canonical

import "errors"

// Sentinel errors returned by parse / validate paths. All errors from this
// package wrap one of these so callers can use errors.Is for control flow.
var (
	ErrInvalidSpec         = errors.New("canonical: invalid spec")
	ErrInvalidMCP          = errors.New("canonical: invalid mcp spec")
	ErrInvalidSkill        = errors.New("canonical: invalid skill spec")
	ErrInvalidPlugin       = errors.New("canonical: invalid plugin spec")
	ErrInvalidSystemPrompt = errors.New("canonical: invalid system_prompt spec")
	ErrInvalidEnvValue     = errors.New("canonical: invalid env value")
)
