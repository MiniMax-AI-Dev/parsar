package main

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/config"
)

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
		"POD_IP":                          "10.1.2.3",
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
