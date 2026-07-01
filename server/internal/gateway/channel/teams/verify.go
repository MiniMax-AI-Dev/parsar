// Package teams — inbound request verification (JWT bearer).
//
// The Bot Framework signs every webhook POST with a JWT bearer in the
// Authorization header, issued by the Bot Framework token service and audienced
// to the bot's Microsoft App Id. Verify checks that signature/issuer/audience —
// the INBOUND half of the asymmetric auth. It shares no token with the OUTBOUND
// AAD client-credentials bearer minted in credentials.go: inbound proves "the
// Connector is calling me", outbound proves "I am the bot calling the
// Connector". Conflating them is the canonical Bot Framework 401, so they live
// in separate files with separate types.
//
// When no verifier is configured (New with an empty AppID, or WithTokenVerifier
// nil) Verify is a pass-through, mirroring Slack's empty-signingSecret skip —
// the Bot Framework Emulator sends unsigned requests, so local debugging needs
// the skip. There is no url_verification handshake on the Bot Framework webhook
// (that is a Slack/Feishu concept), so Verify never returns a challenge.
package teams

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// botFrameworkOpenIDConfig is the public Bot Framework OpenID metadata document
// whose jwks_uri lists the RSA keys inbound tokens are signed with.
const botFrameworkOpenIDConfig = "https://login.botframework.com/v1/.well-known/openidconfiguration"

// botFrameworkIssuer is the issuer claim a genuine Bot Framework inbound token
// carries. Emulator tokens carry a different issuer; verification is disabled
// entirely for that local path rather than special-cased here.
const botFrameworkIssuer = "https://api.botframework.com"

// TokenVerifier authenticates the inbound Authorization header. It is the seam
// Verify calls; a JWKS-backed implementation ships as jwksVerifier, and tests
// inject a fake. Verify passes the raw header ("Bearer <jwt>"); the verifier
// strips the scheme.
type TokenVerifier interface {
	Verify(ctx context.Context, authorizationHeader string) error
}

// Verify authenticates an inbound Teams request. With a verifier configured it
// checks the Authorization bearer and returns the body unchanged on success;
// without one it passes the body through. It never returns a challenge (the Bot
// Framework webhook has no url_verification handshake).
func (c *Channel) Verify(r *http.Request, body []byte) (verified []byte, challenge string, err error) {
	if c.verifier == nil {
		return body, "", nil
	}
	if r == nil {
		return nil, "", errors.New("teams channel: verify requires the request for its Authorization header")
	}
	if err := c.verifier.Verify(r.Context(), r.Header.Get("Authorization")); err != nil {
		return nil, "", fmt.Errorf("teams channel: verify inbound token: %w", err)
	}
	return body, "", nil
}

// jwksVerifier validates a Bot Framework inbound JWT against the RSA keys
// published at the OpenID metadata jwks_uri, checking the RS256 signature, the
// issuer, the audience (== the bot's app id) and expiry. Keys are fetched lazily
// and cached with a TTL so a rotation is picked up without a restart and a
// verified request costs no network round-trip.
type jwksVerifier struct {
	audience string
	// audienceAllowed, when set, replaces the single-audience check: the token's
	// own aud claim(s) are validated against this predicate (multi-tenant — one
	// webhook serving many workspace app_ids). audience is ignored while set.
	audienceAllowed func(string) bool
	issuer          string
	openIDURL       string
	httpClient      *http.Client
	refreshTTL      time.Duration
	mu              sync.RWMutex
	keys            map[string]*rsa.PublicKey
	lastRefresh     time.Time
}

// VerifierOption customizes a jwksVerifier (test injection of the HTTP client /
// endpoints / issuer).
type VerifierOption func(*jwksVerifier)

// WithHTTPClient overrides the HTTP client used to fetch OpenID metadata / JWKS.
func WithHTTPClient(hc *http.Client) VerifierOption {
	return func(v *jwksVerifier) {
		if hc != nil {
			v.httpClient = hc
		}
	}
}

// WithOpenIDConfigURL overrides the OpenID metadata URL (tests point it at a
// local fixture server).
func WithOpenIDConfigURL(u string) VerifierOption {
	return func(v *jwksVerifier) {
		if strings.TrimSpace(u) != "" {
			v.openIDURL = u
		}
	}
}

// WithExpectedIssuer overrides the accepted issuer claim (tests match their
// fixture token's issuer).
func WithExpectedIssuer(iss string) VerifierOption {
	return func(v *jwksVerifier) {
		if strings.TrimSpace(iss) != "" {
			v.issuer = iss
		}
	}
}

// NewJWKSVerifier builds the production Bot Framework token verifier for the
// given bot app id (the enforced audience). It is wired into the adapter via
// WithTokenVerifier; the runner builds it only when an app id is configured.
func NewJWKSVerifier(appID string, opts ...VerifierOption) TokenVerifier {
	v := &jwksVerifier{
		audience:   strings.TrimSpace(appID),
		issuer:     botFrameworkIssuer,
		openIDURL:  botFrameworkOpenIDConfig,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		refreshTTL: 12 * time.Hour,
		keys:       map[string]*rsa.PublicKey{},
	}
	for _, o := range opts {
		o(v)
	}
	return v
}

// NewMultiTenantJWKSVerifier builds a Bot Framework token verifier for a single
// webhook that serves many workspace bots. Instead of enforcing one fixed
// audience it validates each token's own aud claim against allowed — the closure
// answers "is this app_id a registered, enabled Teams connector (or the env
// bot)?". Signature/issuer/JWKS handling is identical to the single-tenant path;
// only the audience check differs.
func NewMultiTenantJWKSVerifier(allowed func(string) bool, opts ...VerifierOption) TokenVerifier {
	v := &jwksVerifier{
		audienceAllowed: allowed,
		issuer:          botFrameworkIssuer,
		openIDURL:       botFrameworkOpenIDConfig,
		httpClient:      &http.Client{Timeout: 10 * time.Second},
		refreshTTL:      12 * time.Hour,
		keys:            map[string]*rsa.PublicKey{},
	}
	for _, o := range opts {
		o(v)
	}
	return v
}

