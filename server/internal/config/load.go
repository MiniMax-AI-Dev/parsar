package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Source labels the origin of a Config for diagnostic messages.
type Source string

const (
	SourceDefault    Source = "default"
	SourceEnv        Source = "env"
	SourceFileAndEnv Source = "file+env"
)

type LoadResult struct {
	Config   Config
	FilePath string
	Source   Source
}

type EnvFunc func(string) string

type FileReader func(path string) ([]byte, error)

// EnvConfigFile points at the optional YAML config file. When unset,
// Load skips file loading entirely — no fallback to `./config.yaml`
// (no-CWD rule).
const EnvConfigFile = "PARSAR_CONFIG_FILE"

// Env names — one place so docs, tests, and operators can grep.
const (
	EnvAddr           = "PARSAR_ADDR"
	EnvPublicURL      = "PARSAR_PUBLIC_URL"
	EnvDataDir        = "PARSAR_DATA_DIR"
	EnvDatabaseURL    = "DATABASE_URL"
	EnvDevAuth        = "PARSAR_DEV_AUTH"
	EnvCookieSecure   = "PARSAR_COOKIE_SECURE"
	EnvMasterKey      = "PARSAR_MASTER_KEY"
	EnvOpenCodeBin    = "PARSAR_OPENCODE_BIN"
	EnvOpenCodeRunner = "PARSAR_OPENCODE_RUNNER"

	// EnvPlatformAdminUserIDs lists user UUIDs that bypass workspace
	// membership checks. Comma-separated. Empty disables.
	EnvPlatformAdminUserIDs = "PARSAR_PLATFORM_ADMIN_USER_IDS"

	EnvLoginRedirectURL = "PARSAR_LOGIN_REDIRECT_URL"

	EnvFeishuMock              = "PARSAR_FEISHU_MOCK"
	EnvFeishuAppID             = "PARSAR_FEISHU_APP_ID"
	EnvFeishuAppSecret         = "PARSAR_FEISHU_APP_SECRET" // #nosec G101 -- env var name, not a credential
	EnvFeishuRedirectURI       = "PARSAR_FEISHU_REDIRECT_URI"
	EnvFeishuScope             = "PARSAR_FEISHU_SCOPE"
	EnvFeishuAuthorizeBase     = "PARSAR_FEISHU_AUTHORIZE_BASE"
	EnvFeishuAPIBase           = "PARSAR_FEISHU_API_BASE"
	EnvFeishuVerificationToken = "PARSAR_FEISHU_VERIFICATION_TOKEN"
	EnvFeishuEncryptKey        = "PARSAR_FEISHU_ENCRYPT_KEY" // #nosec G101 -- env var name, not a credential

	EnvAuditOTLPEnabled          = "PARSAR_AUDIT_OTLP_ENABLED"
	EnvAuditOTLPAddr             = "PARSAR_AUDIT_OTLP_ADDR"
	EnvAuditOTLPSigningKey       = "PARSAR_AUDIT_OTLP_SIGNING_KEY" // #nosec G101 -- env var name, not a credential
	EnvAuditOTLPExternalEndpoint = "PARSAR_AUDIT_OTLP_EXTERNAL_ENDPOINT"
	EnvAuditOTLPFanoutEndpoint   = "PARSAR_AUDIT_OTLP_FANOUT_ENDPOINT"

	// Aliyun OSS. Empty = OSS-backed features off.
	EnvOSSRegion          = "PARSAR_OSS_REGION"
	EnvOSSEndpoint        = "PARSAR_OSS_ENDPOINT"
	EnvOSSBucket          = "PARSAR_OSS_BUCKET"
	EnvOSSAccessKeyID     = "PARSAR_OSS_ACCESS_KEY_ID"     // #nosec G101 -- env var name, not a credential
	EnvOSSAccessKeySecret = "PARSAR_OSS_ACCESS_KEY_SECRET" // #nosec G101 -- env var name, not a credential
	EnvOSSBaseURL         = "PARSAR_OSS_BASE_URL"

	// Capability blob backend selector: "pg" (default) or "oss".
	EnvBlobBackend = "PARSAR_BLOB_BACKEND"
)

// ErrInvalidConfig wraps every validation failure so callers can
// `errors.Is(err, ErrInvalidConfig)`.
var ErrInvalidConfig = errors.New("config: invalid")

// Load resolves the merged Config from defaults + optional file + env.
//
//   - If env[EnvConfigFile] is non-empty, that path is read as YAML.
//     Path MUST be absolute or start with `~/`.
//   - Env vars override file values.
//   - Validate() fires production-only constraints when
//     Profile()==ProfileProd.
func Load(env EnvFunc, readFile FileReader) (LoadResult, error) {
	cfg := Default()
	source := SourceDefault
	filePath := ""

	if env != nil {
		if rawPath := strings.TrimSpace(env(EnvConfigFile)); rawPath != "" {
			abs, err := resolveConfigPath(rawPath)
			if err != nil {
				return LoadResult{}, fmt.Errorf("%w: %s=%q: %v", ErrInvalidConfig, EnvConfigFile, rawPath, err)
			}
			if readFile == nil {
				return LoadResult{}, fmt.Errorf("%w: file reader is nil but %s=%q was set", ErrInvalidConfig, EnvConfigFile, rawPath)
			}
			data, err := readFile(abs)
			if err != nil {
				return LoadResult{}, fmt.Errorf("%w: read %s: %v", ErrInvalidConfig, abs, err)
			}
			if err := mergeYAML(&cfg, data); err != nil {
				return LoadResult{}, fmt.Errorf("%w: parse %s: %v", ErrInvalidConfig, abs, err)
			}
			filePath = abs
			source = SourceFileAndEnv
		}
		applyEnv(&cfg, env)
		if source == SourceDefault {
			source = SourceEnv
		}
	}

	if err := cfg.Validate(); err != nil {
		return LoadResult{}, err
	}

	return LoadResult{Config: cfg, FilePath: filePath, Source: source}, nil
}

