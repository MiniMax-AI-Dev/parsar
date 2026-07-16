// Package config is the typed view of Parsar process configuration.
//
// Loading order (later sources override earlier):
//
//  1. Built-in defaults (Default()).
//  2. Optional YAML file at PARSAR_CONFIG_FILE (or explicit path
//     to LoadFromFile). When PARSAR_CONFIG_FILE is unset, NO file
//     is read — no fallback to `./config.yaml` (see AGENTS.md
//     no-CWD rule).
//  3. Environment variables.
//
// The package itself does NOT call os.Getenv; callers pass an
// `env func(string) string` so tests stay deterministic without
// mutating process environment.
//
// Secrets (Secret.MasterKey, etc.) have no
// Marshal overrides, so a stray log.Printf("%+v", cfg) leaks them —
// callers must never log Config. Use Redacted() instead.
package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Auth     AuthConfig     `yaml:"auth"`
	Secret   SecretConfig   `yaml:"secret"`
	Gateway  GatewayConfig  `yaml:"gateway"`
	Model    ModelConfig    `yaml:"model"`
	Sandbox  SandboxConfig  `yaml:"sandbox"`
	Audit    AuditConfig    `yaml:"audit"`
	Storage  StorageConfig  `yaml:"storage"`
}

type ServerConfig struct {
	// Addr is the TCP listen address. Default ":8080". Env PARSAR_ADDR.
	Addr string `yaml:"addr"`

	// PublicURL is the absolute base URL Parsar is reachable at.
	// Required in production (used to build OAuth callback URLs).
	// Env PARSAR_PUBLIC_URL.
	PublicURL string `yaml:"public_url"`

	// DataDir is the on-disk root for runtime state. MUST live
	// outside the repo / CWD. Default "~/.parsar" expanded at
	// load time. Env PARSAR_DATA_DIR.
	DataDir string `yaml:"data_dir"`
}

type DatabaseConfig struct {
	// URL is the Postgres connection string. Required in production.
	// Env DATABASE_URL.
	URL string `yaml:"url"`
}

// AuthConfig groups auth-related toggles. DevAuth gates the
// X-Parsar-Dev-User-ID middleware shim; Load() refuses
// DevAuth=true under the production profile.
type AuthConfig struct {
	DevAuth bool         `yaml:"dev_auth"`
	Cookie  CookieConfig `yaml:"cookie"`

	// PlatformAdminUserIDs is the comma-separated allowlist of user
	// UUIDs that bypass workspace membership checks and act
	// as owner anywhere. Empty (default) means no platform admins.
	// Env PARSAR_PLATFORM_ADMIN_USER_IDS.
	PlatformAdminUserIDs []string `yaml:"platform_admin_user_ids"`
}

type CookieConfig struct {
	// Secure controls the Secure attribute on the session cookie.
	// Load derives it from the deployment profile.
	Secure bool `yaml:"secure"`
}

// SecretConfig holds the AES-GCM master key used to wrap
// workspace-scoped secrets.
type SecretConfig struct {
	// MasterKey is the AES-256 wrap key (any non-empty string is
	// accepted; production SHOULD use 32+ chars from `openssl rand
	// -hex 32`). Required in production. Env PARSAR_MASTER_KEY.
	MasterKey string `yaml:"master_key"`
}

type GatewayConfig struct {
	Feishu FeishuConfig `yaml:"feishu"`
}

// FeishuConfig is the Feishu (Lark) OIDC + event-subscription
// settings. Env names match the existing
// server/internal/auth/feishu package.
//
// Mock mode (PARSAR_FEISHU_MOCK=true) replaces the real client
// with a stub returning a deterministic user profile; production
// MUST leave it false.
type FeishuConfig struct {
	Mock              bool   `yaml:"mock"`
	AppID             string `yaml:"app_id"`
	AppSecret         string `yaml:"app_secret"`
	RedirectURI       string `yaml:"redirect_uri"`
	Scope             string `yaml:"scope"`
	AuthorizeBase     string `yaml:"authorize_base"`
	APIBase           string `yaml:"api_base"`
	VerificationToken string `yaml:"verification_token"`
	EncryptKey        string `yaml:"encrypt_key"`
	LoginRedirectURL  string `yaml:"login_redirect_url"`
}

