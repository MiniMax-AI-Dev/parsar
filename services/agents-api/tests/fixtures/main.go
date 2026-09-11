// Command fixtures seeds internal Turns for official-client recovery tests.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fixture struct {
	Tenant  string   `json:"tenant"`
	Session string   `json:"session"`
	Turns   []string `json:"turns"`
}

func main() {
	if err := seed(); err != nil {
		os.Stderr.WriteString("Turn fixture setup failed.\n")
		os.Exit(1)
	}
}

func seed() error {
	path := os.Getenv("AGENTS_API_TURN_FIXTURE")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var f fixture
	if err = json.Unmarshal(raw, &f); err != nil {
		return err
	}
	cfg, err := pgxpool.ParseConfig(os.Getenv("PARSAR_AGENTS_API_TEST_DATABASE_URL"))
	if err != nil {
		return err
	}
	if !strings.HasPrefix(cfg.ConnConfig.Database, "parsar_agents_api_") || !strings.HasSuffix(cfg.ConnConfig.Database, "_tests") {
		return errors.New("dedicated test database required")
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	s := store.New(pool)
	for _, status := range []string{store.TurnCompleted, store.TurnFailed, store.TurnCancelled, store.TurnInProgress} {
		receipt, err := s.SubmitMessage(ctx, f.Tenant, f.Session, uuid.NewString(), json.RawMessage(`{"text":"recovery fixture"}`))
		if err != nil {
			return err
		}
		if _, err = s.TransitionTurn(ctx, f.Tenant, f.Session, receipt.TurnID, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress}); err != nil {
			return err
		}
		if status != store.TurnInProgress {
			outcome := json.RawMessage(`{"error":"SECRET engine log","done":{"metadata":{"agent_session_id":"PRIVATE"}}}`)
			if _, err = s.TransitionTurn(ctx, f.Tenant, f.Session, receipt.TurnID, store.TurnTransition{ExpectedStatus: store.TurnInProgress, Status: status, Outcome: outcome}); err != nil {
				return err
			}
		}
		f.Turns = append(f.Turns, receipt.TurnID)
	}
	raw, err = json.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0600)
}
