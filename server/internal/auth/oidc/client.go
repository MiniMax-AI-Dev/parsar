package oidc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Client interface {
	ID() string
	Label() string
	RedirectURI() string
	AuthorizeURL(ctx context.Context, state, nonce string) (string, error)
	ExchangeCode(ctx context.Context, code, nonce string) (UserProfile, error)
}

type UserProfile struct {
	Provider   string
	Subject    string
	Email      string
	Name       string
	AvatarURL  string
	Issuer     string
	Claims     map[string]any
	UserInfo   map[string]any
	ProviderID string
}

type httpClient struct {
	cfg        ProviderConfig
	http       *http.Client
	refreshTTL time.Duration

	mu        sync.RWMutex
	discovery discoveryDocument
	keys      map[string]any
	refreshed time.Time
}

type discoveryDocument struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	UserinfoEndpoint      string   `json:"userinfo_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	IDTokenAlgs           []string `json:"id_token_signing_alg_values_supported"`
	TokenAuthMethods      []string `json:"token_endpoint_auth_methods_supported"`
}

func NewClient(cfg ProviderConfig) (Client, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return &httpClient{
		cfg:        cfg,
		http:       &http.Client{Timeout: 10 * time.Second},
		refreshTTL: 12 * time.Hour,
		keys:       map[string]any{},
	}, nil
}

func (c *httpClient) ID() string          { return c.cfg.ID }
func (c *httpClient) Label() string       { return c.cfg.Label }
func (c *httpClient) RedirectURI() string { return c.cfg.RedirectURI }

func (c *httpClient) AuthorizeURL(ctx context.Context, state, nonce string) (string, error) {
	doc, err := c.discoveryDocument(ctx)
	if err != nil {
		return "", err
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", c.cfg.ClientID)
	q.Set("redirect_uri", c.cfg.RedirectURI)
	q.Set("scope", strings.Join(c.cfg.Scopes, " "))
	q.Set("state", state)
	q.Set("nonce", nonce)
	return doc.AuthorizationEndpoint + "?" + q.Encode(), nil
}

func (c *httpClient) ExchangeCode(ctx context.Context, code, nonce string) (UserProfile, error) {
	doc, err := c.discoveryDocument(ctx)
	if err != nil {
		return UserProfile{}, err
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", strings.TrimSpace(code))
	form.Set("redirect_uri", c.cfg.RedirectURI)
	authMethod := c.tokenAuthMethod(doc)
	if authMethod == "client_secret_post" {
		form.Set("client_id", c.cfg.ClientID)
		form.Set("client_secret", c.cfg.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, doc.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return UserProfile{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if authMethod == "client_secret_basic" {
		req.SetBasicAuth(c.cfg.ClientID, c.cfg.ClientSecret)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return UserProfile{}, fmt.Errorf("oidc: token exchange: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return UserProfile{}, fmt.Errorf("oidc: token endpoint returned %d", resp.StatusCode)
	}
	var token tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return UserProfile{}, fmt.Errorf("oidc: decode token response: %w", err)
	}
	if token.Error != "" {
		return UserProfile{}, fmt.Errorf("oidc: token exchange failed: %s", coalesce(token.ErrorDescription, token.Error))
	}
	if strings.TrimSpace(token.IDToken) == "" {
		return UserProfile{}, errors.New("oidc: token response missing id_token")
	}

	claims, err := c.verifyIDToken(ctx, token.IDToken, nonce, doc)
	if err != nil {
		return UserProfile{}, err
	}
	userInfo := map[string]any{}
	if doc.UserinfoEndpoint != "" && strings.TrimSpace(token.AccessToken) != "" {
		userInfo, _ = c.fetchUserInfo(ctx, doc.UserinfoEndpoint, token.AccessToken)
	}
	if err := validateUserInfoSubject(claims, userInfo); err != nil {
		return UserProfile{}, err
	}
	profile := profileFromClaims(c.cfg, claims, userInfo, doc.Issuer)
	if err := validateProfile(c.cfg, profile); err != nil {
		return UserProfile{}, err
	}
	return profile, nil
}

func (c *httpClient) tokenAuthMethod(doc discoveryDocument) string {
	if c.cfg.TokenAuthMethod != "" {
		return c.cfg.TokenAuthMethod
	}
	if contains(doc.TokenAuthMethods, "client_secret_post") {
		return "client_secret_post"
	}
	return "client_secret_basic"
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	IDToken          string `json:"id_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (c *httpClient) discoveryDocument(ctx context.Context) (discoveryDocument, error) {
	c.mu.RLock()
	doc := c.discovery
	fresh := time.Since(c.refreshed) < c.refreshTTL
	c.mu.RUnlock()
	if doc.TokenEndpoint != "" && fresh {
		return doc, nil
	}
	discoveryURL := c.cfg.IssuerURL + "/.well-known/openid-configuration"
	if err := getJSON(ctx, c.http, discoveryURL, &doc); err != nil {
		return discoveryDocument{}, fmt.Errorf("oidc: fetch discovery for %q: %w", c.cfg.ID, err)
	}
	if strings.TrimSpace(doc.Issuer) == "" {
		doc.Issuer = c.cfg.IssuerURL
	}
	if strings.TrimSpace(doc.AuthorizationEndpoint) == "" ||
		strings.TrimSpace(doc.TokenEndpoint) == "" ||
		strings.TrimSpace(doc.JWKSURI) == "" {
		return discoveryDocument{}, fmt.Errorf("oidc: discovery for %q missing required endpoints", c.cfg.ID)
	}
	c.mu.Lock()
	c.discovery = doc
	c.refreshed = time.Now()
	c.mu.Unlock()
	return doc, nil
}

