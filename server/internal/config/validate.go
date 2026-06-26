package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Validate runs sanity checks against the merged Config. Hard
// checks always fire; profile checks fire only under ProfileProd
// so the local loop does not need a master key. All errors wrap
// ErrInvalidConfig.
func (c Config) Validate() error {
	var problems []string

	// DATABASE_URL required in prod; in dev empty is the documented
	// "health endpoint only" degraded path.
	if strings.TrimSpace(c.Database.URL) == "" {
		if c.Profile() == ProfileProd {
			problems = append(problems, fmt.Sprintf("database.url is required in production (env %s)", EnvDatabaseURL))
		}
	} else if _, err := url.Parse(c.Database.URL); err != nil {
		problems = append(problems, fmt.Sprintf("database.url is not a valid URL: %v", err))
	}

	if strings.TrimSpace(c.Server.Addr) == "" {
		problems = append(problems, fmt.Sprintf("server.addr is required (env %s)", EnvAddr))
	}

	if dd := strings.TrimSpace(c.Server.DataDir); dd == "" {
		problems = append(problems, fmt.Sprintf("server.data_dir is required (env %s)", EnvDataDir))
	} else if !strings.HasPrefix(dd, "/") && !strings.HasPrefix(dd, "~/") {
		problems = append(problems, fmt.Sprintf("server.data_dir must be absolute or start with ~/ (got %q)", dd))
	}

	if publicURL := strings.TrimSpace(c.Server.PublicURL); publicURL != "" {
		parsed, err := url.Parse(publicURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			problems = append(problems, fmt.Sprintf("server.public_url must be an absolute URL (env %s)", EnvPublicURL))
		}
	}

	if c.Profile() == ProfileProd {
		if strings.TrimSpace(c.Server.PublicURL) == "" {
			problems = append(problems, fmt.Sprintf("server.public_url is required in production (env %s)", EnvPublicURL))
		}
		if strings.TrimSpace(c.Secret.MasterKey) == "" {
			problems = append(problems, fmt.Sprintf("secret.master_key is required in production (env %s); generate with `openssl rand -hex 32`", EnvMasterKey))
		}
		if c.Auth.DevAuth {
			problems = append(problems, fmt.Sprintf("auth.dev_auth must NOT be true in production (env %s)", EnvDevAuth))
		}
		if !c.Auth.Cookie.Secure {
			problems = append(problems, fmt.Sprintf("auth.cookie.secure should be true in production (env %s) — HTTP cookies leak", EnvCookieSecure))
		}
	}

	// Sandbox.Runner: empty falls back to "local"; reject anything
	// else so a typo does not silently downgrade.
	if v := strings.TrimSpace(c.Sandbox.Runner); v != "" && v != "local" && v != "sandbox" {
		problems = append(problems, fmt.Sprintf("sandbox.runner must be \"local\" or \"sandbox\" (env %s, got %q)", EnvOpenCodeRunner, v))
	}

	// Audit OTLP: enabled requires non-empty addr. We don't parse
	// host:port here — net.Listen gives a clearer diagnostic.
	if c.Audit.OTLP.Enabled && strings.TrimSpace(c.Audit.OTLP.Addr) == "" {
		problems = append(problems, fmt.Sprintf("audit.otlp.addr is required when audit.otlp.enabled is true (env %s)", EnvAuditOTLPAddr))
	}

	// Audit OTLP: production must provide a signing key explicitly;
	// dev relies on cmd/server's dev-constant fallback (mirrors
	// the master-key path).
	if c.Audit.OTLP.Enabled && c.Profile() == ProfileProd && strings.TrimSpace(c.Audit.OTLP.SigningKey) == "" {
		problems = append(problems, fmt.Sprintf("audit.otlp.signing_key is required in production when audit.otlp.enabled is true (env %s); generate with `openssl rand -hex 32`", EnvAuditOTLPSigningKey))
	}

	// Storage.OSS: either all required fields are set or none are.
	// Half-configured OSS is a frequent operator mistake (AK rotated
	// without secret); catching it here saves a confusing 5xx at
	// first upload.
	if err := validatePartialOSS(c.Storage.OSS); err != nil {
		problems = append(problems, err.Error())
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w:\n  - %s", ErrInvalidConfig, strings.Join(problems, "\n  - "))
}

// Redacted returns a copy of Config with secret-bearing fields
// replaced by "<redacted len=N>" so it is safe to log.
func (c Config) Redacted() Config {
	out := c
	out.Database.URL = redactConnString(c.Database.URL)
	out.Secret.MasterKey = redact(c.Secret.MasterKey)
	out.Auth.Bootstrap.Token = redact(c.Auth.Bootstrap.Token)
	out.Gateway.Feishu.AppSecret = redact(c.Gateway.Feishu.AppSecret)
	out.Gateway.Feishu.VerificationToken = redact(c.Gateway.Feishu.VerificationToken)
	out.Gateway.Feishu.EncryptKey = redact(c.Gateway.Feishu.EncryptKey)
	out.Audit.OTLP.SigningKey = redact(c.Audit.OTLP.SigningKey)
	return out
}

// validatePartialOSS catches the half-configured OSS case: either
// ALL required fields are set, or NONE. "Required" = the four
// fields oss.Config.Enabled() checks; endpoint + base_url are
// independent.
func validatePartialOSS(o OSSConfig) error {
	set := map[string]bool{
		"region":            strings.TrimSpace(o.Region) != "",
		"bucket":            strings.TrimSpace(o.Bucket) != "",
		"access_key_id":     strings.TrimSpace(o.AccessKeyID) != "",
		"access_key_secret": strings.TrimSpace(o.AccessKeySecret) != "",
	}

	anySet := false
	allSet := true
	for _, v := range set {
		if v {
			anySet = true
		} else {
			allSet = false
		}
	}
	if !anySet {
		return nil
	}
	if !allSet {
		var missing []string
		for k, v := range set {
			if !v {
				missing = append(missing, "storage.oss."+k)
			}
		}
		return fmt.Errorf("storage.oss: partially configured — missing %s (set all four or leave them all empty)", strings.Join(missing, ", "))
	}

	if base := strings.TrimSpace(o.BaseURL); base != "" {
		if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
			return fmt.Errorf("storage.oss.base_url must start with http:// or https:// (env %s; got %q)", EnvOSSBaseURL, base)
		}
	}
	return nil
}

func redact(v string) string {
	if v == "" {
		return ""
	}
	return fmt.Sprintf("<redacted len=%d>", len(v))
}

// redactConnString strips userinfo (password) from a Postgres URL
// while keeping host / db / params visible.
func redactConnString(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return redact(raw)
	}
	if u.User != nil {
		u.User = url.UserPassword("<redacted>", "<redacted>")
	}
	return u.String()
}

var _ = errors.New
