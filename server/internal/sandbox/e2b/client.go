package e2b

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultAPIBaseURL  = "https://api.e2b.app"
	DefaultSandboxHost = "e2b.app"
	DefaultTemplate    = "base"
	DefaultEnvdPort    = 49983
)

var (
	ErrAPIKeyRequired = errors.New("e2b: api key is required")
	ErrSandboxIDEmpty = errors.New("e2b: sandbox id is empty")
)

type Client struct {
	HTTPClient        *http.Client
	EnvdHTTPClient    *http.Client // separate client; envd ingress cert may differ from API cert
	APIBaseURL        string
	SandboxHost       string
	SandboxBaseURL    string
	APIKey            string
	DefaultTemplateID string
}

type CreateInput struct {
	TemplateID     string
	TimeoutSeconds int
	Secure         *bool
	Metadata       map[string]string
	Env            map[string]string
}

type Sandbox struct {
	TemplateID         string `json:"templateID"`
	SandboxID          string `json:"sandboxID"`
	Alias              string `json:"alias,omitempty"`
	EnvdVersion        string `json:"envdVersion"`
	EnvdAccessToken    string `json:"envdAccessToken,omitempty"`
	TrafficAccessToken string `json:"trafficAccessToken,omitempty"`
	Domain             string `json:"domain,omitempty"`
}

type RunCommandInput struct {
	Sandbox Sandbox
	Command string
	CWD     string
	User    string // system user to run as; envd requires this field
	Env     map[string]string
	Timeout time.Duration
	EnvdURL string // direct envd base URL; bypasses domain/subdomain construction when set
}

type CommandResult struct {
	PID    int    `json:"pid,omitempty"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Status string `json:"status,omitempty"`
	Error  string `json:"error,omitempty"`
	Exited bool   `json:"exited"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) Create(ctx context.Context, input CreateInput) (Sandbox, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return Sandbox{}, ErrAPIKeyRequired
	}
	template := strings.TrimSpace(input.TemplateID)
	if template == "" {
		template = c.DefaultTemplateID
	}
	if template == "" {
		template = DefaultTemplate
	}
	timeout := input.TimeoutSeconds
	if timeout <= 0 {
		timeout = 60
	}
	body := map[string]any{
		"templateID": template,
		"timeout":    timeout,
		"metadata":   input.Metadata,
	}
	if input.Secure != nil {
		body["secure"] = *input.Secure
	}
	if len(input.Env) > 0 {
		body["envVars"] = input.Env
	}
	var sandbox Sandbox
	if err := c.doJSON(ctx, http.MethodPost, "/sandboxes", body, http.StatusCreated, &sandbox); err != nil {
		return Sandbox{}, err
	}
	return sandbox, nil
}

func (c *Client) Kill(ctx context.Context, sandboxID string) error {
	if strings.TrimSpace(c.APIKey) == "" {
		return ErrAPIKeyRequired
	}
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return ErrSandboxIDEmpty
	}
	return c.doJSON(ctx, http.MethodDelete, "/sandboxes/"+url.PathEscape(sandboxID), nil, http.StatusNoContent, nil)
}

func (c *Client) Renew(ctx context.Context, sandboxID string, timeoutSeconds int) error {
	if strings.TrimSpace(c.APIKey) == "" {
		return ErrAPIKeyRequired
	}
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return ErrSandboxIDEmpty
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = 60
	}
	body := map[string]any{"timeout": timeoutSeconds}
	return c.doJSON(ctx, http.MethodPost, "/sandboxes/"+url.PathEscape(sandboxID)+"/timeout", body, http.StatusNoContent, nil)
}

// SandboxRuntimeInfo is the live-state snapshot returned by GetInfo.
// EndAt is the canonical TTL expiry; the "create + timeoutSeconds"
// math is only approximate because of clock skew and queueing.
type SandboxRuntimeInfo struct {
	SandboxID string    `json:"sandboxID"`
	EndAt     time.Time `json:"endAt"`
	StartedAt time.Time `json:"startedAt"`
	State     string    `json:"state"`
}

