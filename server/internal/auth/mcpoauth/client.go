package mcpoauth

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	maxMetadataBytes = 1 << 20
	maxMCPBytes      = 4 << 20
	mcpProtocol      = "2025-06-18"
)

var ErrUnauthorized = errors.New("mcp oauth: authorization rejected")

type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

type Client struct {
	http Doer
	now  func() time.Time
}

type Transaction struct {
	State                   string `json:"state"`
	CodeVerifier            string `json:"code_verifier"`
	ClientID                string `json:"client_id"`
	ClientSecret            string `json:"client_secret,omitempty"`
	TokenEndpointAuthMethod string `json:"token_endpoint_auth_method"`
	AuthorizationEndpoint   string `json:"authorization_endpoint"`
	TokenEndpoint           string `json:"token_endpoint"`
	RedirectURI             string `json:"redirect_uri"`
	Resource                string `json:"resource"`
	Scope                   string `json:"scope,omitempty"`
	IssuedAt                int64  `json:"issued_at"`
}

type Credential struct {
	AccessToken             string
	RefreshToken            string
	TokenType               string
	Scope                   string
	ExpiresAt               time.Time
	ClientID                string
	ClientSecret            string
	TokenEndpointAuthMethod string
	TokenEndpoint           string
	Resource                string
}

type ProbeResult struct {
	ProtocolVersion string
	ServerName      string
	ServerVersion   string
	ToolCount       int
}

type jsonRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type toolsListResult struct {
	Tools []json.RawMessage `json:"tools"`
}

type protectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

type authorizationServerMetadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
	TokenEndpointMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
}

type registrationResponse struct {
	ClientID                string `json:"client_id"`
	ClientSecret            string `json:"client_secret"`
	TokenEndpointAuthMethod string `json:"token_endpoint_auth_method"`
}

type tokenResponse struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	TokenType    string          `json:"token_type"`
	Scope        string          `json:"scope"`
	ExpiresIn    json.RawMessage `json:"expires_in"`
}

func New(httpClient Doer) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{http: httpClient, now: time.Now}
}

func (c *Client) Begin(ctx context.Context, resource, redirectURI string) (Transaction, string, error) {
	resourceURL, err := requireHTTPSURL(resource)
	if err != nil {
		return Transaction{}, "", fmt.Errorf("mcp oauth: resource: %w", err)
	}
	if _, err := requireHTTPURL(redirectURI); err != nil {
		return Transaction{}, "", fmt.Errorf("mcp oauth: redirect_uri: %w", err)
	}

	var protected protectedResourceMetadata
	if err := c.getJSON(ctx, protectedResourceMetadataURL(resourceURL), &protected); err != nil {
		return Transaction{}, "", fmt.Errorf("mcp oauth: protected resource discovery: %w", err)
	}
	if strings.TrimSpace(protected.Resource) != resourceURL.String() {
		return Transaction{}, "", fmt.Errorf("mcp oauth: protected resource metadata returned resource %q", protected.Resource)
	}
	if len(protected.AuthorizationServers) == 0 {
		return Transaction{}, "", errors.New("mcp oauth: protected resource metadata has no authorization server")
	}
	issuer, err := requireHTTPSURL(protected.AuthorizationServers[0])
	if err != nil {
		return Transaction{}, "", fmt.Errorf("mcp oauth: authorization server: %w", err)
	}

	metadata, err := c.discoverAuthorizationServer(ctx, issuer)
	if err != nil {
		return Transaction{}, "", err
	}
	if strings.TrimRight(metadata.Issuer, "/") != strings.TrimRight(issuer.String(), "/") {
		return Transaction{}, "", fmt.Errorf("mcp oauth: authorization server issuer mismatch: %q", metadata.Issuer)
	}
	if !contains(metadata.CodeChallengeMethodsSupported, "S256") {
		return Transaction{}, "", errors.New("mcp oauth: authorization server does not support PKCE S256")
	}
	if _, err := requireHTTPSURL(metadata.AuthorizationEndpoint); err != nil {
		return Transaction{}, "", fmt.Errorf("mcp oauth: authorization endpoint: %w", err)
	}
	if _, err := requireHTTPSURL(metadata.TokenEndpoint); err != nil {
		return Transaction{}, "", fmt.Errorf("mcp oauth: token endpoint: %w", err)
	}
	if _, err := requireHTTPSURL(metadata.RegistrationEndpoint); err != nil {
		return Transaction{}, "", fmt.Errorf("mcp oauth: registration endpoint: %w", err)
	}

	registration, err := c.register(ctx, metadata.RegistrationEndpoint, redirectURI)
	if err != nil {
		return Transaction{}, "", err
	}
	state, err := randomURLToken(24)
	if err != nil {
		return Transaction{}, "", fmt.Errorf("mcp oauth: state: %w", err)
	}
	verifier, err := randomURLToken(48)
	if err != nil {
		return Transaction{}, "", fmt.Errorf("mcp oauth: code verifier: %w", err)
	}
	challengeBytes := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeBytes[:])

	authorizeURL, err := url.Parse(metadata.AuthorizationEndpoint)
	if err != nil {
		return Transaction{}, "", fmt.Errorf("mcp oauth: parse authorization endpoint: %w", err)
	}
	query := authorizeURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", registration.ClientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("state", state)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	query.Set("resource", resourceURL.String())
	scope := strings.Join(protected.ScopesSupported, " ")
	if scope != "" {
		query.Set("scope", scope)
	}
	authorizeURL.RawQuery = query.Encode()

	return Transaction{
		State:                   state,
		CodeVerifier:            verifier,
		ClientID:                registration.ClientID,
		ClientSecret:            registration.ClientSecret,
		TokenEndpointAuthMethod: registration.TokenEndpointAuthMethod,
		AuthorizationEndpoint:   metadata.AuthorizationEndpoint,
		TokenEndpoint:           metadata.TokenEndpoint,
		RedirectURI:             redirectURI,
		Resource:                resourceURL.String(),
		Scope:                   scope,
		IssuedAt:                c.now().UTC().Unix(),
	}, authorizeURL.String(), nil
}