// Verify strips the bearer scheme and validates the JWT. A malformed header, a
// bad signature, a wrong issuer/audience or an expired token all surface as
// errors so Verify rejects the request.
func (v *jwksVerifier) Verify(ctx context.Context, authorizationHeader string) error {
	raw := strings.TrimSpace(authorizationHeader)
	if raw == "" {
		return errors.New("missing Authorization header")
	}
	if bearer, ok := strings.CutPrefix(raw, "Bearer "); ok {
		raw = strings.TrimSpace(bearer)
	}
	if raw == "" {
		return errors.New("empty bearer token")
	}

	keyfunc := func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method %q", token.Header["alg"])
		}
		kid, _ := token.Header["kid"].(string)
		key, err := v.keyForKID(ctx, kid)
		if err != nil {
			return nil, err
		}
		return key, nil
	}

	parserOpts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{"RS256"}),
	}
	if v.audienceAllowed == nil {
		// Single-tenant: enforce the one fixed audience during parse.
		parserOpts = append(parserOpts, jwt.WithAudience(v.audience))
	}
	if v.issuer != "" {
		parserOpts = append(parserOpts, jwt.WithIssuer(v.issuer))
	}
	token, err := jwt.Parse(raw, keyfunc, parserOpts...)
	if err != nil {
		return err
	}
	if v.audienceAllowed != nil {
		// Multi-tenant: the token is signature/issuer-valid; now the aud claim
		// must name an app_id we actually serve.
		auds, err := token.Claims.GetAudience()
		if err != nil {
			return fmt.Errorf("read audience claim: %w", err)
		}
		if !audienceAccepted(auds, v.audienceAllowed) {
			return fmt.Errorf("token audience %v is not a registered teams bot", auds)
		}
	}
	return nil
}

// audienceAccepted reports whether any audience entry passes the allow predicate.
func audienceAccepted(auds []string, allowed func(string) bool) bool {
	for _, a := range auds {
		if a = strings.TrimSpace(a); a != "" && allowed(a) {
			return true
		}
	}
	return false
}

// keyForKID returns the RSA public key for a JWT kid, refreshing the JWKS cache
// on a miss or once the TTL has lapsed. A miss after a fresh fetch is a hard
// error (unknown signing key).
func (v *jwksVerifier) keyForKID(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	kid = strings.TrimSpace(kid)
	if kid == "" {
		return nil, errors.New("token missing kid header")
	}
	v.mu.RLock()
	key, ok := v.keys[kid]
	fresh := time.Since(v.lastRefresh) < v.refreshTTL
	v.mu.RUnlock()
	if ok && fresh {
		return key, nil
	}
	if err := v.refresh(ctx); err != nil {
		// Serve a stale-but-known key rather than fail on a transient fetch error.
		if ok {
			return key, nil
		}
		return nil, err
	}
	v.mu.RLock()
	key, ok = v.keys[kid]
	v.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no signing key for kid %q", kid)
	}
	return key, nil
}

// refresh fetches the OpenID metadata, follows jwks_uri and rebuilds the kid→key
// map. It replaces the cache wholesale so a rotated-out key stops validating.
func (v *jwksVerifier) refresh(ctx context.Context) error {
	jwksURI, err := v.fetchJWKSURI(ctx)
	if err != nil {
		return err
	}
	keys, err := v.fetchKeys(ctx, jwksURI)
	if err != nil {
		return err
	}
	v.mu.Lock()
	v.keys = keys
	v.lastRefresh = time.Now()
	v.mu.Unlock()
	return nil
}

func (v *jwksVerifier) fetchJWKSURI(ctx context.Context) (string, error) {
	var doc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := v.getJSON(ctx, v.openIDURL, &doc); err != nil {
		return "", fmt.Errorf("fetch openid metadata: %w", err)
	}
	if strings.TrimSpace(doc.JWKSURI) == "" {
		return "", errors.New("openid metadata has no jwks_uri")
	}
	return doc.JWKSURI, nil
}

// jwk is one JSON Web Key (RSA public key: modulus n, exponent e, key id kid).
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (v *jwksVerifier) fetchKeys(ctx context.Context, jwksURI string) (map[string]*rsa.PublicKey, error) {
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := v.getJSON(ctx, jwksURI, &set); err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}
	out := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if !strings.EqualFold(k.Kty, "RSA") || strings.TrimSpace(k.Kid) == "" {
			continue
		}
		pub, err := jwkToRSA(k)
		if err != nil {
			continue // skip a malformed key rather than fail the whole set
		}
		out[k.Kid] = pub
	}
	if len(out) == 0 {
		return nil, errors.New("jwks carried no usable RSA keys")
	}
	return out, nil
}

// jwkToRSA reconstructs an *rsa.PublicKey from a JWK's base64url n / e.
func jwkToRSA(k jwk) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.N, "="))
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(k.E, "="))
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}
	// Left-pad the exponent to 8 bytes so it fits a uint64.
	if len(eBytes) > 8 {
		return nil, errors.New("exponent too large")
	}
	padded := make([]byte, 8)
	copy(padded[8-len(eBytes):], eBytes)
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(binary.BigEndian.Uint64(padded)),
	}, nil
}

func (v *jwksVerifier) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("GET %s returned status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