// ModelConfig is process-level model-provider plumbing. The actual
// provider + model rows live in Postgres (workspace-scoped).
type ModelConfig struct {
	// OpenCodeBin is the path to the opencode CLI binary used by
	// the OpenCode Local Connector. Empty = use $PATH. Env
	// PARSAR_OPENCODE_BIN.
	OpenCodeBin string `yaml:"opencode_bin"`
}

// SandboxConfig drives the OpenCode runner provider selection.
// Runner=local (default) forks opencode on the host.
// Runner=sandbox routes through the E2B sandbox provider (E2B API
// key is read inside the e2b package via the same env merge). The
// pre-warmed sandbox pool is admin-managed via the Runtime page.
type SandboxConfig struct {
	// Runner picks the OpenCode runner provider: "local" or
	// "sandbox". Default "local". Env PARSAR_OPENCODE_RUNNER.
	Runner string `yaml:"runner"`
}

// StorageConfig groups object-storage backends. OSS is OFF by
// default — every field defaults to empty and OSS-dependent features
// surface "OSS not configured" rather than failing server boot.
type StorageConfig struct {
	OSS OSSConfig `yaml:"oss"`
	// BlobBackend selects the capability-blob backend: "pg" (default,
	// stores zips in Postgres — zero external infra) or "oss" (Aliyun
	// OSS; requires storage.oss.* configured). Env PARSAR_BLOB_BACKEND.
	BlobBackend string `yaml:"blob_backend"`
}

// OSSConfig is the Aliyun OSS connection knobs. Fields map 1:1 to
// EnvOSS* in load.go. Validation lives in storage/oss.Config.Validate;
// here we only check syntactic plausibility. An entirely-empty
// OSSConfig is valid: it means "OSS is off".
type OSSConfig struct {
	Region          string `yaml:"region"`
	Endpoint        string `yaml:"endpoint"`
	Bucket          string `yaml:"bucket"`
	AccessKeyID     string `yaml:"access_key_id"`
	AccessKeySecret string `yaml:"access_key_secret"`
	BaseURL         string `yaml:"base_url"`
}

// AuditConfig groups audit-pipeline knobs. Two inputs feed the same
// audit.Ingester / audit_records table: the in-process
// store.emitAuditEvent path and the embedded OTLP receiver. OTLP is
// additive, not a replacement.
type AuditConfig struct {
	// OTLP configures the embedded OTLP/HTTP receiver. Disabled by
	// default.
	OTLP OTLPConfig `yaml:"otlp"`
}

type OTLPConfig struct {
	// Enabled toggles the embedded receiver. Default false. Env
	// PARSAR_AUDIT_OTLP_ENABLED.
	Enabled bool `yaml:"enabled"`

	// Addr is the TCP listen address. Default ":4318". Env
	// PARSAR_AUDIT_OTLP_ADDR.
	Addr string `yaml:"addr"`

	// SigningKey is the HMAC-SHA256 secret used to mint + verify
	// per-agent-run Bearer tokens producers carry on every OTLP
	// request. The receiver REFUSES to start without a non-empty
	// key. Production MUST set this to a strong random value; dev
	// profile lets cmd/server substitute a stable dev constant when
	// empty (mirrors the master-key path). Env
	// PARSAR_AUDIT_OTLP_SIGNING_KEY.
	SigningKey string `yaml:"signing_key"`

	// ExternalEndpoint is the URL sandboxes / external tools use to
	// reach this receiver — distinct from Addr (local bind). Used
	// at spawn time to construct OTEL_EXPORTER_OTLP_ENDPOINT
	// injected into sandboxes. Empty disables spawn-time OTel env
	// injection; operators must set it explicitly because the right
	// value depends on deployment topology. Env
	// PARSAR_AUDIT_OTLP_EXTERNAL_ENDPOINT.
	ExternalEndpoint string `yaml:"external_endpoint"`

	// FanoutEndpoint is the URL of a customer-owned OTel collector
	// that also receives a copy of every audit event. Empty = no
	// fan-out. When set, the canonical PostgresSink still writes to
	// audit_records; failures to deliver to the collector are
	// logged but never block the canonical write. Env
	// PARSAR_AUDIT_OTLP_FANOUT_ENDPOINT.
	FanoutEndpoint string `yaml:"fanout_endpoint"`
}

