// Package feishu implements the OAuth 2.0 / OIDC client for the
// Feishu (Lark) authen endpoint.
//
// Feishu's flow has FOUR legs the connector cares about:
//
//  1. Authorize URL — browser is redirected to
//     https://accounts.feishu.cn/open-apis/authen/v1/authorize.
//
//  2. App access token — server-to-server credential obtained from
//     POST /open-apis/auth/v3/app_access_token/internal. Cached
//     in-process until ~30s before the upstream-reported expiry.
//
//  3. Token exchange — POST /open-apis/authen/v1/oidc/access_token
//     with body {grant_type, code} AND Authorization: Bearer
//     <app_access_token>. The endpoint does NOT accept
//     client_id / client_secret / redirect_uri in the body; sending
//     a generic OAuth body with no Bearer header gets code=20014
//     with an empty msg.
//
//  4. User info — GET /open-apis/authen/v1/user_info with Bearer
//     user_access_token; returns union_id + open_id + email + name.
//
// PARSAR_FEISHU_MOCK=true short-circuits the entire flow so
// `make dev` and tests drive the callback without real credentials.
package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	DefaultAuthorizeBase = "https://accounts.feishu.cn"
	DefaultAPIBase       = "https://open.feishu.cn"

	// DefaultScope requires `contact:user.email:readonly` to be
	// approved in the Feishu app settings; without it user_info
	// returns email="".
	DefaultScope = "contact:user.id:readonly contact:user.base:readonly contact:user.email:readonly"

	authorizePath = "/open-apis/authen/v1/authorize"
	appTokenPath  = "/open-apis/auth/v3/app_access_token/internal"
	tokenPath     = "/open-apis/authen/v1/oidc/access_token"
	userInfoPath  = "/open-apis/authen/v1/user_info"

	// appTokenSafetyWindow is subtracted from the upstream-reported
	// expires_in so we refresh before actual expiry (clock skew +
	// request latency).
	appTokenSafetyWindow = 30 * time.Second
)

const (
	EnvAppID             = "PARSAR_FEISHU_APP_ID"
	EnvAppSecret         = "PARSAR_FEISHU_APP_SECRET"
	EnvRedirectURI       = "PARSAR_FEISHU_REDIRECT_URI"
	EnvScope             = "PARSAR_FEISHU_SCOPE"
	EnvAuthorizeBase     = "PARSAR_FEISHU_AUTHORIZE_BASE"
	EnvAPIBase           = "PARSAR_FEISHU_API_BASE"
	EnvMock              = "PARSAR_FEISHU_MOCK"
	EnvVerificationToken = "PARSAR_FEISHU_VERIFICATION_TOKEN"
	EnvEncryptKey        = "PARSAR_FEISHU_ENCRYPT_KEY"
)

// IsWebhookConfigured returns true when the event callback can
// authenticate incoming events. Encrypt key is optional — only
// needed when the app enables event encryption.
func IsWebhookConfigured(env func(string) string) bool {
	if env == nil {
		env = os.Getenv
	}
	return strings.TrimSpace(env(EnvVerificationToken)) != ""
}

var ErrNotConfigured = errors.New("feishu: OIDC not configured (set PARSAR_FEISHU_APP_ID / APP_SECRET / REDIRECT_URI)")

type Client interface {
	AuthorizeURL(state string) string
	ExchangeCode(ctx context.Context, code string) (UserProfile, error)
	IsMock() bool
}

// UserProfile is the projection persisted to users + auth_identities.
type UserProfile struct {
	// UnionID is the stable cross-tenant identifier; stored as
	// auth_identities.subject so the same user across multiple
	// Feishu tenants resolves to one Parsar user.
	UnionID string

	// OpenID is the per-tenant identifier; kept in metadata for
	// audit lookups.
	OpenID string

	// Email is REQUIRED — the users table keys on it. Empty means
	// the Feishu app lacks the `contact:user.email:readonly`
	// scope; the handler MUST surface a friendly error.
	Email string

	Name string

	AvatarURL string
}

