package codex

import (
	"maps"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func executionOptions(req proto.PromptRequestPayload) map[string]any {
	if req.ExecutionControls == nil && req.MCPHTTPServers == nil {
		return req.AgentOptions
	}
	options := maps.Clone(req.AgentOptions)
	if options == nil {
		options = make(map[string]any)
	}
	if req.ExecutionControls != nil {
		// Reuse native validation/catalog handling, overriding lower-priority operator options.
		options["web_search"] = req.ExecutionControls.WebSearch
		options["model_verbosity"] = req.ExecutionControls.TextVerbosity
	}
	if req.MCPHTTPServers != nil {
		delete(options, "mcp_servers")
	}
	return options
}