// GetInfo fetches the e2b control plane's current view of a sandbox.
func (c *Client) GetInfo(ctx context.Context, sandboxID string) (SandboxRuntimeInfo, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return SandboxRuntimeInfo{}, ErrAPIKeyRequired
	}
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return SandboxRuntimeInfo{}, ErrSandboxIDEmpty
	}
	var info SandboxRuntimeInfo
	if err := c.doJSON(ctx, http.MethodGet, "/sandboxes/"+url.PathEscape(sandboxID), nil, http.StatusOK, &info); err != nil {
		return SandboxRuntimeInfo{}, err
	}
	return info, nil
}

func (c *Client) RunCommand(ctx context.Context, input RunCommandInput) (CommandResult, error) {
	if strings.TrimSpace(input.Sandbox.SandboxID) == "" {
		return CommandResult{}, ErrSandboxIDEmpty
	}
	cmd := strings.TrimSpace(input.Command)
	if cmd == "" {
		return CommandResult{}, errors.New("e2b: command is required")
	}
	endpoint := c.envdURL(input.Sandbox) + "/process.Process/Start"
	if input.EnvdURL != "" {
		endpoint = strings.TrimRight(input.EnvdURL, "/") + "/process.Process/Start"
	}
	payload := map[string]any{
		"process": map[string]any{
			"cmd":  "/bin/bash",
			"args": []string{"-l", "-c", cmd},
		},
		"stdin": false,
	}
	process := payload["process"].(map[string]any)
	user := strings.TrimSpace(input.User)
	if user == "" {
		user = "root"
	}
	process["user"] = user
	if strings.TrimSpace(input.CWD) != "" {
		process["cwd"] = input.CWD
	}
	if len(input.Env) > 0 {
		process["envs"] = input.Env
	}
	body, err := marshalConnectEnvelope(payload)
	if err != nil {
		return CommandResult{}, err
	}
	if input.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, input.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return CommandResult{}, err
	}
	req.Header.Set("Content-Type", "application/connect+json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("E2b-Sandbox-Id", input.Sandbox.SandboxID)
	req.Header.Set("E2b-Sandbox-Port", fmt.Sprint(DefaultEnvdPort))
	if input.Sandbox.EnvdAccessToken != "" {
		req.Header.Set("X-Access-Token", input.Sandbox.EnvdAccessToken)
	}
	// envd authenticates the system user via HTTP Basic Auth:
	// username = OS user to run as, password empty.
	req.SetBasicAuth(user, "")
	res, err := c.envdHTTPClient().Do(req)
	if err != nil {
		return CommandResult{}, fmt.Errorf("e2b envd request to %s failed: %w", endpoint, err)
	}
	defer res.Body.Close()
	// Read full body so we can log it for debugging and still decode.
	respBody, _ := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return CommandResult{}, errors.New(RedactSecret(
			fmt.Sprintf("e2b envd start failed: status=%d body=%s", res.StatusCode, strings.TrimSpace(string(respBody))),
			c.APIKey,
		))
	}
	if len(respBody) == 0 {
		return CommandResult{}, fmt.Errorf("e2b envd: %s returned status=%d with empty body (endpoint=%s sandbox=%s)",
			res.Status, res.StatusCode, endpoint, input.Sandbox.SandboxID)
	}
	result, decodeErr := decodeProcessEvents(bytes.NewReader(respBody))
	if decodeErr != nil {
		return result, fmt.Errorf("e2b envd decode: %w (status=%d bodyLen=%d bodyHex=%.512s endpoint=%s)",
			decodeErr, res.StatusCode, len(respBody), fmt.Sprintf("%x", respBody), endpoint)
	}
	if !result.Exited || result.Status == "" {
		return result, fmt.Errorf("e2b envd: process did not exit normally (exited=%v status=%q stdout_len=%d stderr_len=%d status_code=%d bodyLen=%d bodyHex=%.512s endpoint=%s)",
			result.Exited, result.Status, len(result.Stdout), len(result.Stderr), res.StatusCode, len(respBody), fmt.Sprintf("%x", respBody), endpoint)
	}
	return result, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, wantStatus int, out any) error {
	base := strings.TrimRight(c.APIBaseURL, "/")
	if base == "" {
		base = DefaultAPIBaseURL
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != wantStatus {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		var apiErr apiError
		if err := json.Unmarshal(b, &apiErr); err == nil && apiErr.Message != "" {
			return errors.New(RedactSecret(
				fmt.Sprintf("e2b api %s %s failed: status=%d message=%s", method, path, res.StatusCode, apiErr.Message),
				c.APIKey,
			))
		}
		return errors.New(RedactSecret(
			fmt.Sprintf("e2b api %s %s failed: status=%d body=%s", method, path, res.StatusCode, strings.TrimSpace(string(b))),
			c.APIKey,
		))
	}
	if out == nil || res.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (c *Client) envdURL(s Sandbox) string {
	return c.SandboxPortURL(s, DefaultEnvdPort)
}

// SandboxPortURL returns the public HTTPS URL where the sandbox-internal
// process listening on the given port can be reached from outside. The
// scheme is: https://<port>-<sandboxID>.<domain>
//
// The caller is responsible for adding `X-Access-Token` when secure mode
// is enabled.
func (c *Client) SandboxPortURL(s Sandbox, port int) string {
	if port <= 0 {
		port = DefaultEnvdPort
	}
	if strings.TrimSpace(c.SandboxBaseURL) != "" {
		// Test / proxy override: honour the caller-provided base URL verbatim.
		return strings.TrimRight(c.SandboxBaseURL, "/")
	}
	// SandboxHost takes priority when explicitly set — self-hosted
	// deployments may return an API domain in s.Domain that differs
	// from the envd routing domain.
	host := strings.TrimSpace(c.SandboxHost)
	if host == "" {
		host = strings.TrimSpace(s.Domain)
	}
	if host == "" {
		host = DefaultSandboxHost
	}
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	return fmt.Sprintf("https://%d-%s.%s", port, s.SandboxID, host)
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c *Client) envdHTTPClient() *http.Client {
	if c.EnvdHTTPClient != nil {
		return c.EnvdHTTPClient
	}
	return c.httpClient()
}

// RedactSecret removes a caller-supplied secret from user-visible errors.
// E2B upstream error bodies may echo request metadata; must never return
// an API key to the browser or logs.
func RedactSecret(message, secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return message
	}
	return strings.ReplaceAll(message, secret, "[REDACTED]")
}

func marshalConnectEnvelope(msg any) ([]byte, error) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	buf := bytes.NewBuffer(make([]byte, 0, len(payload)+5))
	buf.WriteByte(0)
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(payload)))
	buf.Write(length[:])
	buf.Write(payload)
	return buf.Bytes(), nil
}

