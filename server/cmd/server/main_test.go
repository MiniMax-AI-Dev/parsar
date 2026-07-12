package main

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth/feishu"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/config"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestDecideFeishuStartup(t *testing.T) {
	cases := []struct {
		name        string
		env         map[string]string
		wantMode    feishuStartupMode
		wantOAuth   bool
		wantWebhook bool
		wantWarning bool
	}{
		{
			name: "dev mock",
			env: map[string]string{
				feishu.EnvMock: "true",
			},
			wantMode:    feishuStartupModeMock,
			wantOAuth:   true,
			wantWebhook: true,
		},
		{
			name:     "missing config disables optional feishu routes",
			env:      map[string]string{},
			wantMode: feishuStartupModeDisabled,
		},
		{
			name: "prod oauth configured warns when cookie not secure",
			env: map[string]string{
				feishu.EnvAppID:       "cli_x",
				feishu.EnvAppSecret:   "secret",
				feishu.EnvRedirectURI: "https://parsar.example/api/v1/auth/feishu/callback",
			},
			wantMode:    feishuStartupModeProd,
			wantOAuth:   true,
			wantWarning: true,
		},
		{
			name: "prod oauth configured secure cookies",
			env: map[string]string{
				feishu.EnvAppID:        "cli_x",
				feishu.EnvAppSecret:    "secret",
				feishu.EnvRedirectURI:  "https://parsar.example/api/v1/auth/feishu/callback",
				"PARSAR_COOKIE_SECURE": "true",
			},
			wantMode:  feishuStartupModeProd,
			wantOAuth: true,
		},
		{
			name: "prod webhook-only config enables webhook security",
			env: map[string]string{
				feishu.EnvVerificationToken: "verify-token",
			},
			wantMode:    feishuStartupModeProd,
			wantWebhook: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideFeishuStartup(func(k string) string { return tc.env[k] })
			if got.Mode != tc.wantMode {
				t.Fatalf("Mode = %q, want %q", got.Mode, tc.wantMode)
			}
			if got.RegisterOAuthHandlers != tc.wantOAuth {
				t.Fatalf("RegisterOAuthHandlers = %v, want %v", got.RegisterOAuthHandlers, tc.wantOAuth)
			}
			if got.RegisterWebhookSecurity != tc.wantWebhook {
				t.Fatalf("RegisterWebhookSecurity = %v, want %v", got.RegisterWebhookSecurity, tc.wantWebhook)
			}
			if got.CookieSecureWarning != tc.wantWarning {
				t.Fatalf("CookieSecureWarning = %v, want %v", got.CookieSecureWarning, tc.wantWarning)
			}
		})
	}
}

func TestBuildAuthProviderRegistry(t *testing.T) {
	cfg := config.Config{Server: config.ServerConfig{PublicURL: "https://parsar.example"}}
	cases := []struct {
		name             string
		env              map[string]string
		wantFeishuEnable bool
		wantMissing      []string
	}{
		{
			name: "default password only and feishu diagnostic disabled",
			env:  map[string]string{},
			wantMissing: []string{
				feishu.EnvAppID,
				feishu.EnvAppSecret,
				feishu.EnvRedirectURI,
			},
		},
		{
			name: "feishu oauth configured",
			env: map[string]string{
				feishu.EnvAppID:       "cli_x",
				feishu.EnvAppSecret:   "secret",
				feishu.EnvRedirectURI: "https://parsar.example/api/v1/auth/feishu/callback",
			},
			wantFeishuEnable: true,
		},
		{
			name: "mock feishu configured",
			env: map[string]string{
				feishu.EnvMock: "true",
			},
			wantFeishuEnable: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := func(k string) string { return tc.env[k] }
			registry := buildAuthProviderRegistry(env, cfg, decideFeishuStartup(env))
			if len(registry.Providers) != 2 {
				t.Fatalf("provider count = %d, want 2", len(registry.Providers))
			}
			if registry.Providers[0].ID != "password" || !registry.Providers[0].Enabled {
				t.Fatalf("password provider = %+v, want enabled password", registry.Providers[0])
			}
			feishuProvider := registry.Providers[1]
			if feishuProvider.ID != "feishu" {
				t.Fatalf("second provider id = %q, want feishu", feishuProvider.ID)
			}
			if feishuProvider.Enabled != tc.wantFeishuEnable {
				t.Fatalf("feishu enabled = %v, want %v", feishuProvider.Enabled, tc.wantFeishuEnable)
			}
			if !equalStringSlices(feishuProvider.MissingEnv, tc.wantMissing) {
				t.Fatalf("feishu missing env = %#v, want %#v", feishuProvider.MissingEnv, tc.wantMissing)
			}
			if feishuProvider.CallbackURL == "" {
				t.Fatal("feishu callback URL must be populated for admin diagnostics")
			}
		})
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

func TestWorkspaceFeishuWorkersFollowStoredConnectorConfiguration(t *testing.T) {
	dbStore := store.New(nil)

	t.Run("missing master key skips workers", func(t *testing.T) {
		env := envMap(nil)
		manager, err := buildFeishuWebSocketManager(env, dbStore, nil, "")
		if err != nil {
			t.Fatalf("buildFeishuWebSocketManager() error = %v", err)
		}
		if manager != nil {
			t.Fatal("buildFeishuWebSocketManager() returned manager without master key")
		}
		worker, err := buildFeishuOutboundWorker(env, dbStore, nil, nil, "")
		if err != nil {
			t.Fatalf("buildFeishuOutboundWorker() error = %v", err)
		}
		if worker != nil {
			t.Fatal("buildFeishuOutboundWorker() returned worker without master key")
		}
	})

	t.Run("master key starts workers without feature flags", func(t *testing.T) {
		env := envMap(map[string]string{
			"PARSAR_MASTER_KEY": "0000000000000000000000000000000000000000000000000000000000000000",
		})
		manager, err := buildFeishuWebSocketManager(env, dbStore, nil, "")
		if err != nil {
			t.Fatalf("buildFeishuWebSocketManager() error = %v", err)
		}
		if manager == nil {
			t.Fatal("buildFeishuWebSocketManager() returned nil with master key")
		}
		worker, err := buildFeishuOutboundWorker(env, dbStore, nil, nil, "")
		if err != nil {
			t.Fatalf("buildFeishuOutboundWorker() error = %v", err)
		}
		if worker == nil {
			t.Fatal("buildFeishuOutboundWorker() returned nil with master key")
		}
	})
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
