package execution

import "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/engine"

// Policy supplies immutable service qualification to admission and dispatch.
// The zero value uses built-in profiles. Custom composition must supply the same
// policy to the HTTP handler and Dispatcher; Runtime claims never add profiles.
type Policy struct {
	Engines engine.Catalog
}
