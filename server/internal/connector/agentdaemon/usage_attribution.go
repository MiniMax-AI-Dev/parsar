package agentdaemon

import (
	"maps"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type usageAttribution struct {
	agentKind     string
	modelProvider string
}

// Attribute to the launched model, not a later catalog edit or CLI provider alias.
func (a usageAttribution) fromProto(u proto.Usage) store.UsageInput {
	result := store.UsageInput{
		Provider: u.Provider, Model: u.Model,
		InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
		CostUSD: u.CostUSD, Raw: u.Raw,
	}
	// A completion without reported usage must not create a usage record.
	if u.Provider == "" && u.Model == "" && u.InputTokens == 0 && u.OutputTokens == 0 && u.CostUSD == 0 && len(u.Raw) == 0 {
		return result
	}
	source := "adapter"
	if a.modelProvider != "" {
		result.Provider = a.modelProvider
		source = "managed_model"
	}
	result.Raw = maps.Clone(u.Raw)
	if result.Raw == nil {
		result.Raw = make(map[string]any)
	}
	result.Raw["parsar_usage"] = map[string]any{
		"agent_kind":        a.agentKind,
		"reported_provider": u.Provider,
		"provider_source":   source,
	}
	return result
}