func (c *httpClient) verifyIDToken(ctx context.Context, raw, nonce string, doc discoveryDocument) (map[string]any, error) {
	claims := jwt.MapClaims{}
	keyfunc := func(token *jwt.Token) (any, error) {
		alg, _ := token.Header["alg"].(string)
		if !acceptedAlg(alg, doc.IDTokenAlgs) {
			return nil, fmt.Errorf("oidc: unsupported signing method %q", alg)
		}
		kid, _ := token.Header["kid"].(string)
		return c.keyForToken(ctx, doc.JWKSURI, kid, alg)
	}
	token, err := jwt.ParseWithClaims(raw, claims, keyfunc,
		jwt.WithIssuer(doc.Issuer),
		jwt.WithAudience(c.cfg.ClientID),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods(acceptedAlgs(doc.IDTokenAlgs)),
	)
	if err != nil {
		return nil, fmt.Errorf("oidc: verify id_token: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("oidc: invalid id_token")
	}
	if nonce != "" {
		got, _ := claims["nonce"].(string)
		if got != nonce {
			return nil, errors.New("oidc: nonce mismatch")
		}
	}
	return map[string]any(claims), nil
}

func (c *httpClient) keyForToken(ctx context.Context, jwksURI, kid, alg string) (any, error) {
	kid = strings.TrimSpace(kid)
	if kid == "" {
		return nil, errors.New("oidc: token missing kid header")
	}
	c.mu.RLock()
	key, ok := c.keys[kid]
	fresh := time.Since(c.refreshed) < c.refreshTTL
	c.mu.RUnlock()
	if ok && fresh {
		return key, nil
	}
	if err := c.refreshKeys(ctx, jwksURI); err != nil {
		if ok {
			return key, nil
		}
		return nil, err
	}
	c.mu.RLock()
	key, ok = c.keys[kid]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("oidc: no signing key for kid %q", kid)
	}
	if strings.HasPrefix(alg, "RS") {
		if _, ok := key.(*rsa.PublicKey); !ok {
			return nil, fmt.Errorf("oidc: kid %q is not an RSA key", kid)
		}
	}
	if strings.HasPrefix(alg, "ES") {
		if _, ok := key.(*ecdsa.PublicKey); !ok {
			return nil, fmt.Errorf("oidc: kid %q is not an ECDSA key", kid)
		}
	}
	return key, nil
}

