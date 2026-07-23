package mcpoauth

import (
	"fmt"
	"strings"
	"time"
)

const CredentialProvider = "mcp_oauth"

const (
	VerificationVerified          = "verified"
	VerificationReconnectRequired = "reconnect_required"
	VerificationUnavailable       = "unavailable"
)

type Verification struct {
	Status          string
	CheckedAt       time.Time
	ErrorCode       string
	ProtocolVersion string
	ServerName      string
	ServerVersion   string
	ToolCount       int
}

func (c Credential) Payload() map[string]any {
	payload := map[string]any{
		"provider":                   CredentialProvider,
		"access_token":               c.AccessToken,
		"refresh_token":              c.RefreshToken,
		"token_type":                 c.TokenType,
		"scope":                      c.Scope,
		"client_id":                  c.ClientID,
		"client_secret":              c.ClientSecret,
		"token_endpoint_auth_method": c.TokenEndpointAuthMethod,
		"token_endpoint":             c.TokenEndpoint,
		"resource":                   c.Resource,
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
		TokenType:               stringValue(payload, "token_type"),
		Scope:                   stringValue(payload, "scope"),
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

func ApplyVerification(payload map[string]any, verification Verification) {
	payload["connection_status"] = verification.Status
	payload["connection_error"] = verification.ErrorCode
	payload["connection_protocol_version"] = verification.ProtocolVersion
	payload["connection_server_name"] = verification.ServerName
	payload["connection_server_version"] = verification.ServerVersion
	payload["connection_tool_count"] = verification.ToolCount
	if verification.CheckedAt.IsZero() {
		delete(payload, "connection_checked_at")
	} else {
		payload["connection_checked_at"] = verification.CheckedAt.UTC().Format(time.RFC3339)
	}
}

func VerificationFromPayload(payload map[string]any) Verification {
	verification := Verification{
		Status:          stringValue(payload, "connection_status"),
		ErrorCode:       stringValue(payload, "connection_error"),
		ProtocolVersion: stringValue(payload, "connection_protocol_version"),
		ServerName:      stringValue(payload, "connection_server_name"),
		ServerVersion:   stringValue(payload, "connection_server_version"),
	}
	if raw := stringValue(payload, "connection_checked_at"); raw != "" {
		verification.CheckedAt, _ = time.Parse(time.RFC3339, raw)
	}
	switch value := payload["connection_tool_count"].(type) {
	case int:
		verification.ToolCount = value
	case float64:
		verification.ToolCount = int(value)
	}
	return verification
}

// PreserveMetadata keeps non-token connector metadata when a refresh-token
// rotation replaces the OAuth payload.
func PreserveMetadata(source, target map[string]any) {
	for _, key := range []string{
		"catalog_id",
		"connection_status",
		"connection_error",
		"connection_protocol_version",
		"connection_server_name",
		"connection_server_version",
		"connection_tool_count",
		"connection_checked_at",
	} {
		if value, ok := source[key]; ok {
			target[key] = value
		}
	}
}

func stringValue(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}
