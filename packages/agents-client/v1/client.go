// Package v1 configures the official Go SDK for Parsar's independent Agents API.
package v1

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

type Config struct {
	// BaseURL includes the API prefix, for example https://agents.example/v1.
	BaseURL string
	APIKey  string
	// HTTPClient optionally supplies a trusted transport and timeout. The client
	// is copied; its cookie jar and redirect policy are not used.
	HTTPClient *http.Client
}

// New returns the official Session service. It does not load OpenAI environment
// credentials or switch Parsar's execution path. Callers use SDK types, pagination
// and *openai.Error directly, and supply Idempotency-Key for creation retries.
// SDK retries are disabled; an uncertain write can be retried with the same key.
func New(cfg Config) (openai.BetaAgentSessionService, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return openai.BetaAgentSessionService{}, errors.New("agents API base URL must be an absolute HTTP(S) URL without credentials, query or fragment")
	}
	if strings.TrimSpace(cfg.APIKey) == "" || strings.ContainsAny(cfg.APIKey, " \t\r\n") {
		return openai.BetaAgentSessionService{}, errors.New("an explicit agents API key without whitespace is required")
	}
	h := http.Client{Timeout: 30 * time.Second}
	if cfg.HTTPClient != nil {
		h = *cfg.HTTPClient
	}
	// Product cookies and redirected credentials must not cross this boundary.
	h.Jar = nil
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("agents API redirects are disabled") }
	return openai.NewBetaAgentSessionService(
		option.WithBaseURL(strings.TrimRight(u.String(), "/")+"/"),
		option.WithAPIKey(cfg.APIKey),
		option.WithHTTPClient(&h),
		option.WithMaxRetries(0),
	), nil
}
