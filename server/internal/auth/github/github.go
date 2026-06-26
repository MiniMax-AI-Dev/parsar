// Package github implements the personal GitHub OAuth App flow.
// Account-scoped (not a GitHub App installation): the user
// authorizes once and Parsar stores the OAuth access token as the
// user's github_pat credential.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	DefaultAuthorizeBase = "https://github.com"
	DefaultAPIBase       = "https://api.github.com"
	DefaultScope         = "repo read:user user:email"

	authorizePath = "/login/oauth/authorize"
	tokenPath     = "/login/oauth/access_token"
	userPath      = "/user"
)

const (
	EnvClientID      = "PARSAR_GITHUB_CLIENT_ID"
	EnvClientSecret  = "PARSAR_GITHUB_CLIENT_SECRET"
	EnvRedirectURI   = "PARSAR_GITHUB_REDIRECT_URI"
	EnvScope         = "PARSAR_GITHUB_SCOPE"
	EnvAuthorizeBase = "PARSAR_GITHUB_AUTHORIZE_BASE"
	EnvAPIBase       = "PARSAR_GITHUB_API_BASE"
)

var ErrNotConfigured = errors.New("github: OAuth not configured (set PARSAR_GITHUB_CLIENT_ID / CLIENT_SECRET / REDIRECT_URI)")

type Client interface {
	AuthorizeURL(state string) string
	ExchangeCode(ctx context.Context, code string) (Credential, error)
}

type Credential struct {
	AccessToken string
	TokenType   string
	Scope       string
	Login       string
	UserID      int64
	Name        string
	AvatarURL   string
}

type httpClient struct {
	clientID      string
	clientSecret  string
	redirectURI   string
	scope         string
	authorizeBase string
	apiBase       string
	http          *http.Client
}

func IsConfigured(env func(string) string) bool {
	if env == nil {
		env = os.Getenv
	}
	return strings.TrimSpace(env(EnvClientID)) != "" &&
		strings.TrimSpace(env(EnvClientSecret)) != "" &&
		strings.TrimSpace(env(EnvRedirectURI)) != ""
}

func NewClientFromEnv(env func(string) string) (Client, error) {
	if env == nil {
		env = os.Getenv
	}
	if !IsConfigured(env) {
		return nil, ErrNotConfigured
	}
	return &httpClient{
		clientID:      strings.TrimSpace(env(EnvClientID)),
		clientSecret:  strings.TrimSpace(env(EnvClientSecret)),
		redirectURI:   strings.TrimSpace(env(EnvRedirectURI)),
		scope:         coalesce(strings.TrimSpace(env(EnvScope)), DefaultScope),
		authorizeBase: strings.TrimRight(coalesce(strings.TrimSpace(env(EnvAuthorizeBase)), DefaultAuthorizeBase), "/"),
		apiBase:       strings.TrimRight(coalesce(strings.TrimSpace(env(EnvAPIBase)), DefaultAPIBase), "/"),
		http:          &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (c *httpClient) AuthorizeURL(state string) string {
	q := url.Values{}
	q.Set("client_id", c.clientID)
	q.Set("redirect_uri", c.redirectURI)
	q.Set("scope", c.scope)
	q.Set("state", state)
	return c.authorizeBase + authorizePath + "?" + q.Encode()
}

func (c *httpClient) ExchangeCode(ctx context.Context, code string) (Credential, error) {
	form := url.Values{}
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	form.Set("code", strings.TrimSpace(code))
	form.Set("redirect_uri", c.redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.authorizeBase+tokenPath, strings.NewReader(form.Encode()))
	if err != nil {
		return Credential{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return Credential{}, fmt.Errorf("github token exchange: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Credential{}, fmt.Errorf("github token exchange status %d", resp.StatusCode)
	}

	var token tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return Credential{}, fmt.Errorf("github token decode: %w", err)
	}
	if token.Error != "" {
		return Credential{}, fmt.Errorf("github token exchange failed: %s", token.ErrorDescription)
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return Credential{}, errors.New("github token exchange returned empty access_token")
	}

	profile, err := c.fetchUser(ctx, token.AccessToken)
	if err != nil {
		return Credential{}, err
	}
	return Credential{
		AccessToken: token.AccessToken,
		TokenType:   token.TokenType,
		Scope:       token.Scope,
		Login:       profile.Login,
		UserID:      profile.ID,
		Name:        profile.Name,
		AvatarURL:   profile.AvatarURL,
	}, nil
}

func (c *httpClient) fetchUser(ctx context.Context, accessToken string) (userResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+userPath, nil)
	if err != nil {
		return userResponse{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return userResponse{}, fmt.Errorf("github user fetch: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return userResponse{}, fmt.Errorf("github user fetch status %d", resp.StatusCode)
	}
	var profile userResponse
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		return userResponse{}, fmt.Errorf("github user decode: %w", err)
	}
	if profile.ID == 0 || strings.TrimSpace(profile.Login) == "" {
		return userResponse{}, errors.New("github user profile missing id or login")
	}
	return profile, nil
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type userResponse struct {
	Login     string `json:"login"`
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

func coalesce(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