func IsConfigured(env func(string) string) bool {
	if env == nil {
		env = os.Getenv
	}
	return strings.TrimSpace(env(EnvAppID)) != "" &&
		strings.TrimSpace(env(EnvAppSecret)) != "" &&
		strings.TrimSpace(env(EnvRedirectURI)) != ""
}

func IsMockEnabled(env func(string) string) bool {
	if env == nil {
		env = os.Getenv
	}
	switch strings.ToLower(strings.TrimSpace(env(EnvMock))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// NewClientFromEnv returns a mock client when EnvMock is set, the
// real client otherwise. ErrNotConfigured when mock is off and any
// required env var is missing.
func NewClientFromEnv(env func(string) string) (Client, error) {
	if env == nil {
		env = os.Getenv
	}
	if IsMockEnabled(env) {
		return NewMockClient(env), nil
	}
	if !IsConfigured(env) {
		return nil, ErrNotConfigured
	}
	return &httpClient{
		appID:         strings.TrimSpace(env(EnvAppID)),
		appSecret:     strings.TrimSpace(env(EnvAppSecret)),
		redirectURI:   strings.TrimSpace(env(EnvRedirectURI)),
		scope:         coalesce(strings.TrimSpace(env(EnvScope)), DefaultScope),
		authorizeBase: coalesce(strings.TrimSpace(env(EnvAuthorizeBase)), DefaultAuthorizeBase),
		apiBase:       coalesce(strings.TrimSpace(env(EnvAPIBase)), DefaultAPIBase),
		http:          &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func coalesce(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

type httpClient struct {
	appID         string
	appSecret     string
	redirectURI   string
	scope         string
	authorizeBase string
	apiBase       string
	http          *http.Client

	// app_access_token cache. Mutex serialises concurrent refreshes
	// during a login burst.
	tokenMu         sync.Mutex
	appToken        string
	appTokenExpires time.Time
}

func (c *httpClient) IsMock() bool { return false }

// AuthorizeURL composes the front-channel URL. The caller MUST
// generate `state` as a CSRF nonce stored in a short-lived cookie;
// the callback verifies the cookie matches the URL state echoed back.
func (c *httpClient) AuthorizeURL(state string) string {
	q := url.Values{}
	q.Set("app_id", c.appID)
	q.Set("redirect_uri", c.redirectURI)
	q.Set("scope", c.scope)
	q.Set("state", state)
	q.Set("response_type", "code")
	return c.authorizeBase + authorizePath + "?" + q.Encode()
}

// ExchangeCode runs the token + user_info legs back-to-back.
func (c *httpClient) ExchangeCode(ctx context.Context, code string) (UserProfile, error) {
	token, err := c.exchangeAccessToken(ctx, code)
	if err != nil {
		return UserProfile{}, fmt.Errorf("feishu: exchange code: %w", err)
	}
	profile, err := c.fetchUserInfo(ctx, token)
	if err != nil {
		return UserProfile{}, fmt.Errorf("feishu: fetch user_info: %w", err)
	}
	if strings.TrimSpace(profile.Email) == "" {
		return UserProfile{}, fmt.Errorf("feishu: user_info returned empty email — the Feishu app needs the `contact:user.email:readonly` scope approved")
	}
	if strings.TrimSpace(profile.UnionID) == "" {
		return UserProfile{}, fmt.Errorf("feishu: user_info returned empty union_id — the Feishu app needs the `contact:user.id:readonly` scope approved")
	}
	return profile, nil
}

// appAccessTokenResponse — note the envelope is flat (no `data.`
// wrapper) unlike most authen v1 endpoints.
type appAccessTokenResponse struct {
	Code           int    `json:"code"`
	Msg            string `json:"msg"`
	AppAccessToken string `json:"app_access_token"`
	Expire         int    `json:"expire"` // seconds until expiry, typically 7200
}

// appAccessToken returns a valid app_access_token, refreshing the
// cache when missing or near expiry. Safe for concurrent callers.
func (c *httpClient) appAccessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()
	if c.appToken != "" && time.Now().Before(c.appTokenExpires) {
		return c.appToken, nil
	}
	body, err := json.Marshal(map[string]string{
		"app_id":     c.appID,
		"app_secret": c.appSecret,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+appTokenPath, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("feishu: app_access_token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("feishu: app_access_token endpoint returned %d", resp.StatusCode)
	}
	var parsed appAccessTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("feishu: decode app_access_token response: %w", err)
	}
	if parsed.Code != 0 {
		// Common: code=10003 wrong app_secret, code=99991663 app
		// disabled. Surface raw fields for diagnosis.
		return "", fmt.Errorf("feishu: app_access_token returned code=%d msg=%q", parsed.Code, parsed.Msg)
	}
	if parsed.AppAccessToken == "" {
		return "", fmt.Errorf("feishu: app_access_token response missing app_access_token field")
	}
	ttl := time.Duration(parsed.Expire) * time.Second
	if ttl <= appTokenSafetyWindow {
		// Defensive: don't underflow the cache TTL if upstream
		// returns an oddly small expire.
		ttl = appTokenSafetyWindow
	}
	c.appToken = parsed.AppAccessToken
	c.appTokenExpires = time.Now().Add(ttl - appTokenSafetyWindow)
	return c.appToken, nil
}

type accessTokenResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	} `json:"data"`
}

// exchangeAccessToken trades the authorization code for a
// user_access_token. Per Feishu spec the OIDC token endpoint
// requires Authorization: Bearer <app_access_token> and ONLY accepts
// {grant_type, code} in the body. Sending client_id / client_secret
// / redirect_uri (as generic OAuth 2.0 clients do) is ignored, and
// a missing Bearer causes code=20014 with empty msg.
func (c *httpClient) exchangeAccessToken(ctx context.Context, code string) (string, error) {
	appTok, err := c.appAccessToken(ctx)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]string{
		"grant_type": "authorization_code",
		"code":       code,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiBase+tokenPath, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+appTok)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("feishu: access_token endpoint returned %d", resp.StatusCode)
	}
	var parsed accessTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if parsed.Code != 0 {
		return "", fmt.Errorf("feishu: access_token returned code=%d msg=%q", parsed.Code, parsed.Msg)
	}
	if parsed.Data.AccessToken == "" {
		return "", fmt.Errorf("feishu: access_token response missing data.access_token")
	}
	return parsed.Data.AccessToken, nil
}

// userInfoResponse mirrors the Feishu authen v1 user_info shape.
// See: https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/authentication-management/login-state-management/get
type userInfoResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Name      string `json:"name"`
		EnName    string `json:"en_name"`
		AvatarURL string `json:"avatar_url"`
		OpenID    string `json:"open_id"`
		UnionID   string `json:"union_id"`
		Email     string `json:"email"`
	} `json:"data"`
}

func (c *httpClient) fetchUserInfo(ctx context.Context, accessToken string) (UserProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+userInfoPath, nil)
	if err != nil {
		return UserProfile{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return UserProfile{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return UserProfile{}, fmt.Errorf("feishu: user_info endpoint returned %d", resp.StatusCode)
	}
	var parsed userInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return UserProfile{}, err
	}
	if parsed.Code != 0 {
		return UserProfile{}, fmt.Errorf("feishu: user_info returned code=%d msg=%q", parsed.Code, parsed.Msg)
	}
	return UserProfile{
		UnionID:   parsed.Data.UnionID,
		OpenID:    parsed.Data.OpenID,
		Email:     parsed.Data.Email,
		Name:      parsed.Data.Name,
		AvatarURL: parsed.Data.AvatarURL,
	}, nil
}
