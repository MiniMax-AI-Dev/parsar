package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveMigrationsDirExplicitEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PARSAR_MIGRATIONS_DIR", dir)

	got, err := resolveMigrationsDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != dir {
		t.Fatalf("expected %q, got %q", dir, got)
	}
}

func TestResolveMigrationsDirExplicitEnvMissing(t *testing.T) {
	t.Setenv("PARSAR_MIGRATIONS_DIR", "/definitely/not/a/real/dir/parsar-migrate-test")

	_, err := resolveMigrationsDir()
	if err == nil {
		t.Fatal("expected error when explicit env points to missing dir")
	}
	if !strings.Contains(err.Error(), "PARSAR_MIGRATIONS_DIR") {
		t.Fatalf("error should mention the env var, got %q", err.Error())
	}
}

func TestResolveMigrationsDirExplicitEnvNotADirectory(t *testing.T) {
	f, err := os.CreateTemp("", "migrate-not-a-dir-*")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(f.Name())
	f.Close()
	t.Setenv("PARSAR_MIGRATIONS_DIR", f.Name())

	_, err = resolveMigrationsDir()
	if err == nil {
		t.Fatal("expected error when explicit env points at a regular file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error should explain the file-vs-dir mismatch, got %q", err.Error())
	}
}

func TestResolveMigrationsDirFallsBackToRepoLayout(t *testing.T) {
	t.Setenv("PARSAR_MIGRATIONS_DIR", "")
	dir := t.TempDir()
	migrations := filepath.Join(dir, "server", "migrations")
	if err := os.MkdirAll(migrations, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	oldCWD, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	got, err := resolveMigrationsDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != filepath.Join("server", "migrations") {
		t.Fatalf("expected server/migrations, got %q", got)
	}
}

func TestResolveMigrationsDirErrorsWhenNothingFound(t *testing.T) {
	t.Setenv("PARSAR_MIGRATIONS_DIR", "")
	dir := t.TempDir()
	oldCWD, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	_, err := resolveMigrationsDir()
	if err == nil {
		t.Fatal("expected error when no migrations directory exists anywhere")
	}
	if !strings.Contains(err.Error(), "no migrations directory found") {
		t.Fatalf("expected explanatory error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "CWD=") {
		t.Fatalf("expected CWD hint in error, got %q", err.Error())
	}
}
