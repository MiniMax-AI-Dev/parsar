// Package transport carries proto.Envelope frames between the daemon
// and the server-side agentdaemon gateway:
//
//  1. Bootstrap — one-shot HTTP POST asking the server for the WS URL
//     and heartbeat cadence; validates credential before WS handshake.
//  2. Dial / Conn — the live WebSocket. Single send goroutine (gorilla
//     requires single writer) and a bounded recv channel.
//  3. Reconnect — exponential-backoff loop the caller drives.
//
// Wire-only: nothing here knows about agent kinds.
package transport

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

// BootstrapResponse mirrors gateway/handler.go's Bootstrap response.
type BootstrapResponse struct {
	DeviceID         string `json:"device_id"`
	WorkspaceID      string `json:"workspace_id"`
	WSURL            string `json:"ws_url"`
	HeartbeatSeconds int    `json:"heartbeat_seconds"`
	ProtocolVersion  string `json:"protocol_version"`
}

// HeartbeatInterval defends against the server returning 0 — a zero
// heartbeat means "never ping" which guarantees a 60s timeout, so we
// fall back to 15s instead of silently breaking liveness.
func (b BootstrapResponse) HeartbeatInterval() time.Duration {
	if b.HeartbeatSeconds <= 0 {
		return 15 * time.Second
	}
	return time.Duration(b.HeartbeatSeconds) * time.Second
}

// Bootstrap calls POST <serverURL>/agent-daemon/bootstrap with the
// device's runner_credential as a bearer token.
func Bootstrap(ctx context.Context, serverURL, deviceID, credential, daemonVersion string) (*BootstrapResponse, error) {
	if strings.TrimSpace(serverURL) == "" {
		return nil, fmt.Errorf("transport.Bootstrap: serverURL required")
	}
	if strings.TrimSpace(deviceID) == "" {
		return nil, fmt.Errorf("transport.Bootstrap: deviceID required")
	}
	if strings.TrimSpace(credential) == "" {
		return nil, fmt.Errorf("transport.Bootstrap: credential required")
	}
	body, err := json.Marshal(map[string]string{"device_id": deviceID})
	if err != nil {
		return nil, fmt.Errorf("transport.Bootstrap: marshal body: %w", err)
	}
	target, err := joinURL(serverURL, "/agent-daemon/bootstrap")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("transport.Bootstrap: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+credential)
	if daemonVersion != "" {
		req.Header.Set("User-Agent", "parsar-daemon/"+daemonVersion)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("transport.Bootstrap: post: %w", err)
	}
	defer resp.Body.Close()
	// Cap the body so a misbehaving server can't OOM us. 64 KiB is
	// orders of magnitude above what the real handler returns.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("transport.Bootstrap: server returned %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var out BootstrapResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("transport.Bootstrap: decode response: %w", err)
	}
	if out.DeviceID == "" {
		return nil, fmt.Errorf("transport.Bootstrap: server returned empty device_id")
	}
	return &out, nil
}

// joinURL concatenates base + path, normalising trailing slashes.
func joinURL(base, path string) (string, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return "", fmt.Errorf("transport: parse base url %q: %w", base, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("transport: base url %q is missing scheme or host", base)
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u.Path = u.Path + path
	return u.String(), nil
}

// DeriveWSURL turns a Bootstrap response's ws_url into the absolute URL
// to dial. Empty ws_url (dev mode without a separate public hostname)
// is derived from serverBase by swapping http→ws / https→wss. Non-
// absolute ws_url is rejected — the server-side handler is the only
// component that knows the externally-reachable host.
func DeriveWSURL(boot BootstrapResponse, serverBase string) (string, error) {
	if abs := strings.TrimSpace(boot.WSURL); abs != "" {
		u, err := url.Parse(abs)
		if err != nil {
			return "", fmt.Errorf("transport: parse ws_url %q: %w", abs, err)
		}
		if u.Scheme != "ws" && u.Scheme != "wss" {
			return "", fmt.Errorf("transport: ws_url %q must use ws:// or wss://", abs)
		}
		return abs, nil
	}
	u, err := url.Parse(strings.TrimRight(serverBase, "/"))
	if err != nil {
		return "", fmt.Errorf("transport: parse serverBase: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("transport: serverBase scheme %q must be http or https", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/agent-daemon/ws"
	return u.String(), nil
}
