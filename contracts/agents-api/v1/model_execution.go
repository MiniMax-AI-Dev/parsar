package v1

import (
	"errors"
	"net/url"
	"strings"
)

// SessionExecutionInput is a write-only execution extension, not a provider resource.
type SessionExecutionInput struct {
	ModelProvider *ModelProviderInput `json:"model_provider"`
}

type ModelProviderInput struct {
	Protocol        string `json:"protocol" enums:"anthropic,responses"`
	BaseURL         string `json:"base_url"`
	APIKey          string `json:"api_key"`
	ContextWindow   int32  `json:"context_window,omitempty"`
	MaxOutputTokens int32  `json:"max_output_tokens,omitempty"`
}

func (p *ModelProviderInput) Validate() error {
	if p == nil {
		return errors.New("model_provider is required")
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(p.BaseURL, "\x00\r\n") {
		return errors.New("model provider requires an HTTPS base_url without credentials, query or fragment")
	}
	if p.Protocol != "anthropic" && p.Protocol != "responses" {
		return errors.New("unsupported model provider protocol")
	}
	if strings.TrimSpace(p.APIKey) == "" || len(p.APIKey) > 16384 || strings.ContainsAny(p.APIKey, "\x00\r\n") {
		return errors.New("invalid model provider API key")
	}
	if p.ContextWindow < 0 || p.MaxOutputTokens < 0 || (p.MaxOutputTokens > p.ContextWindow) {
		return errors.New("invalid model token limits")
	}
	return nil
}

func (p *ModelProviderInput) ValidateHarness(harness string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if err := ValidateModelProtocol(p.Protocol, harness); err != nil {
		return err
	}
	if harness == "mcode" && (p.ContextWindow == 0 || p.MaxOutputTokens == 0) {
		return errors.New("MiniMax Code requires model context_window and max_output_tokens")
	}
	return nil
}

func ValidateModelProtocol(protocol, harness string) error {
	if (harness == "codex" && protocol == "responses") || ((harness == "claude_sdk" || harness == "mcode") && protocol == "anthropic") {
		return nil
	}
	return errors.New("selected harness does not support this model provider protocol")
}
