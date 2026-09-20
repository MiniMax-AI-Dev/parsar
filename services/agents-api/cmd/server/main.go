// Package main runs the standalone Agents API service.
//
// @title Agents API
// @version 1
// @description Supported single-Agent execution resources from the pinned openai-python beta/agents contract. Bearer keys bind an execution principal to one project; optional OpenAI-Organization and OpenAI-Project headers must match that binding.
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

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
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
	credentialKey, err := credentialCipher()
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
	managed, closeManaged, err := managedRuntimes()
	if err != nil {
		return err
	}
	defer closeManaged()
	transientOptions, err := executionOptions()
	if err != nil {
		return err
	}
	executionStore := store.NewWithCredentialCipher(pool, credentialKey)
	if err := executionStore.EnsureProjectScopes(ready, auth.ProjectScopes()); err != nil {
		return err
	}
	var workerDone chan error
	var worker *execution.Worker
	options := []api.Option{api.WithSourceFiles(executionStore), api.WithSessionArtifacts(executionStore)}
	var daemonHandler http.Handler
	var registry *gateway.Registry
	var checkOwnership func(context.Context) error
	if wsURL := os.Getenv("AGENTS_API_DAEMON_WS_URL"); wsURL != "" {
		daemonHandler, registry, err = runtime.NewGateway(executionStore, wsURL)
		if err != nil {
			return err
		}
		defer runtime.CloseConnections(registry)
		// Constructors do not invoke ownership checks; publish only after setup.
		checkOwnership = func(ctx context.Context) error {
			if worker == nil {
				return errors.New("execution worker is not initialized")
			}
			return worker.CheckOwnership(ctx)
		}
	}
	executor, err := executorRegistry(executionStore, func() *execution.Worker { return worker }, checkOwnership)
	if err != nil {
		return err
	}
	if executor != nil {
		defer executor.Close()
		options = append(options, api.WithEnvironmentRemoteURL(executor.PublicURL()))
	}
	if registry != nil {
		dispatcher := &execution.Dispatcher{Store: executionStore, Registry: registry,
			EnvironmentConnection: environmentConnection(executor), ManagedRuntimes: managed, Options: transientOptions}
		if executor != nil {
			dispatcher.CloseEnvironmentConnections = executor.Close
		}
		worker, err = execution.StartWorker(ctx, dispatcher)
		if err != nil {
			return err
		}
		workerDone = make(chan error, 1)
		go func() { workerDone <- worker.Run(ctx) }()
		defer func() {
			stop()
			if workerDone != nil {
				<-workerDone
			}
		}()
		options = append(options, api.WithExecution(worker), api.WithEnvironmentDirectoryReader(worker), api.WithEnvironmentFileWriter(worker))
		if managed != nil && (managed.DefaultProvider != "" || len(managed.EngineProviders) > 0) {
			kinds := make([]string, 0, len(managed.EngineProviders))
			for kind := range managed.EngineProviders {
				kinds = append(kinds, kind)
			}
			options = append(options, api.WithHostedEnvironments(), api.WithHarnesses(kinds))
		}
	}
	handler, err := api.NewHandler(executionStore, auth, engine, options...)
	if err != nil {
		return err
	}
	if daemonHandler != nil || executor != nil {
		mux := http.NewServeMux()
		if daemonHandler != nil {
			mux.Handle("/api/v1/agent-daemon/", daemonHandler)
		}
		if executor != nil {
			mux.Handle("/cloud/environment/", executor.Handler())
		}
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
	case err := <-workerDone:
		workerDone = nil
		stop()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