func decodeProcessEvents(r io.Reader) (CommandResult, error) {
	var result CommandResult
	for {
		var header [5]byte
		_, err := io.ReadFull(r, header[:])
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}
		if err != nil {
			return result, err
		}
		length := binary.BigEndian.Uint32(header[1:])
		if length == 0 {
			continue
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			return result, err
		}
		var event processStartResponse
		if err := json.Unmarshal(payload, &event); err != nil {
			return result, err
		}
		applyProcessEvent(&result, event.Event)
	}
	return result, nil
}

type processStartResponse struct {
	Event processEvent `json:"event"`
}

type processEvent struct {
	Start     *processStartEvent `json:"start,omitempty"`
	Data      *processDataEvent  `json:"data,omitempty"`
	End       *processEndEvent   `json:"end,omitempty"`
	Keepalive map[string]any     `json:"keepalive,omitempty"`
}

type processStartEvent struct {
	PID int `json:"pid"`
}

type processDataEvent struct {
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	PTY    string `json:"pty,omitempty"`
}

type processEndEvent struct {
	Exited bool   `json:"exited"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func applyProcessEvent(result *CommandResult, event processEvent) {
	if event.Start != nil {
		result.PID = event.Start.PID
	}
	if event.Data != nil {
		if event.Data.Stdout != "" {
			result.Stdout += decodeBase64OrLiteral(event.Data.Stdout)
		}
		if event.Data.Stderr != "" {
			result.Stderr += decodeBase64OrLiteral(event.Data.Stderr)
		}
		if event.Data.PTY != "" {
			result.Stdout += decodeBase64OrLiteral(event.Data.PTY)
		}
	}
	if event.End != nil {
		result.Exited = event.End.Exited
		// envd returns "exit status 0" / "exit status 1" — normalize
		// to just the numeric code so callers can compare with "0".
		result.Status = normalizeExitStatus(event.End.Status)
		result.Error = event.End.Error
	}
}

func normalizeExitStatus(s string) string {
	if after, ok := strings.CutPrefix(s, "exit status "); ok {
		return after
	}
	return s
}

func decodeBase64OrLiteral(value string) string {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return value
	}
	return string(decoded)
}
