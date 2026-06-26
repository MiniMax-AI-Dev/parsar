package main

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth/feishu"
)

func TestDecideFeishuStartup(t *testing.T) {
	cases := []struct {
		name        string
		env         map[string]string
		wantMode    feishuStartupMode
		wantFatal   bool
		wantWarning bool
	}{
		{
			name: "dev mock",
			env: map[string]string{
				feishu.EnvMock: "true",
			},
			wantMode: feishuStartupModeDev,
		},
		{
			name:      "prod missing config",
			env:       map[string]string{},
			wantMode:  feishuStartupModeProd,
			wantFatal: true,
		},
		{
			name: "prod configured warns when cookie not secure",
			env: map[string]string{
				feishu.EnvAppID:             "cli_x",
				feishu.EnvAppSecret:         "secret",
				feishu.EnvRedirectURI:       "https://parsar.example/api/v1/auth/feishu/callback",
				feishu.EnvVerificationToken: "verify-token",
			},
			wantMode:    feishuStartupModeProd,
			wantWarning: true,
		},
		{
			name: "prod configured secure cookies",
			env: map[string]string{
				feishu.EnvAppID:             "cli_x",
				feishu.EnvAppSecret:         "secret",
				feishu.EnvRedirectURI:       "https://parsar.example/api/v1/auth/feishu/callback",
				feishu.EnvVerificationToken: "verify-token",
				"PARSAR_COOKIE_SECURE":    "true",
			},
			wantMode: feishuStartupModeProd,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideFeishuStartup(func(k string) string { return tc.env[k] })
			if got.Mode != tc.wantMode {
				t.Fatalf("Mode = %q, want %q", got.Mode, tc.wantMode)
			}
			if (got.FatalMessage != "") != tc.wantFatal {
				t.Fatalf("FatalMessage = %q, want fatal=%v", got.FatalMessage, tc.wantFatal)
			}
			if got.RegisterHandlers == tc.wantFatal {
				t.Fatalf("RegisterHandlers = %v, want fatal inverse", got.RegisterHandlers)
			}
			if got.CookieSecureWarning != tc.wantWarning {
				t.Fatalf("CookieSecureWarning = %v, want %v", got.CookieSecureWarning, tc.wantWarning)
			}
		})
	}
}

// TestFanoutEndpointHost guards host-only output for the "audit OTLP
// fan-out wired" startup log so an internal collector URL does not show
// up in INFO logs. Unparseable input must still produce a non-empty label.
func TestFanoutEndpointHost(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"https base", "https://otel.example.com:4318", "otel.example.com:4318"},
		{"http base", "http://otel:4318", "otel:4318"},
		{"with path", "https://otel.example.com:4318/v1/logs", "otel.example.com:4318"},
		{"with query", "https://otel.example.com:4318/v1/logs?token=x", "otel.example.com:4318"},
		{"surrounding whitespace", "  https://otel.example.com:4318  ", "otel.example.com:4318"},
		{"missing scheme", "otel.example.com:4318", "<unparseable>"},
		{"empty", "", "<unparseable>"},
		{"garbage", "::::::", "<unparseable>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fanoutEndpointHost(tc.in); got != tc.want {
				t.Errorf("fanoutEndpointHost(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDefaultFeishuSharedBotEnv(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want feishuSharedBotEnv
	}{
		{
			name: "falls back to platform feishu app when default unset",
			env: map[string]string{
				"PARSAR_FEISHU_APP_ID":              " cli_oauth ",
				"PARSAR_FEISHU_APP_SECRET":          " oauth-secret ",
				"PARSAR_FEISHU_BOT_OPEN_ID":         " ou_legacy_bot ",
				"PARSAR_FEISHU_DEFAULT_BOT_OPEN_ID": "",
			},
			want: feishuSharedBotEnv{appID: "cli_oauth", appSecret: "oauth-secret", botOpenID: "ou_legacy_bot"},
		},
		{
			name: "explicit default bot overrides platform app",
			env: map[string]string{
				"PARSAR_FEISHU_APP_ID":                 "cli_oauth",
				"PARSAR_FEISHU_APP_SECRET":             "oauth-secret",
				"PARSAR_FEISHU_BOT_OPEN_ID":            "ou_legacy_bot",
				"PARSAR_FEISHU_DEFAULT_BOT_APP_ID":     " cli_default ",
				"PARSAR_FEISHU_DEFAULT_BOT_APP_SECRET": " default-secret ",
				"PARSAR_FEISHU_DEFAULT_BOT_OPEN_ID":    " ou_default_bot ",
			},
			want: feishuSharedBotEnv{appID: "cli_default", appSecret: "default-secret", botOpenID: "ou_default_bot"},
		},
		{
			name: "partial explicit default does not silently mix app id and secret",
			env: map[string]string{
				"PARSAR_FEISHU_APP_ID":                 "cli_oauth",
				"PARSAR_FEISHU_APP_SECRET":             "oauth-secret",
				"PARSAR_FEISHU_DEFAULT_BOT_APP_ID":     "cli_default",
				"PARSAR_FEISHU_DEFAULT_BOT_APP_SECRET": "",
			},
			want: feishuSharedBotEnv{appID: "cli_default", appSecret: "", botOpenID: ""},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := defaultFeishuSharedBotEnv(func(k string) string { return tc.env[k] })
			if got != tc.want {
				t.Fatalf("defaultFeishuSharedBotEnv() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveRuntimeProfile(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		managed bool
		want    string
	}{
		{name: "default oss", env: map[string]string{}, want: "oss"},
		{name: "managed provider auto", env: map[string]string{}, managed: true, want: "managed"},
		{name: "explicit managed", env: map[string]string{envRuntimeProfile: "managed"}, want: "managed"},
		{name: "explicit oss overrides provider", env: map[string]string{envRuntimeProfile: "oss"}, managed: true, want: "oss"},
		{name: "explicit selfhost", env: map[string]string{envRuntimeProfile: "SELFHOST"}, managed: true, want: "selfhost"},
		{name: "invalid falls back to provider", env: map[string]string{envRuntimeProfile: "internal"}, managed: true, want: "managed"},
		{name: "invalid without provider falls back oss", env: map[string]string{envRuntimeProfile: "internal"}, want: "oss"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveRuntimeProfile(envMap(tc.env), tc.managed); got != tc.want {
				t.Fatalf("resolveRuntimeProfile() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestConfiguredSandboxProber(t *testing.T) {
	if err := (configuredSandboxProber{}).Ping(context.Background()); err != nil {
		t.Fatalf("healthy configured prober returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (configuredSandboxProber{}).Ping(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled prober error = %v, want context.Canceled", err)
	}
}
