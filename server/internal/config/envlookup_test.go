package config

import "testing"

func TestAsEnvLookupOverridesBase(t *testing.T) {
	cfg := Default()
	cfg.Server.Addr = ":9999"
	cfg.Gateway.Feishu.AppID = "cli_yaml"
	cfg.Gateway.Feishu.Mock = true

	base := envMap{
		EnvAddr:                  ":1111",
		EnvFeishuAppID:           "cli_base",
		EnvFeishuAppSecret:       "secret_from_base", // not modeled in cfg, falls through
		"PARSAR_UNRELATED_KEY": "value-from-base",
	}
	lookup := cfg.AsEnvLookup(base.get)

	if got := lookup(EnvAddr); got != ":9999" {
		t.Fatalf("Addr lookup = %q, want :9999 (cfg should win)", got)
	}
	if got := lookup(EnvFeishuAppID); got != "cli_yaml" {
		t.Fatalf("AppID lookup = %q, want cli_yaml (cfg should win)", got)
	}
	if got := lookup(EnvFeishuMock); got != "true" {
		t.Fatalf("FeishuMock lookup = %q, want true", got)
	}
	if got := lookup(EnvFeishuAppSecret); got != "secret_from_base" {
		t.Fatalf("AppSecret lookup = %q, want fall-through from base", got)
	}
	if got := lookup("PARSAR_UNRELATED_KEY"); got != "value-from-base" {
		t.Fatalf("unrelated key lost: %q", got)
	}
}

func TestAsEnvLookupNilBase(t *testing.T) {
	cfg := Default()
	cfg.Server.Addr = ":7777"
	lookup := cfg.AsEnvLookup(nil)

	if got := lookup(EnvAddr); got != ":7777" {
		t.Fatalf("Addr = %q, want :7777", got)
	}
	if got := lookup("MISSING_KEY"); got != "" {
		t.Fatalf("missing key with nil base should be \"\", got %q", got)
	}
}

func TestAsEnvLookupEmptyCfgFieldFallsThrough(t *testing.T) {
	cfg := Default()
	base := envMap{EnvPublicURL: "https://from-base.example.com"}
	lookup := cfg.AsEnvLookup(base.get)
	if got := lookup(EnvPublicURL); got != "https://from-base.example.com" {
		t.Fatalf("empty cfg field should fall through to base, got %q", got)
	}
}
