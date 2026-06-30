package cli

import (
	"strings"
	"testing"
)

func TestConfigValidateOK(t *testing.T) {
	cfg := Config{ServerURL: "https://api.example.com", RunnerToken: "tok"}
	if err := cfg.validate(); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
}

func TestConfigValidateMissingURL(t *testing.T) {
	cfg := Config{RunnerToken: "tok"}
	err := cfg.validate()
	if err == nil || !strings.Contains(err.Error(), "PARSAR_SERVER_URL") {
		t.Fatalf("expected PARSAR_SERVER_URL error, got %v", err)
	}
}

func TestConfigValidateMissingToken(t *testing.T) {
	cfg := Config{ServerURL: "https://api.example.com"}
	err := cfg.validate()
	if err == nil || !strings.Contains(err.Error(), "PARSAR_RUNNER_TOKEN") {
		t.Fatalf("expected PARSAR_RUNNER_TOKEN error, got %v", err)
	}
}

func TestConfigValidateBadScheme(t *testing.T) {
	cfg := Config{ServerURL: "ftp://api.example.com", RunnerToken: "tok"}
	err := cfg.validate()
	if err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("expected scheme error, got %v", err)
	}
}

func TestConfigValidateRelativeURL(t *testing.T) {
	cfg := Config{ServerURL: "/api", RunnerToken: "tok"}
	err := cfg.validate()
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected absolute-url error, got %v", err)
	}
}

func TestLoadConfigFromEnvHappyPath(t *testing.T) {
	t.Setenv("PARSAR_SERVER_URL", "https://api.example.com")
	t.Setenv("PARSAR_RUNNER_TOKEN", "tok")
	t.Setenv("PARSAR_RUNTIME_ID", "rt-1")
	t.Setenv("PARSAR_WORKSPACE_ID", "ws-1")
	t.Setenv("PARSAR_USER_ID", "user-1")
	t.Setenv("PARSAR_CONNECTOR", "claude")
	cfg, err := loadConfigFromEnv()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.RuntimeID != "rt-1" || cfg.WorkspaceID != "ws-1" || cfg.UserID != "user-1" || cfg.Connector != "claude" {
		t.Fatalf("informational fields not propagated: %+v", cfg)
	}
}

func TestLoadConfigFromEnvMissing(t *testing.T) {
	t.Setenv("PARSAR_SERVER_URL", "")
	t.Setenv("PARSAR_RUNNER_TOKEN", "")
	_, err := loadConfigFromEnv()
	if err == nil {
		t.Fatal("expected error when required vars missing")
	}
}
