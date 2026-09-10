package store

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// HTTPAgentConfig keeps endpoint authentication separate from the public request body.
type HTTPAgentConfig struct {
	Endpoint string `json:"endpoint"`
	SecretID string `json:"secret_id,omitempty"`
}

func HTTPAgentConfigFrom(config map[string]any) HTTPAgentConfig {
	value := func(key string) string {
		if s, ok := config[key].(string); ok {
			return strings.TrimSpace(s)
		}
		if nested, ok := config["http"].(map[string]any); ok {
			if s, ok := nested[key].(string); ok {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}
	return HTTPAgentConfig{Endpoint: value("endpoint"), SecretID: value("secret_id")}
}

func ValidHTTPAgentEndpoint(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	return err == nil && parsed.Hostname() != "" && parsed.User == nil && parsed.Fragment == "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func foldHTTPAgentConfig(dst, src map[string]any) error {
	// Canonicalize both the original flat protocol and the nested HTTP form.
	current := HTTPAgentConfigFrom(dst)
	if nested, ok := src["http"]; ok {
		if nested == nil {
			current = HTTPAgentConfig{}
		} else if m, ok := nested.(map[string]any); ok {
			for key, target := range map[string]*string{"endpoint": &current.Endpoint, "secret_id": &current.SecretID} {
				if v, exists := m[key]; exists {
					s, ok := v.(string)
					if !ok {
						return fmt.Errorf("%w: HTTP %s must be a string", ErrInvalidInput, key)
					}
					*target = strings.TrimSpace(s)
				}
			}
		} else {
			return fmt.Errorf("%w: http must be an object", ErrInvalidInput)
		}
	}
	for key, target := range map[string]*string{"endpoint": &current.Endpoint, "secret_id": &current.SecretID} {
		if v, exists := src[key]; exists {
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("%w: HTTP %s must be a string", ErrInvalidInput, key)
			}
			*target = strings.TrimSpace(s)
		}
	}
	if current.Endpoint != "" && !ValidHTTPAgentEndpoint(current.Endpoint) {
		return fmt.Errorf("%w: HTTP endpoint must be an http(s) URL without credentials or fragment", ErrInvalidInput)
	}
	delete(dst, "endpoint")
	delete(dst, "secret_id")
	dst["http"] = map[string]any{"endpoint": current.Endpoint, "secret_id": current.SecretID}
	return nil
}

// GetHTTPAgentSecretPayload intentionally rejects globally readable model secrets:
// an arbitrary HTTP endpoint may use only its own workspace's HTTP bearer secret.
func (s *Store) GetHTTPAgentSecretPayload(ctx context.Context, workspaceID, secretID string) (SecretPayload, error) {
	secret, err := s.GetSecretPayload(ctx, workspaceID, secretID)
	if err != nil || secret.ManagementWorkspaceID != workspaceID || secret.Kind != "http_agent" || secret.Provider != "http_agent" || secret.AuthType != "bearer" || secret.Status != "active" {
		return SecretPayload{}, fmt.Errorf("%w: select an active HTTP Agent bearer credential managed by this workspace", ErrInvalidInput)
	}
	return secret, nil
}

func (s *Store) validateHTTPAgentSecret(ctx context.Context, workspaceID string, config map[string]any) error {
	httpConfig := HTTPAgentConfigFrom(config)
	if httpConfig.SecretID == "" {
		return nil
	}
	if !ValidHTTPAgentEndpoint(httpConfig.Endpoint) {
		return fmt.Errorf("%w: HTTP endpoint is required", ErrInvalidInput)
	}
	_, err := s.GetHTTPAgentSecretPayload(ctx, workspaceID, httpConfig.SecretID)
	return err
}
