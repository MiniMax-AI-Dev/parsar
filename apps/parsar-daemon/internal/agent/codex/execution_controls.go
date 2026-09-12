package codex

import (
	"maps"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func executionOptions(req proto.PromptRequestPayload) map[string]any {
	if req.ExecutionControls == nil {
		return req.AgentOptions
	}
	options := maps.Clone(req.AgentOptions)
	if options == nil {
		options = make(map[string]any)
	}
	// Reuse native validation/catalog handling, overriding lower-priority operator options.
	options["web_search"] = req.ExecutionControls.WebSearch
	options["model_verbosity"] = req.ExecutionControls.TextVerbosity
	return options
}
