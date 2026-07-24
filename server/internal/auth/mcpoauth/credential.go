package mcpoauth

import (
	"fmt"
	"strings"
	"time"
)

const CredentialProvider = "mcp_oauth"

func (c Credential) Payload() map[string]any {
	payload := map[string]any{
		"provider":                   CredentialProvider,
		"access_token":               c.AccessToken,
		"client_id":                  c.ClientID,
		"token_endpoint_auth_method": c.TokenEndpointAuthMethod,
		"token_endpoint":             c.TokenEndpoint,
		"resource":                   c.Resource,
	}
	if c.RefreshToken != "" {
		payload["refresh_token"] = c.RefreshToken
	}
	if c.ClientSecret != "" {
		payload["client_secret"] = c.ClientSecret
	}
	if !c.ExpiresAt.IsZero() {
		payload["expires_at"] = c.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return payload
}

func CredentialFromPayload(payload map[string]any) (Credential, bool, error) {
	if stringValue(payload, "provider") != CredentialProvider {
		return Credential{}, false, nil
	}
	credential := Credential{
		AccessToken:             stringValue(payload, "access_token"),
		RefreshToken:            stringValue(payload, "refresh_token"),
		ClientID:                stringValue(payload, "client_id"),
		ClientSecret:            stringValue(payload, "client_secret"),
		TokenEndpointAuthMethod: stringValue(payload, "token_endpoint_auth_method"),
		TokenEndpoint:           stringValue(payload, "token_endpoint"),
		Resource:                stringValue(payload, "resource"),
	}
	if raw := stringValue(payload, "expires_at"); raw != "" {
		expiresAt, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return Credential{}, true, fmt.Errorf("mcp oauth: parse expires_at: %w", err)
		}
		credential.ExpiresAt = expiresAt
	}
	if credential.AccessToken == "" || credential.ClientID == "" || credential.TokenEndpoint == "" || credential.Resource == "" {
		return Credential{}, true, fmt.Errorf("mcp oauth: stored credential is incomplete")
	}
	return credential, true, nil
}

func (c Credential) NeedsRefresh(now time.Time) bool {
	return !c.ExpiresAt.IsZero() && !c.ExpiresAt.After(now.UTC().Add(time.Minute))
}

func stringValue(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}