func (c *Client) discoverAuthorizationServer(ctx context.Context, issuer *url.URL) (authorizationServerMetadata, error) {
	var metadata authorizationServerMetadata
	oauthURL := authorizationServerMetadataURL(issuer)
	if err := c.getJSON(ctx, oauthURL, &metadata); err == nil {
		return metadata, nil
	} else {
		var openIDMetadata authorizationServerMetadata
		openIDURL := openIDConfigurationURL(issuer)
		if openIDErr := c.getJSON(ctx, openIDURL, &openIDMetadata); openIDErr != nil {
			return authorizationServerMetadata{}, fmt.Errorf(
				"mcp oauth: authorization server discovery: oauth metadata: %v; openid metadata: %w",
				err,
				openIDErr,
			)
		}
		return openIDMetadata, nil
	}
}

func (c *Client) Exchange(ctx context.Context, transaction Transaction, code string) (Credential, error) {
	values := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {strings.TrimSpace(code)},
		"redirect_uri":  {transaction.RedirectURI},
		"client_id":     {transaction.ClientID},
		"code_verifier": {transaction.CodeVerifier},
		"resource":      {transaction.Resource},
	}
	return c.tokenRequest(ctx, transaction.TokenEndpoint, transaction.TokenEndpointAuthMethod, transaction.ClientID, transaction.ClientSecret, values, "")
}

func (c *Client) Refresh(ctx context.Context, credential Credential) (Credential, error) {
	if strings.TrimSpace(credential.RefreshToken) == "" {
		return Credential{}, errors.New("mcp oauth: refresh token is empty")
	}
	values := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {credential.RefreshToken},
		"client_id":     {credential.ClientID},
		"resource":      {credential.Resource},
	}
	return c.tokenRequest(ctx, credential.TokenEndpoint, credential.TokenEndpointAuthMethod, credential.ClientID, credential.ClientSecret, values, credential.RefreshToken)
}

