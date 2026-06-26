package transport_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/transport"
)

func TestBootstrapSendsBearerAndDeviceID(t *testing.T) {
	var sawAuth, sawCT, sawDeviceID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent-daemon/bootstrap" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %q", r.Method)
		}
		sawAuth = r.Header.Get("Authorization")
		sawCT = r.Header.Get("Content-Type")
		var body struct {
			DeviceID string `json:"device_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		sawDeviceID = body.DeviceID
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_id":         "rt_abc",
			"workspace_id":      "ws_xyz",
			"ws_url":            "wss://example/agent-daemon/ws",
			"heartbeat_seconds": 15,
			"protocol_version":  "0.1.0",
		})
	}))
	defer srv.Close()

	resp, err := transport.Bootstrap(context.Background(), srv.URL, "rt_abc", "secret123", "0.0.0-dev")
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if resp.DeviceID != "rt_abc" || resp.WorkspaceID != "ws_xyz" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if resp.WSURL != "wss://example/agent-daemon/ws" {
		t.Errorf("ws_url = %q", resp.WSURL)
	}
	if got := resp.HeartbeatInterval().Seconds(); got != 15 {
		t.Errorf("HeartbeatInterval = %vs, want 15s", got)
	}
	if sawAuth != "Bearer secret123" {
		t.Errorf("Authorization = %q, want Bearer secret123", sawAuth)
	}
	if sawCT != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", sawCT)
	}
	if sawDeviceID != "rt_abc" {
		t.Errorf("body.device_id = %q, want rt_abc", sawDeviceID)
	}
}

func TestBootstrapHeartbeatIntervalDefaultsToFifteen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_id":         "rt",
			"workspace_id":      "ws",
			"ws_url":            "",
			"heartbeat_seconds": 0, // server forgot to set it
		})
	}))
	defer srv.Close()

	resp, err := transport.Bootstrap(context.Background(), srv.URL, "rt", "c", "v")
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if got := resp.HeartbeatInterval().Seconds(); got != 15 {
		t.Errorf("HeartbeatInterval = %vs, want 15s default", got)
	}
}

func TestBootstrapNonSuccessSurfacesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad_credential","detail":"wrong key"}`))
	}))
	defer srv.Close()

	_, err := transport.Bootstrap(context.Background(), srv.URL, "rt", "c", "v")
	if err == nil {
		t.Fatal("Bootstrap returned nil error on 401")
	}
	if !strings.Contains(err.Error(), "bad_credential") || !strings.Contains(err.Error(), "401") {
		t.Errorf("error %q missing 'bad_credential' or '401'", err.Error())
	}
}

func TestBootstrapRejectsEmptyInputs(t *testing.T) {
	cases := []struct{ name, base, device, cred string }{
		{"empty base", "", "rt", "c"},
		{"empty device", "https://x", "", "c"},
		{"empty cred", "https://x", "rt", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transport.Bootstrap(context.Background(), tc.base, tc.device, tc.cred, "v")
			if err == nil {
				t.Fatal("Bootstrap accepted empty input")
			}
		})
	}
}

func TestDeriveWSURLPrefersAbsolute(t *testing.T) {
	got, err := transport.DeriveWSURL(transport.BootstrapResponse{WSURL: "wss://prod/agent-daemon/ws"}, "https://anything")
	if err != nil {
		t.Fatalf("DeriveWSURL: %v", err)
	}
	if got != "wss://prod/agent-daemon/ws" {
		t.Errorf("got %q, want wss://prod/agent-daemon/ws", got)
	}
}

func TestDeriveWSURLFallsBackToServerBase(t *testing.T) {
	cases := []struct{ base, want string }{
		{"https://parsar.example.com", "wss://parsar.example.com/agent-daemon/ws"},
		{"http://localhost:3000", "ws://localhost:3000/agent-daemon/ws"},
		{"http://localhost:3000/", "ws://localhost:3000/agent-daemon/ws"},
		{"https://parsar.example.com/api", "wss://parsar.example.com/api/agent-daemon/ws"},
	}
	for _, tc := range cases {
		got, err := transport.DeriveWSURL(transport.BootstrapResponse{WSURL: ""}, tc.base)
		if err != nil {
			t.Errorf("DeriveWSURL(%q): %v", tc.base, err)
			continue
		}
		if got != tc.want {
			t.Errorf("DeriveWSURL(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

func TestDeriveWSURLRejectsRelativeWSURL(t *testing.T) {
	_, err := transport.DeriveWSURL(transport.BootstrapResponse{WSURL: "/agent-daemon/ws"}, "https://x")
	if err == nil {
		t.Fatal("DeriveWSURL accepted relative ws_url")
	}
}

func TestDeriveWSURLRejectsBadScheme(t *testing.T) {
	_, err := transport.DeriveWSURL(transport.BootstrapResponse{WSURL: ""}, "ftp://example")
	if err == nil {
		t.Fatal("DeriveWSURL accepted ftp scheme")
	}
}
