package agentdaemon

import (
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func openCodeEndpointBaseURL(mr store.ModelRuntime) string {
	var endpointType string
	switch strings.TrimSpace(mr.Adapter) {
	case "@ai-sdk/anthropic":
		return modelEndpointBaseURL(mr, "anthropic")
	case "@ai-sdk/openai-compatible":
		endpointType = "openai"
	case "@ai-sdk/openai":
		endpointType = "openai-response"
	default:
		return mr.BaseURL
	}
	if base, ok := configuredModelEndpointBaseURL(mr, endpointType); ok {
		return base
	}
	return mr.BaseURL
}

func configuredModelEndpointBaseURL(mr store.ModelRuntime, endpointType string) (string, bool) {
	want := normalizeModelEndpointType(endpointType)
	switch raw := mr.ProviderConfig["endpoint_base_urls"].(type) {
	case map[string]any:
		for k, v := range raw {
			if normalizeModelEndpointType(k) != want {
				continue
			}
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return inferModelEndpointBaseURL(want, s), true
			}
		}
	case map[string]string:
		for k, s := range raw {
			if normalizeModelEndpointType(k) == want && strings.TrimSpace(s) != "" {
				return inferModelEndpointBaseURL(want, s), true
			}
		}
	}
	return "", false
}
