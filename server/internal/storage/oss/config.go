package oss

import (
	"errors"
	"fmt"
	"strings"
)

// Config is the typed OSS configuration. Fields map 1:1 to env vars
// in server/internal/config/load.go.
//
// Empty values are valid only when no OSS-dependent feature is
// enabled. The server boots fine without OSS; callers observe
// `client == nil` and surface a clean "OSS not configured" error.
type Config struct {
	Region          string `yaml:"region"`
	Endpoint        string `yaml:"endpoint"`
	Bucket          string `yaml:"bucket"`
	AccessKeyID     string `yaml:"access_key_id"`
	AccessKeySecret string `yaml:"access_key_secret"`
	BaseURL         string `yaml:"base_url"`
}

// Enabled reports whether the minimum set of fields needed to
// construct a working client is present.
func (c Config) Enabled() bool {
	return strings.TrimSpace(c.Region) != "" &&
		strings.TrimSpace(c.Bucket) != "" &&
		strings.TrimSpace(c.AccessKeyID) != "" &&
		strings.TrimSpace(c.AccessKeySecret) != ""
}

// Validate enforces structural sanity. Called by the Client
// constructor; returns ErrInvalidConfig on any malformed field.
func (c Config) Validate() error {
	var problems []string

	if strings.TrimSpace(c.Region) == "" {
		problems = append(problems, "region is required (env OSS_REGION)")
	}
	if strings.TrimSpace(c.Bucket) == "" {
		problems = append(problems, "bucket is required (env OSS_BUCKET)")
	}
	if strings.TrimSpace(c.AccessKeyID) == "" {
		problems = append(problems, "access_key_id is required (env OSS_ACCESS_KEY_ID)")
	}
	if strings.TrimSpace(c.AccessKeySecret) == "" {
		problems = append(problems, "access_key_secret is required (env OSS_ACCESS_KEY_SECRET)")
	}
	if base := strings.TrimSpace(c.BaseURL); base != "" {
		if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
			problems = append(problems, fmt.Sprintf("base_url must start with http:// or https:// (got %q)", base))
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%w:\n  - %s", ErrInvalidConfig, strings.Join(problems, "\n  - "))
}

var ErrInvalidConfig = errors.New("oss: invalid config")