// applyEnv copies env values into cfg. Empty env values do NOT
// clear non-empty file values (so `unset FOO; restart` does not
// clobber config).
func applyEnv(cfg *Config, env EnvFunc) {
	stringSetter := func(envName string, dst *string) {
		if v := strings.TrimSpace(env(envName)); v != "" {
			*dst = v
		}
	}
	boolSetter := func(envName string, dst *bool) {
		if v := strings.TrimSpace(env(envName)); v != "" {
			parsed, ok := parseBool(v)
			if ok {
				*dst = parsed
			}
		}
	}
	csvSetter := func(envName string, dst *[]string) {
		v := strings.TrimSpace(env(envName))
		if v == "" {
			return
		}
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		*dst = out
	}

	stringSetter(EnvAddr, &cfg.Server.Addr)
	stringSetter(EnvPublicURL, &cfg.Server.PublicURL)
	stringSetter(EnvDataDir, &cfg.Server.DataDir)
	stringSetter(EnvDatabaseURL, &cfg.Database.URL)

	boolSetter(EnvDevAuth, &cfg.Auth.DevAuth)
	boolSetter(EnvCookieSecure, &cfg.Auth.Cookie.Secure)
	csvSetter(EnvPlatformAdminUserIDs, &cfg.Auth.PlatformAdminUserIDs)

	stringSetter(EnvMasterKey, &cfg.Secret.MasterKey)

	boolSetter(EnvFeishuMock, &cfg.Gateway.Feishu.Mock)
	stringSetter(EnvFeishuAppID, &cfg.Gateway.Feishu.AppID)
	stringSetter(EnvFeishuAppSecret, &cfg.Gateway.Feishu.AppSecret)
	stringSetter(EnvFeishuRedirectURI, &cfg.Gateway.Feishu.RedirectURI)
	stringSetter(EnvFeishuScope, &cfg.Gateway.Feishu.Scope)
	stringSetter(EnvFeishuAuthorizeBase, &cfg.Gateway.Feishu.AuthorizeBase)
	stringSetter(EnvFeishuAPIBase, &cfg.Gateway.Feishu.APIBase)
	stringSetter(EnvFeishuVerificationToken, &cfg.Gateway.Feishu.VerificationToken)
	stringSetter(EnvFeishuEncryptKey, &cfg.Gateway.Feishu.EncryptKey)
	stringSetter(EnvLoginRedirectURL, &cfg.Gateway.Feishu.LoginRedirectURL)

	stringSetter(EnvOpenCodeBin, &cfg.Model.OpenCodeBin)
	stringSetter(EnvOpenCodeRunner, &cfg.Sandbox.Runner)

	boolSetter(EnvAuditOTLPEnabled, &cfg.Audit.OTLP.Enabled)
	stringSetter(EnvAuditOTLPAddr, &cfg.Audit.OTLP.Addr)
	stringSetter(EnvAuditOTLPSigningKey, &cfg.Audit.OTLP.SigningKey)
	stringSetter(EnvAuditOTLPExternalEndpoint, &cfg.Audit.OTLP.ExternalEndpoint)
	stringSetter(EnvAuditOTLPFanoutEndpoint, &cfg.Audit.OTLP.FanoutEndpoint)

	stringSetter(EnvOSSRegion, &cfg.Storage.OSS.Region)
	stringSetter(EnvOSSEndpoint, &cfg.Storage.OSS.Endpoint)
	stringSetter(EnvOSSBucket, &cfg.Storage.OSS.Bucket)
	stringSetter(EnvOSSAccessKeyID, &cfg.Storage.OSS.AccessKeyID)
	stringSetter(EnvOSSAccessKeySecret, &cfg.Storage.OSS.AccessKeySecret)
	stringSetter(EnvOSSBaseURL, &cfg.Storage.OSS.BaseURL)
	stringSetter(EnvBlobBackend, &cfg.Storage.BlobBackend)
}

// resolveConfigPath enforces the absolute-or-~/ rule.
func resolveConfigPath(rawPath string) (string, error) {
	if strings.HasPrefix(rawPath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("user home dir: %w", err)
		}
		return filepath.Join(home, rawPath[2:]), nil
	}
	if !filepath.IsAbs(rawPath) {
		return "", errors.New("config path must be absolute or start with ~/")
	}
	return rawPath, nil
}

// defaultDataDir returns ~/.parsar, or empty on os.UserHomeDir
// failure so Validate() surfaces a clear "data_dir is required"
// error. Per AGENTS.md, runtime state MUST live under ~/.parsar/;
// falling back to a temp dir would silently violate that.
func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".parsar")
}

// parseBool accepts the same truthy strings as auth.parseDevAuth so
// behaviour is uniform.
func parseBool(v string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

// LoadFromOS wires os.Getenv + os.ReadFile.
func LoadFromOS() (LoadResult, error) {
	return Load(os.Getenv, os.ReadFile)
}
