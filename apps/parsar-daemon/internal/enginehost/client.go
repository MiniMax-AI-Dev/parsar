package enginehost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is the transport half of the engine-server contract: JSON over
// loopback HTTP plus a WebSocket downlink. It is deliberately protocol
// agnostic — it moves bytes and decodes JSON, and knows nothing about any
// engine's envelope, method names or event vocabulary. Adapters wrap it
// with their own typed calls.
//
// Requests carry no Origin header. The engines this package supervises
// gate /api on a browser-trust check that accepts a loopback Host with no
// Origin, and rejects a cross-site one; sending an Origin we invented
// would be the one way to fail that check.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient binds a Client to a lease's base URL. timeout bounds unary
// calls; pass 0 for no client-side deadline (long turns should instead be
// bounded by the caller's context).
func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: timeout},
	}
}

// BaseURL is the origin this client talks to.
func (c *Client) BaseURL() string { return c.baseURL }

// PostJSON sends body as JSON to path and decodes the response into out.
// A nil out discards the body. Non-2xx responses become errors carrying a
// truncated body, because engine gateways answer policy rejections with a
// bare status and a one-word body.
func (c *Client) PostJSON(ctx context.Context, path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("enginehost: encode request %s: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(path), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("enginehost: build request %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("enginehost: post %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("enginehost: post %s: status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("enginehost: decode response %s: %w", path, err)
	}
	return nil
}

// Probe is a ServerSpec.Ready helper: it reports success when a POST to
// path answers any 2xx. Adapters that need a specific handshake write
// their own probe instead.
func (c *Client) Probe(ctx context.Context, path string, body any) error {
	return c.PostJSON(ctx, path, body, nil)
}

func (c *Client) url(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return c.baseURL + path
}

// wsURL rewrites the client's origin to the ws scheme for a downlink.
func (c *Client) wsURL(path string) (string, error) {
	u, err := url.Parse(c.url(path))
	if err != nil {
		return "", fmt.Errorf("enginehost: parse downlink url: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	return u.String(), nil
}
