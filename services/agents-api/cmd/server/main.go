// Package main runs the standalone Agents API service.
//
// @title Agents API
// @version 1
// @description Supported single-Agent execution resources from the pinned openai-python beta/agents contract.
// @license.name Apache 2.0
// @license.url https://www.apache.org/licenses/LICENSE-2.0.html
// @BasePath /v1
// @schemes http https
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtime"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		log.Bg().Error("agents-api startup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL, keysFile := os.Getenv("AGENTS_API_DATABASE_URL"), os.Getenv("AGENTS_API_KEYS_FILE")
	if databaseURL == "" || keysFile == "" {
		return errors.New("AGENTS_API_DATABASE_URL and AGENTS_API_KEYS_FILE are required")
	}
	content, err := os.ReadFile(keysFile)
	if err != nil {
		return errors.New("cannot read AGENTS_API_KEYS_FILE")
	}
	var keys []api.APIKey
	if err := json.Unmarshal(content, &keys); err != nil {
		return errors.New("AGENTS_API_KEYS_FILE must contain an array of API key bindings")
	}
	auth, err := api.NewAuthenticator(keys)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return errors.New("invalid Agents API database configuration")
	}
	defer pool.Close()
	ready, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(ready); err != nil {
		return errors.New("Agents API database connection failed")
	}
	engine := os.Getenv("AGENTS_API_ENGINE")
	if engine == "" {
		engine = "codex"
	}
	executionStore := store.New(pool)
	handler, err := api.NewHandler(executionStore, auth, engine)
	if err != nil {
		return err
	}
	if wsURL := os.Getenv("AGENTS_API_DAEMON_WS_URL"); wsURL != "" {
		daemonHandler, registry, err := runtime.NewGateway(executionStore, wsURL)
		if err != nil {
			return err
		}
		defer runtime.CloseConnections(registry)
		mux := http.NewServeMux()
		mux.Handle("/api/v1/agent-daemon/", daemonHandler)
		mux.Handle("/", handler)
		handler = mux
	}
	addr := os.Getenv("AGENTS_API_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8091"
	}
	server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
