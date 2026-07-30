package oidc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type Client interface {
	AuthorizeURL(ctx context.Context, state, nonce string) (string, error)
	ExchangeCode(ctx context.Context, code, nonce string) (UserProfile, error)
}

type UserProfile struct {
	Provider   string
	ProviderID string
	Subject    string
	Email      string
	Name       string
	AvatarURL  string
	Issuer     string
	Claims     map[string]any
	UserInfo   map[string]any
}

type httpClient struct {
	cfg ProviderConfig

	mu       sync.RWMutex
	provider *gooidc.Provider
}

func NewClient(cfg ProviderConfig) (Client, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return &httpClient{cfg: cfg}, nil
}

func (c *httpClient) AuthorizeURL(ctx context.Context, state, nonce string) (string, error) {
	provider, err := c.providerFor(ctx)
	if err != nil {
		return "", err
	}
	oauth2Config := c.oauth2Config(provider)
	return oauth2Config.AuthCodeURL(state, gooidc.Nonce(nonce)), nil
}

func (c *httpClient) ExchangeCode(ctx context.Context, code, nonce string) (UserProfile, error) {
	provider, err := c.providerFor(ctx)
	if err != nil {
		return UserProfile{}, err
	}
	oauth2Config := c.oauth2Config(provider)
	token, err := oauth2Config.Exchange(ctx, strings.TrimSpace(code))
	if err != nil {
		return UserProfile{}, fmt.Errorf("oidc: exchange code for provider %q: %w", c.cfg.ID, err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || strings.TrimSpace(rawIDToken) == "" {
		return UserProfile{}, errors.New("oidc: token response missing id_token")
	}

	verifier := provider.Verifier(&gooidc.Config{ClientID: c.cfg.ClientID})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return UserProfile{}, fmt.Errorf("oidc: verify id_token for provider %q: %w", c.cfg.ID, err)
	}
	if idToken.Nonce != nonce {
		return UserProfile{}, errors.New("oidc: nonce mismatch")
	}

	claims := map[string]any{}
	if err := idToken.Claims(&claims); err != nil {
		return UserProfile{}, fmt.Errorf("oidc: decode id_token claims: %w", err)
	}
	userInfo := map[string]any{}
	if token.AccessToken != "" && provider.UserInfoEndpoint() != "" {
		info, err := provider.UserInfo(ctx, oauth2.StaticTokenSource(token))
		if err == nil {
			if err := info.Claims(&userInfo); err != nil {
				return UserProfile{}, fmt.Errorf("oidc: decode userinfo claims: %w", err)
			}
			if info.Subject != "" && info.Subject != idToken.Subject {
				return UserProfile{}, errors.New("oidc: userinfo sub does not match id_token sub")
			}
		}
	}
	profile := profileFromClaims(c.cfg, idToken, claims, userInfo)
	if err := validateProfile(c.cfg, profile); err != nil {
		return UserProfile{}, err
	}
	return profile, nil
}

func (c *httpClient) providerFor(ctx context.Context) (*gooidc.Provider, error) {
	c.mu.RLock()
	provider := c.provider
	c.mu.RUnlock()
	if provider != nil {
		return provider, nil
	}
	provider, err := gooidc.NewProvider(ctx, c.cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc: discover provider %q: %w", c.cfg.ID, err)
	}
	c.mu.Lock()
	c.provider = provider
	c.mu.Unlock()
	return provider, nil
}

func (c *httpClient) oauth2Config(provider *gooidc.Provider) oauth2.Config {
	return oauth2.Config{
		ClientID:     c.cfg.ClientID,
		ClientSecret: c.cfg.ClientSecret,
		RedirectURL:  c.cfg.RedirectURI,
		Endpoint:     provider.Endpoint(),
		Scopes:       c.cfg.Scopes,
	}
}

func profileFromClaims(cfg ProviderConfig, idToken *gooidc.IDToken, claims, userInfo map[string]any) UserProfile {
	merged := make(map[string]any, len(claims)+len(userInfo))
	for k, v := range claims {
		merged[k] = v
	}
	for k, v := range userInfo {
		if _, ok := merged[k]; !ok {
			merged[k] = v
		}
	}
	email, _ := merged["email"].(string)
	name, _ := merged["name"].(string)
	if name == "" {
		name, _ = merged["preferred_username"].(string)
	}
	if name == "" {
		name, _ = merged["given_name"].(string)
	}
	avatar, _ := merged["picture"].(string)
	return UserProfile{
		Provider:   "oidc:" + cfg.ID,
		ProviderID: cfg.ID,
		Subject:    idToken.Subject,
		Email:      strings.TrimSpace(email),
		Name:       strings.TrimSpace(name),
		AvatarURL:  strings.TrimSpace(avatar),
		Issuer:     idToken.Issuer,
		Claims:     claims,
		UserInfo:   userInfo,
	}
}

func validateProfile(cfg ProviderConfig, profile UserProfile) error {
	if strings.TrimSpace(profile.Subject) == "" {
		return errors.New("oidc: id_token missing sub claim")
	}
	if strings.TrimSpace(profile.Email) == "" {
		return errors.New("oidc: profile missing email claim")
	}
	if cfg.RequireVerifiedEmail {
		verified, ok := boolClaim(profile.Claims["email_verified"])
		if !ok && len(profile.UserInfo) > 0 {
			verified, ok = boolClaim(profile.UserInfo["email_verified"])
		}
		if !ok || !verified {
			return errors.New("oidc: email is not verified")
		}
	}
	if len(cfg.AllowedDomains) > 0 {
		at := strings.LastIndex(profile.Email, "@")
		if at <= 0 || !contains(cfg.AllowedDomains, strings.ToLower(profile.Email[at+1:])) {
			return errors.New("oidc: email domain is not allowed")
		}
	}
	return nil
}

func boolClaim(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}
