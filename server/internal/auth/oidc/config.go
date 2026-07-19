package oidc

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const (
	EnvProviders = "PARSAR_AUTH_OIDC_PROVIDERS"

	envPrefix = "PARSAR_AUTH_OIDC_"
)

var providerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

type EnvFunc func(string) string

type ProviderConfig struct {
	ID                   string
	Label                string
	IssuerURL            string
	ClientID             string
	ClientSecret         string
	RedirectURI          string
	Scopes               []string
	AllowedDomains       []string
	TokenAuthMethod      string
	RequireVerifiedEmail bool
}

type ProviderEnvStatus struct {
	Config      ProviderConfig
	RequiredEnv []string
	MissingEnv  []string
}

func LoadProviderStatuses(env EnvFunc, publicURL string) ([]ProviderEnvStatus, error) {
	if env == nil {
		return nil, errors.New("oidc: env func is nil")
	}
	ids := splitCSV(env(EnvProviders))
	out := make([]ProviderEnvStatus, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" {
			continue
		}
		if !providerIDPattern.MatchString(id) {
			return nil, fmt.Errorf("oidc: invalid provider id %q", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("oidc: duplicate provider id %q", id)
		}
		seen[id] = true

		key := envKey(id)
		required := []string{
			envPrefix + key + "_ISSUER_URL",
			envPrefix + key + "_CLIENT_ID",
			envPrefix + key + "_CLIENT_SECRET",
		}
		missing := missingEnv(env, required)
		redirectEnv := envPrefix + key + "_REDIRECT_URI"
		redirectURI := strings.TrimSpace(env(redirectEnv))
		if redirectURI == "" {
			redirectURI = defaultRedirectURI(publicURL, id)
		}
		if redirectURI == "" {
			missing = append(missing, redirectEnv)
		}

		cfg := ProviderConfig{
			ID:                   id,
			Label:                coalesce(strings.TrimSpace(env(envPrefix+key+"_LABEL")), id),
			IssuerURL:            strings.TrimRight(strings.TrimSpace(env(envPrefix+key+"_ISSUER_URL")), "/"),
			ClientID:             strings.TrimSpace(env(envPrefix + key + "_CLIENT_ID")),
			ClientSecret:         strings.TrimSpace(env(envPrefix + key + "_CLIENT_SECRET")),
			RedirectURI:          redirectURI,
			Scopes:               splitSpaceList(coalesce(strings.TrimSpace(env(envPrefix+key+"_SCOPES")), "openid email profile")),
			AllowedDomains:       lowerList(splitCSV(env(envPrefix + key + "_ALLOWED_DOMAINS"))),
			TokenAuthMethod:      strings.TrimSpace(env(envPrefix + key + "_TOKEN_AUTH_METHOD")),
			RequireVerifiedEmail: !isExplicitFalse(env(envPrefix + key + "_REQUIRE_VERIFIED_EMAIL")),
		}
		out = append(out, ProviderEnvStatus{
			Config:      cfg,
			RequiredEnv: append(required, redirectEnv),
			MissingEnv:  missing,
		})
	}
	return out, nil
}

func NewClientsFromEnv(env EnvFunc, publicURL string) (map[string]Client, []ProviderEnvStatus, error) {
	statuses, err := LoadProviderStatuses(env, publicURL)
	if err != nil {
		return nil, nil, err
	}
	clients := make(map[string]Client, len(statuses))
	for _, status := range statuses {
		if len(status.MissingEnv) > 0 {
			continue
		}
		client, err := NewClient(status.Config)
		if err != nil {
			return nil, statuses, err
		}
		clients[status.Config.ID] = client
	}
	return clients, statuses, nil
}

func defaultRedirectURI(publicURL, id string) string {
	base := strings.TrimRight(strings.TrimSpace(publicURL), "/")
	if base == "" {
		return ""
	}
	return base + "/api/v1/auth/oidc/" + id + "/callback"
}

func envKey(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

func missingEnv(env EnvFunc, names []string) []string {
	missing := make([]string, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(env(name)) == "" {
			missing = append(missing, name)
		}
	}
	return missing
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitSpaceList(v string) []string {
	fields := strings.Fields(v)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if strings.TrimSpace(f) != "" {
			out = append(out, f)
		}
	}
	return out
}

func lowerList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func isExplicitFalse(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return true
	default:
		return false
	}
}

func validateConfig(cfg ProviderConfig) error {
	if !providerIDPattern.MatchString(cfg.ID) {
		return fmt.Errorf("oidc: invalid provider id %q", cfg.ID)
	}
	for name, raw := range map[string]string{
		"issuer_url":    cfg.IssuerURL,
		"client_id":     cfg.ClientID,
		"client_secret": cfg.ClientSecret,
		"redirect_uri":  cfg.RedirectURI,
	} {
		if strings.TrimSpace(raw) == "" {
			return fmt.Errorf("oidc: %s is required for provider %q", name, cfg.ID)
		}
	}
	if _, err := url.ParseRequestURI(cfg.RedirectURI); err != nil {
		return fmt.Errorf("oidc: invalid redirect_uri for provider %q: %w", cfg.ID, err)
	}
	if u, err := url.Parse(cfg.IssuerURL); err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("oidc: invalid issuer_url for provider %q", cfg.ID)
	}
	if !contains(cfg.Scopes, "openid") {
		return fmt.Errorf("oidc: scopes for provider %q must include openid", cfg.ID)
	}
	if cfg.TokenAuthMethod != "" &&
		cfg.TokenAuthMethod != "client_secret_post" &&
		cfg.TokenAuthMethod != "client_secret_basic" {
		return fmt.Errorf("oidc: unsupported token auth method %q for provider %q", cfg.TokenAuthMethod, cfg.ID)
	}
	return nil
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

func coalesce(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
