package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pressly/goose/v3"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// envOpencodeRunner reads the bootstrap-default runtime mode operators set
// at server startup. Only "sandbox" is honored; everything else collapses
// to "local" so migration backfill stays deterministic regardless of env
// typos.
//
// Why Go-side (not Postgres GUC): set_config(..., false) relies on
// connection-pool LIFO reuse — true for current pgx stdlib but NOT
// API-guaranteed. Reading env in Go and passing as a query parameter
// removes the pool dependency entirely.
func envOpencodeRunner() string {
	if strings.TrimSpace(os.Getenv("PARSAR_OPENCODE_RUNNER")) == "sandbox" {
		return "sandbox"
	}
	return "local"
}

func main() {
	if err := run(context.Background()); err != nil {
		log.Bg().Error("migration failed", "error", err)
		os.Exit(1)
	}
}

// resolveMigrationsDir picks the migrations directory in this priority:
//
//  1. PARSAR_MIGRATIONS_DIR (operator override; absolute path expected
//     in container builds — production image exports /app/migrations).
//  2. ./server/migrations relative to CWD (repo-root invocation).
//
// Returns a clear error when none resolve so an operator running from
// the wrong CWD sees the problem instead of a goose stack later. No
// CWD-relative `./migrations` fallback — it caused a real bug where a
// top-level migration was silently skipped.
func resolveMigrationsDir() (string, error) {
	if explicit := os.Getenv("PARSAR_MIGRATIONS_DIR"); explicit != "" {
		info, err := os.Stat(explicit)
		if err != nil {
			return "", fmt.Errorf("PARSAR_MIGRATIONS_DIR=%q: %w", explicit, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("PARSAR_MIGRATIONS_DIR=%q is not a directory", explicit)
		}
		return explicit, nil
	}

	candidate := filepath.Join("server", "migrations")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate, nil
	}

	cwd, _ := os.Getwd()
	return "", fmt.Errorf("no migrations directory found; tried PARSAR_MIGRATIONS_DIR and server/migrations (CWD=%s). "+
		"Set PARSAR_MIGRATIONS_DIR to an absolute path, or run the binary from the repo root", cwd)
}

func run(ctx context.Context) error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = db.DefaultDatabaseURL
	}

	migrationsDir, err := resolveMigrationsDir()
	if err != nil {
		return err
	}

	sqlDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	if err := sqlDB.PingContext(ctx); err != nil {
		return err
	}

	goose.SetBaseFS(nil)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	if err := goose.UpContext(ctx, sqlDB, migrationsDir); err != nil {
		return err
	}
	return nil
}