// Default returns the built-in defaults applied before file/env
// overrides.
func Default() Config {
	return Config{
		Server: ServerConfig{
			Addr:    ":8080",
			DataDir: defaultDataDir(),
		},
		Sandbox: SandboxConfig{
			Runner: "local",
		},
		Audit: AuditConfig{
			OTLP: OTLPConfig{
				Enabled: false,
				Addr:    ":4318",
			},
		},
		Storage: StorageConfig{
			BlobBackend: "pg",
		},
		Gateway: GatewayConfig{
			Feishu: FeishuConfig{
				Scope:         "contact:user.id:readonly contact:user.base:readonly contact:user.email:readonly",
				AuthorizeBase: "https://accounts.feishu.cn",
				APIBase:       "https://open.feishu.cn",
			},
		},
	}
}

// Profile is the deployment posture inferred from the loaded config.
// Production-only validation (master key required, dev_auth
// forbidden, secure cookie required) fires when Profile()==ProfileProd.
type Profile string

const (
	// ProfileDev is the local-loop posture, entered when ANY dev
	// signal is present: PARSAR_DEV_AUTH=true, PARSAR_FEISHU_MOCK=true,
	// or a loopback PARSAR_PUBLIC_URL (127.0.0.0/8, localhost, ::1).
	// The loopback signal is what lets real-Feishu OIDC be tested on
	// http://127.0.0.1 without tripping the production secure-cookie /
	// master-key checks — loopback traffic never leaves the host, so
	// relaxing the prod posture there is sound (see isLoopback).
	ProfileDev Profile = "dev"

	// ProfileProd is the deployment posture.
	ProfileProd Profile = "prod"
)

// Profile returns the inferred posture. Any single dev signal flips
// the whole process into dev.
func (c Config) Profile() Profile {
	if c.Auth.DevAuth || c.Gateway.Feishu.Mock || isLoopback(c.Server.PublicURL) {
		return ProfileDev
	}
	return ProfileProd
}

// isLoopback reports whether publicURL points at the local host.
// Both http and https loopback count as dev: loopback traffic never
// crosses the network, so the production secure-cookie requirement is
// moot regardless of scheme. Empty, unparseable, or any non-loopback
// host returns false, so a real public deployment (real domain) keeps
// the strict prod posture — this relaxation is self-limiting.
func isLoopback(publicURL string) bool {
	raw := strings.TrimSpace(publicURL)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname() // strips the port and IPv6 brackets
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() // 127.0.0.0/8 and ::1
}

// BuildPublicURL returns an absolute URL for a Parsar-owned path.
func (c Config) BuildPublicURL(path string) string {
	publicURL := strings.TrimSpace(c.Server.PublicURL)
	publicURL = strings.TrimRight(publicURL, "/")
	if publicURL != "" {
		return publicURL + ensureLeadingSlash(path)
	}
	if c.Profile() == ProfileDev {
		return "http://127.0.0.1:18080" + ensureLeadingSlash(path)
	}
	panic(fmt.Sprintf("%s is required in production to build public URLs", EnvPublicURL))
}

func ensureLeadingSlash(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if strings.HasPrefix(path, "/") {
		return path
	}
	return "/" + path
}

const defaultStartupTimeout = 30 * time.Second

var _ = defaultStartupTimeout
