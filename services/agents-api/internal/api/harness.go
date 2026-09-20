package api

import (
	"encoding/json"
	"fmt"
)

// WithHarnesses enables explicit selections qualified by this deployment.
func WithHarnesses(kinds []string) Option {
	return func(h *Handler) {
		h.harnesses = make(map[string]bool, len(kinds))
		for _, kind := range kinds {
			h.harnesses[kind] = true
		}
	}
}

func (h *Handler) sessionHarness(raw json.RawMessage) (string, error) {
	var cfg configuration
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", err
	}
	if cfg.Agent.XAgentsCore == nil {
		return h.engine, nil
	}
	kind := cfg.Agent.XAgentsCore.Harness
	if kind != h.engine && !h.harnesses[kind] {
		return "", fmt.Errorf("Harness %s is not enabled on this Core deployment.", kind)
	}
	return kind, nil
}