func (c *httpClient) refreshKeys(ctx context.Context, jwksURI string) error {
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := getJSON(ctx, c.http, jwksURI, &set); err != nil {
		return fmt.Errorf("oidc: fetch jwks: %w", err)
	}
	keys := map[string]any{}
	for _, k := range set.Keys {
		if strings.TrimSpace(k.Kid) == "" {
			continue
		}
		switch strings.ToUpper(k.Kty) {
		case "RSA":
			pub, err := jwkToRSA(k)
			if err == nil {
				keys[k.Kid] = pub
			}
		case "EC":
			pub, err := jwkToECDSA(k)
			if err == nil {
				keys[k.Kid] = pub
			}
		}
	}
	if len(keys) == 0 {
		return errors.New("oidc: jwks carried no usable keys")
	}
	c.mu.Lock()
	c.keys = keys
	c.refreshed = time.Now()
	c.mu.Unlock()
	return nil
}

func (c *httpClient) fetchUserInfo(ctx context.Context, endpoint, accessToken string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc: fetch userinfo: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("oidc: userinfo endpoint returned %d", resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("oidc: decode userinfo: %w", err)
	}
	return out, nil
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func jwkToRSA(k jwk) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.N, "="))
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.E, "="))
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}
	if len(eBytes) == 0 {
		return nil, errors.New("empty exponent")
	}
	var e uint64
	if len(eBytes) < 8 {
		padded := make([]byte, 8)
		copy(padded[8-len(eBytes):], eBytes)
		e = binary.BigEndian.Uint64(padded)
	} else {
		e = binary.BigEndian.Uint64(eBytes[len(eBytes)-8:])
	}
	if e == 0 || e > uint64(^uint(0)>>1) {
		return nil, fmt.Errorf("invalid exponent %d", e)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e)}, nil
}

func jwkToECDSA(k jwk) (*ecdsa.PublicKey, error) {
	if k.Crv != "P-256" {
		return nil, fmt.Errorf("unsupported ec curve %q", k.Crv)
	}
	xBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.X, "="))
	if err != nil {
		return nil, fmt.Errorf("decode x: %w", err)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.Y, "="))
	if err != nil {
		return nil, fmt.Errorf("decode y: %w", err)
	}
	curve := elliptic.P256()
	x := new(big.Int).SetBytes(xBytes)
	y := new(big.Int).SetBytes(yBytes)
	if !curve.IsOnCurve(x, y) {
		return nil, errors.New("ec point is not on P-256")
	}
	return &ecdsa.PublicKey{
		Curve: curve,
		X:     x,
		Y:     y,
	}, nil
}

func validateUserInfoSubject(claims, userInfo map[string]any) error {
	if len(userInfo) == 0 {
		return nil
	}
	infoSub, _ := userInfo["sub"].(string)
	if strings.TrimSpace(infoSub) == "" {
		return nil
	}
	claimSub, _ := claims["sub"].(string)
	if infoSub != claimSub {
		return errors.New("oidc: userinfo sub does not match id_token sub")
	}
	return nil
}

func profileFromClaims(cfg ProviderConfig, claims, userInfo map[string]any, issuer string) UserProfile {
	merged := make(map[string]any, len(claims)+len(userInfo))
	for k, v := range claims {
		merged[k] = v
	}
	for k, v := range userInfo {
		if _, exists := merged[k]; !exists {
			merged[k] = v
		}
	}
	sub, _ := merged["sub"].(string)
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
		Subject:    sub,
		Email:      strings.TrimSpace(email),
		Name:       strings.TrimSpace(name),
		AvatarURL:  strings.TrimSpace(avatar),
		Issuer:     issuer,
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

func acceptedAlgs(discovered []string) []string {
	allowed := []string{"RS256", "RS384", "RS512", "ES256"}
	if len(discovered) == 0 {
		return allowed
	}
	out := make([]string, 0, len(discovered))
	for _, alg := range discovered {
		for _, a := range allowed {
			if alg == a {
				out = append(out, alg)
				break
			}
		}
	}
	if len(out) == 0 {
		return allowed
	}
	return out
}

func acceptedAlg(alg string, discovered []string) bool {
	return contains(acceptedAlgs(discovered), alg)
}

func getJSON(ctx context.Context, hc *http.Client, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
