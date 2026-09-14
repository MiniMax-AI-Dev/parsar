package main

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func executorRegistry(s *store.Store, worker func() *execution.Worker, checkOwnership func(context.Context) error) (*codex.Registry, error) {
	file, url := os.Getenv("AGENTS_API_EXECUTOR_KEYS_FILE"), os.Getenv("AGENTS_API_EXECUTOR_URL")
	harnessFile := os.Getenv("AGENTS_API_HARNESS_KEYS_FILE")
	if file == "" && url == "" && harnessFile == "" {
		return nil, nil
	}
	if file != "" {
		return nil, errors.New("AGENTS_API_EXECUTOR_KEYS_FILE is retired; issue durable credentials with agents-api-environment-key and remove the old setting")
	}
	if harnessFile != "" {
		return nil, errors.New("AGENTS_API_HARNESS_KEYS_FILE is retired; harness credentials belong to internal execution ownership; remove the old setting")
	}
	if url == "" || checkOwnership == nil || worker == nil {
		return nil, errors.New("executor registry requires URL and the daemon execution worker")
	}
	return codex.New(codex.Config{Store: s, CheckOwnership: checkOwnership, PublicURL: url,
		ReplaceConnection: func(ctx context.Context, tenant, environment, generation string) error {
			if worker() == nil {
				return errors.New("execution worker is not initialized")
			}
			return worker().ReplaceEnvironmentConnection(ctx, tenant, environment, generation)
		},
		ObserveConnection: func(ctx context.Context, tenant, environment, generation string, revision int64, connected bool) error {
			if worker() == nil {
				return errors.New("execution worker is not initialized")
			}
			return worker().ObserveEnvironmentConnection(ctx, tenant, environment, generation, revision, connected)
		},
	})
}

func environmentConnection(registry *codex.Registry, origin string) func(context.Context, store.Session, store.Environment) (execution.EnvironmentConnection, error) {
	if registry == nil {
		return nil
	}
	return func(ctx context.Context, session store.Session, environment store.Environment) (execution.EnvironmentConnection, error) {
		if session.Engine != "codex" {
			return execution.EnvironmentConnection{}, store.ErrInvalidInput
		}
		token, release, err := registry.IssueHarnessCredential(ctx, session.TenantID, environment.ID)
		if err != nil {
			return execution.EnvironmentConnection{}, err
		}
		return execution.EnvironmentConnection{URL: strings.TrimRight(origin, "/"), Token: token, Release: release}, nil
	}
}
