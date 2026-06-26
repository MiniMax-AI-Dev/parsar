package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/config"
)

// TestExampleYAMLsLoadCleanly asserts the operator-facing example
// YAMLs parse + survive the unknown-key check, catching drift
// between Config and the example files. Driven through the dev
// profile so the placeholder master_key / DATABASE_URL are accepted.
func TestExampleYAMLsLoadCleanly(t *testing.T) {
	docs := filepath.Join("..", "..", "..", "docs", "deploy")
	for _, name := range []string{"config.example.yaml"} {
		path := filepath.Join(docs, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s missing: %v", path, err)
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			t.Fatalf("abs path: %v", err)
		}
		env := func(k string) string {
			switch k {
			case config.EnvConfigFile:
				return absPath
			case config.EnvDevAuth:
				return "true"
			default:
				return ""
			}
		}
		if _, err := config.Load(env, os.ReadFile); err != nil {
			t.Fatalf("%s failed to load: %v", name, err)
		}
	}
}
