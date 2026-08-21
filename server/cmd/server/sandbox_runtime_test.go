package main

import (
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/config"
)

func TestResolveAgentDaemonSandboxTTL(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want time.Duration
	}{
		{name: "unset uses provider default", env: nil, want: 0},
		{name: "duration", env: map[string]string{"AGENT_DAEMON_SANDBOX_TTL": "90m"}, want: 90 * time.Minute},
		{name: "duration takes precedence", env: map[string]string{
			"AGENT_DAEMON_SANDBOX_TTL":       "45m",
			"AGENT_DAEMON_SANDBOX_TTL_HOURS": "23",
		}, want: 45 * time.Minute},
		{name: "legacy hours", env: map[string]string{"AGENT_DAEMON_SANDBOX_TTL_HOURS": "6"}, want: 6 * time.Hour},
		{name: "provider specific maximum is not imposed", env: map[string]string{"AGENT_DAEMON_SANDBOX_TTL": "720h"}, want: 720 * time.Hour},
		{name: "invalid duration", env: map[string]string{"AGENT_DAEMON_SANDBOX_TTL": "tomorrow"}, want: 0},
		{name: "non-positive duration", env: map[string]string{"AGENT_DAEMON_SANDBOX_TTL": "0s"}, want: 0},
		{name: "sub-second duration", env: map[string]string{"AGENT_DAEMON_SANDBOX_TTL": "500ms"}, want: 0},
		{name: "fractional second is normalized", env: map[string]string{"AGENT_DAEMON_SANDBOX_TTL": "1500ms"}, want: time.Second},
		{name: "legacy hours overflow", env: map[string]string{"AGENT_DAEMON_SANDBOX_TTL_HOURS": "596524"}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveAgentDaemonSandboxTTL(envMap(tt.env)); got != tt.want {
				t.Fatalf("resolveAgentDaemonSandboxTTL() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestResolveAgentDaemonSandboxAutoRenew(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
		want bool
	}{
		{name: "unset defaults on", want: true},
		{name: "enabled", raw: "true", want: true},
		{name: "disabled", raw: "false", want: false},
		{name: "invalid defaults on", raw: "sometimes", want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAgentDaemonSandboxAutoRenew(envMap(map[string]string{
				"AGENT_DAEMON_SANDBOX_AUTO_RENEW": tt.raw,
			}))
			if got != tt.want {
				t.Fatalf("resolveAgentDaemonSandboxAutoRenew() = %v, want %v", got, tt.want)
			}
		})
	}
}

// envMap turns a Go map into the `func(string) string` shape that
// buildAgentDaemonSandboxProvider etc. expect.
func envMap(m map[string]string) func(string) string {
	return func(k string) string {
		if v, ok := m[k]; ok {
			return v
		}
		return ""
	}
}

func TestResolveAgentDaemonOwnerURLPrefersExplicitValue(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Addr = ":9090"
	cfg.Server.PublicURL = "https://public.example.com"
	got, err := resolveAgentDaemonOwnerURL(envMap(map[string]string{
		"PARSAR_AGENT_DAEMON_OWNER_URL": "http://explicit-owner:8080/",
		"POD_IP":                        "10.1.2.3",
	}), cfg)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "http://explicit-owner:8080" {
		t.Fatalf("owner URL = %q, want explicit value without trailing slash", got)
	}
}

func TestResolveAgentDaemonOwnerURLUsesPodIPAndListenPort(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Addr = ":9090"
	cfg.Server.PublicURL = "https://public.example.com"
	got, err := resolveAgentDaemonOwnerURL(envMap(map[string]string{
		"POD_IP": "10.1.2.3",
	}), cfg)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "http://10.1.2.3:9090" {
		t.Fatalf("owner URL = %q, want Pod IP + listen port", got)
	}
}

func TestResolveAgentDaemonOwnerURLBracketsIPv6PodIP(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Addr = "0.0.0.0:7070"
	got, err := resolveAgentDaemonOwnerURL(envMap(map[string]string{
		"POD_IP": "fd00::10",
	}), cfg)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "http://[fd00::10]:7070" {
		t.Fatalf("owner URL = %q, want bracketed IPv6 Pod IP", got)
	}
}

// PublicURL points at the ingress and gets load-balanced across replicas,
// which would trip stale_owner on every cross-pod forward. Fail fast instead
// of falling back to it (or 127.0.0.1) when neither POD_IP nor
// PARSAR_AGENT_DAEMON_OWNER_URL is set.
func TestResolveAgentDaemonOwnerURLFailsFastWhenUnconfigured(t *testing.T) {
	cfg := config.Default()
	cfg.Server.Addr = ":9090"
	cfg.Server.PublicURL = "https://parsar.example.com/base/"
	if _, err := resolveAgentDaemonOwnerURL(envMap(nil), cfg); err == nil {
		t.Fatal("expected error when POD_IP and PARSAR_AGENT_DAEMON_OWNER_URL are unset, got nil")
	}

	cfg.Server.PublicURL = ""
	if _, err := resolveAgentDaemonOwnerURL(envMap(nil), cfg); err == nil {
		t.Fatal("expected error when nothing is configured, got nil")
	}
}

func TestResolveListenPort(t *testing.T) {
	cases := map[string]string{
		"":             "8080",
		":8081":        "8081",
		"0.0.0.0:8082": "8082",
		"[::]:8083":    "8083",
		"bad":          "8080",
	}
	for input, want := range cases {
		if got := resolveListenPort(input); got != want {
			t.Fatalf("resolveListenPort(%q) = %q, want %q", input, got, want)
		}
	}
}
