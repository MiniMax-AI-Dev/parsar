package feishu

import (
	"context"
	"os"
	"strings"
)

// MockClient is a deterministic stand-in for the real Feishu OIDC
// upstream. Used by `make dev` (PARSAR_FEISHU_MOCK=true) and tests
// that want to drive the callback without real credentials. Its
// AuthorizeURL short-circuits straight to the callback so the
// browser never leaves localhost.
type MockClient struct {
	redirectURI string
	profile     UserProfile
}

const (
	EnvMockEmail   = "PARSAR_FEISHU_MOCK_EMAIL"
	EnvMockName    = "PARSAR_FEISHU_MOCK_NAME"
	EnvMockUnionID = "PARSAR_FEISHU_MOCK_UNION_ID"
	EnvMockOpenID  = "PARSAR_FEISHU_MOCK_OPEN_ID"
)

// NewMockClient defaults to the dev seed admin so callback resolves
// to the user the seed already wires into workspace_members.
func NewMockClient(env func(string) string) *MockClient {
	if env == nil {
		env = os.Getenv
	}
	return &MockClient{
		redirectURI: strings.TrimSpace(env(EnvRedirectURI)),
		profile: UserProfile{
			UnionID:   coalesceEnv(env, EnvMockUnionID, "on_mock_union_admin"),
			OpenID:    coalesceEnv(env, EnvMockOpenID, "ou_feishu_admin"),
			Email:     coalesceEnv(env, EnvMockEmail, "admin@example.com"),
			Name:      coalesceEnv(env, EnvMockName, "Dev Admin"),
			AvatarURL: "",
		},
	}
}

func coalesceEnv(env func(string) string, key, fallback string) string {
	if v := strings.TrimSpace(env(key)); v != "" {
		return v
	}
	return fallback
}

func (m *MockClient) IsMock() bool { return true }

// AuthorizeURL short-circuits straight to the callback with the
// canonical mock code. State is echoed so the CSRF cookie check
// still works in dev.
func (m *MockClient) AuthorizeURL(state string) string {
	redirect := m.redirectURI
	if redirect == "" {
		redirect = "/api/v1/auth/feishu/callback"
	}
	sep := "?"
	if strings.Contains(redirect, "?") {
		sep = "&"
	}
	return redirect + sep + "code=mock-code&state=" + state
}

// ExchangeCode ignores the code and returns the configured fixture.
// "mock-code" is the documented value for operators driving the mock
// by hand.
func (m *MockClient) ExchangeCode(_ context.Context, _ string) (UserProfile, error) {
	return m.profile, nil
}