// Probe verifies that an OAuth access token is accepted by a remote MCP
// server and that the server can complete the MCP lifecycle through
// tools/list. It never executes a tool.
func (c *Client) Probe(ctx context.Context, resource, accessToken string) (ProbeResult, error) {
	resourceURL, err := requireHTTPSURL(resource)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("mcp probe: resource: %w", err)
	}
	if strings.TrimSpace(accessToken) == "" {
		return ProbeResult{}, errors.New("mcp probe: access token is empty")
	}

	var initialized initializeResult
	headers, err := c.mcpRequest(ctx, resourceURL.String(), accessToken, "", "", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcpProtocol,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]string{
				"name":    "Parsar",
				"version": "1.0",
			},
		},
	}, &initialized)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("mcp probe: initialize: %w", err)
	}
	protocolVersion := strings.TrimSpace(initialized.ProtocolVersion)
	if protocolVersion == "" {
		return ProbeResult{}, errors.New("mcp probe: initialize returned no protocol version")
	}
	sessionID := strings.TrimSpace(headers.Get("Mcp-Session-Id"))

	if _, err := c.mcpRequest(ctx, resourceURL.String(), accessToken, sessionID, protocolVersion, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}, nil); err != nil {
		return ProbeResult{}, fmt.Errorf("mcp probe: initialized notification: %w", err)
	}

	var tools toolsListResult
	if _, err := c.mcpRequest(ctx, resourceURL.String(), accessToken, sessionID, protocolVersion, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]any{},
	}, &tools); err != nil {
		return ProbeResult{}, fmt.Errorf("mcp probe: tools/list: %w", err)
	}

	return ProbeResult{
		ProtocolVersion: protocolVersion,
		ServerName:      strings.TrimSpace(initialized.ServerInfo.Name),
		ServerVersion:   strings.TrimSpace(initialized.ServerInfo.Version),
		ToolCount:       len(tools.Tools),
	}, nil
}

func (c *Client) mcpRequest(
	ctx context.Context,
	endpoint string,
	accessToken string,
	sessionID string,
	protocolVersion string,
	payload map[string]any,
	out any,
) (http.Header, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	if protocolVersion != "" {
		req.Header.Set("MCP-Protocol-Version", protocolVersion)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxMCPBytes))
		return resp.Header, ErrUnauthorized
	}
	if out == nil && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent) {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxMCPBytes))
		return resp.Header, nil
	}
	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, maxMetadataBytes))
		return resp.Header, fmt.Errorf("unexpected HTTP status %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}
	if err := decodeMCPResponse(resp, out); err != nil {
		return resp.Header, err
	}
	return resp.Header, nil
}

func decodeMCPResponse(resp *http.Response, out any) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMCPBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxMCPBytes {
		return errors.New("mcp response exceeds 4 MiB")
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		body, err = firstSSEData(body)
		if err != nil {
			return err
		}
	}
	var envelope jsonRPCResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("decode json-rpc response: %w", err)
	}
	if envelope.Error != nil {
		return fmt.Errorf("json-rpc error %d: %s", envelope.Error.Code, strings.TrimSpace(envelope.Error.Message))
	}
	if len(envelope.Result) == 0 {
		return errors.New("json-rpc response has no result")
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return fmt.Errorf("decode json-rpc result: %w", err)
	}
	return nil
}

func firstSSEData(body []byte) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 64*1024), maxMCPBytes)
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data.Len() > 0 {
				return []byte(strings.TrimSuffix(data.String(), "\n")), nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			data.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if data.Len() > 0 {
		return []byte(strings.TrimSuffix(data.String(), "\n")), nil
	}
	return nil, errors.New("mcp SSE response has no data event")
}

