// Package modelendpoint resolves model API endpoints shared by probes and runtimes.
package modelendpoint

import "strings"

// AnthropicMessagesURL accepts a service root, versioned base, or messages endpoint.
func AnthropicMessagesURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch {
	case strings.HasSuffix(base, "/v1/messages"), strings.HasSuffix(base, "/messages"):
		return base
	case strings.HasSuffix(base, "/v1"):
		return base + "/messages"
	default:
		return base + "/v1/messages"
	}
}
