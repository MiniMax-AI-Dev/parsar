// Command environment-key issues, rotates or revokes exact-Environment executor access using operator DB authority.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		log.Bg().Error("environment executor credential operation failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	tenant := flag.String("tenant", "", "execution tenant UUID")
	environment := flag.String("environment", "", "existing Environment UUID")
	rotate := flag.Bool("rotate", false, "explicitly replace the existing credential, including a revoked credential")
	revoke := flag.Bool("revoke", false, "revoke the existing credential without issuing a secret")
	flag.Parse()
	dsn := os.Getenv("AGENTS_API_DATABASE_URL")
	if dsn == "" || *tenant == "" || *environment == "" || flag.NArg() != 0 || (*rotate && *revoke) {
		return errors.New("AGENTS_API_DATABASE_URL, --tenant and --environment are required; --rotate and --revoke are mutually exclusive")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return errors.New("invalid execution database configuration")
	}
	defer pool.Close()
	s := store.New(pool)
	if *revoke {
		return s.RevokeEnvironmentExecutorCredential(ctx, *tenant, *environment)
	}
	issue := s.IssueEnvironmentExecutorCredential
	if *rotate {
		issue = s.RotateEnvironmentExecutorCredential
	}
	token, err := issue(ctx, *tenant, *environment)
	if err != nil {
		return err
	}
	// Operators redirect stdout to a mode-0600 file under ~/.parsar; no read-back operation exists.
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"environment_id": *environment, "executor_token": token})
}