func (c *Client) register(ctx context.Context, endpoint, redirectURI string) (registrationResponse, error) {
	body := map[string]any{
		"client_name":                "Parsar",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return registrationResponse{}, fmt.Errorf("mcp oauth: encode registration: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(encoded)))
	if err != nil {
		return registrationResponse{}, fmt.Errorf("mcp oauth: create registration request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	var response registrationResponse
	if err := c.doJSON(req, http.StatusCreated, &response); err != nil {
		return registrationResponse{}, fmt.Errorf("mcp oauth: dynamic client registration: %w", err)
	}
	if strings.TrimSpace(response.ClientID) == "" {
		return registrationResponse{}, errors.New("mcp oauth: dynamic client registration returned no client_id")
	}
	if strings.TrimSpace(response.TokenEndpointAuthMethod) == "" {
		response.TokenEndpointAuthMethod = "none"
	}
	switch response.TokenEndpointAuthMethod {
	case "none", "client_secret_basic", "client_secret_post":
	default:
		return registrationResponse{}, fmt.Errorf("mcp oauth: unsupported token endpoint auth method %q", response.TokenEndpointAuthMethod)
	}
	return response, nil
}

func (c *Client) tokenRequest(ctx context.Context, endpoint, authMethod, clientID, clientSecret string, values url.Values, fallbackRefreshToken string) (Credential, error) {
	if _, err := requireHTTPSURL(endpoint); err != nil {
		return Credential{}, fmt.Errorf("mcp oauth: token endpoint: %w", err)
	}
	if authMethod == "client_secret_post" {
		values.Set("client_secret", clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return Credential{}, fmt.Errorf("mcp oauth: create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if authMethod == "client_secret_basic" {
		req.SetBasicAuth(clientID, clientSecret)
	}
	var response tokenResponse
	if err := c.doJSON(req, http.StatusOK, &response); err != nil {
		return Credential{}, fmt.Errorf("mcp oauth: token exchange: %w", err)
	}
	if strings.TrimSpace(response.AccessToken) == "" {
		return Credential{}, errors.New("mcp oauth: token response has no access_token")
	}
	refreshToken := strings.TrimSpace(response.RefreshToken)
	if refreshToken == "" {
		refreshToken = fallbackRefreshToken
	}
	expiresIn := parseExpiresIn(response.ExpiresIn)
	var expiresAt time.Time
	if expiresIn > 0 {
		expiresAt = c.now().UTC().Add(expiresIn)
	}
	return Credential{
		AccessToken:             response.AccessToken,
		RefreshToken:            refreshToken,
		TokenType:               response.TokenType,
		Scope:                   response.Scope,
		ExpiresAt:               expiresAt,
		ClientID:                clientID,
		ClientSecret:            clientSecret,
		TokenEndpointAuthMethod: authMethod,
		TokenEndpoint:           endpoint,
		Resource:                values.Get("resource"),
	}, nil
}

func (c *Client) getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	return c.doJSON(req, http.StatusOK, out)
}

func (c *Client) doJSON(req *http.Request, expectedStatus int, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadataBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxMetadataBytes {
		return errors.New("response exceeds 1 MiB")
	}
	if resp.StatusCode != expectedStatus {
		return fmt.Errorf("unexpected HTTP status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}

func protectedResourceMetadataURL(resource *url.URL) string {
	copyURL := *resource
	copyURL.RawQuery = ""
	copyURL.Fragment = ""
	path := strings.TrimPrefix(copyURL.EscapedPath(), "/")
	copyURL.Path = "/.well-known/oauth-protected-resource"
	copyURL.RawPath = ""
	if path != "" {
		copyURL.Path += "/" + path
	}
	return copyURL.String()
}

func authorizationServerMetadataURL(issuer *url.URL) string {
	copyURL := *issuer
	copyURL.RawQuery = ""
	copyURL.Fragment = ""
	issuerPath := strings.TrimPrefix(copyURL.EscapedPath(), "/")
	copyURL.Path = "/.well-known/oauth-authorization-server"
	copyURL.RawPath = ""
	if issuerPath != "" {
		copyURL.Path += "/" + issuerPath
	}
	return copyURL.String()
}

func openIDConfigurationURL(issuer *url.URL) string {
	copyURL := *issuer
	copyURL.RawQuery = ""
	copyURL.Fragment = ""
	issuerPath := strings.TrimPrefix(copyURL.EscapedPath(), "/")
	copyURL.Path = "/.well-known/openid-configuration"
	copyURL.RawPath = ""
	if issuerPath != "" {
		copyURL.Path += "/" + issuerPath
	}
	return copyURL.String()
}

func requireHTTPSURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" || parsed.User != nil {
		return nil, errors.New("must be an https URL without embedded credentials")
	}
	return parsed, nil
}

func requireHTTPURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return nil, errors.New("must be an http or https URL without embedded credentials")
	}
	return parsed, nil
}

func randomURLToken(size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func parseExpiresIn(raw json.RawMessage) time.Duration {
	if len(raw) == 0 {
		return 0
	}
	var seconds int64
	if err := json.Unmarshal(raw, &seconds); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		seconds, _ = strconv.ParseInt(value, 10, 64)
		if seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return 0
}
