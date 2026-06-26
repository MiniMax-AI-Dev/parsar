package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/internal/runtimecrypto"
)

func pairProfile(ctx context.Context, serverURL, token, deviceName string) (auth.Profile, *pairResponse, error) {
	base, err := normalizeServerURL(serverURL)
	if err != nil {
		return auth.Profile{}, nil, err
	}

	host, err := os.Hostname()
	if err != nil {
		// Stripped-down sandboxes can fail os.Hostname; "unknown"
		// keeps the request well-formed (it's only a label).
		host = "unknown"
	}
	if strings.TrimSpace(deviceName) == "" {
		deviceName = host
	}

	// Generate a fresh X25519 keypair before pairing. Public half is
	// persisted server-side under runtimes.config.runner_public_key so
	// SealForRuntime can encrypt payloads to this daemon; private half
	// stays in auth.Profile (0o600) and is required to OpenSeal on
	// receive. Every successful pair binds a brand-new pair.
	pubB64, privB64, err := runtimecrypto.GenerateRuntimeKeypair()
	if err != nil {
		return auth.Profile{}, nil, fmt.Errorf("generate runner keypair: %w", err)
	}

	pair, err := pairWithServer(ctx, base, pairRequest{
		PairingToken:    token,
		Hostname:        host,
		Version:         Version,
		RunnerPublicKey: pubB64,
	})
	if err != nil {
		return auth.Profile{}, nil, err
	}

	prof := auth.Profile{
		ServerURL:        base,
		RuntimeID:        pair.Runtime.ID,
		RunnerCredential: pair.RunnerCredential,
		DeviceName:       deviceName,
		Hostname:         host,
		PairedAt:         time.Now().UTC(),
		RunnerPublicKey:  pubB64,
		RunnerPrivateKey: privB64,
	}
	return prof, pair, nil
}

// pairRequest mirrors the wire shape of server/internal/api/runtime.
// Re-declared here so the daemon doesn't import a server-internal
// package — keeps the wire schema as the only coupling.
type pairRequest struct {
	PairingToken    string `json:"pairing_token"`
	Hostname        string `json:"hostname"`
	Version         string `json:"version"`
	RunnerPublicKey string `json:"runner_public_key,omitempty"`
}

type pairRuntime struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Liveness string `json:"liveness"`
}

type pairResponse struct {
	Runtime          pairRuntime `json:"runtime"`
	RunnerCredential string      `json:"runner_credential"`
}

// pairWithServer issues POST /api/v1/runtimes/pair. Returns the parsed
// response on success; on non-2xx, returns an error containing the
// server's error code + message when present.
func pairWithServer(ctx context.Context, base string, req pairRequest) (*pairResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal pair request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/runtimes/pair", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "parsar-daemon/"+Version)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("server returned %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}
	var out pairResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("decode pair response: %w", err)
	}
	if out.Runtime.ID == "" || out.RunnerCredential == "" {
		return nil, fmt.Errorf("pair response missing runtime.id or runner_credential")
	}
	if out.Runtime.Type != "" && out.Runtime.Type != "agent_daemon" {
		// Refuse rather than take over the wrong runtime row if the
		// user pasted a non-agent_daemon pairing token by accident.
		return nil, fmt.Errorf("pair response runtime.type=%q, expected agent_daemon (was the token issued under the Agent Daemon tab?)", out.Runtime.Type)
	}
	return &out, nil
}

// normalizeServerURL rejects junk inputs and strips a trailing slash so
// concatenating "/api/v1/..." paths never produces "//".
func normalizeServerURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse --url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("--url must use http or https (got %q)", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("--url is missing a host")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
