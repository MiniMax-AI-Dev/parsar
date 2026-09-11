package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/migrations"
)

func main() {
	if err := run(); err != nil {
		log.Bg().Error("agents-api migration failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := os.Getenv("AGENTS_API_DATABASE_URL")
	if databaseURL == "" {
		return errors.New("AGENTS_API_DATABASE_URL must point to a dedicated execution database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return migrations.Apply(ctx, databaseURL)
}
