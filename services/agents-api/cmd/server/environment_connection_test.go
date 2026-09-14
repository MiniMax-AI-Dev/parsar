package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type connectionStore struct{ tenant, environment string }

func (s connectionStore) GetEnvironment(ctx context.Context, tenant, environment string) (store.Environment, error) {
	if err := ctx.Err(); err != nil {
		return store.Environment{}, err
	}
	if tenant != s.tenant || environment != s.environment {
		return store.Environment{}, store.ErrNotFound
	}
	return store.Environment{ID: environment}, nil
}

func (connectionStore) AuthenticateEnvironmentExecutor(context.Context, string, string) (string, error) {
	return "", store.ErrNotFound
}

func TestEnvironmentConnectionUsesScopedOwnerCredentials(t *testing.T) {
	if environmentConnection(nil) != nil {
		t.Fatal("disabled registry enabled pending-input scheduling")
	}
	source := connectionStore{tenant: uuid.NewString(), environment: uuid.NewString()}
	owned := true
	registry, err := codex.New(codex.Config{Store: source, PublicURL: "https://executor.example/",
		ReplaceConnection: func(context.Context, string, string, string) error {
			t.Fatal("credential-only fixture replaced a connection")
			return nil
		},
		ObserveConnection: func(context.Context, string, string, string, int64, bool) error {
			t.Fatal("credential-only fixture observed a connection")
			return nil
		},
		CheckOwnership: func(context.Context) error {
			if !owned {
				return errors.New("execution ownership lost")
			}
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	resolve := environmentConnection(registry)
	session := store.Session{TenantID: source.tenant, Engine: "codex"}
	environment := store.Environment{ID: source.environment}
	owner, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, err := resolve(owner, session, environment)
	if err != nil || first.URL != "https://executor.example" || first.Token == "" || first.Release == nil {
		t.Fatal("connection did not carry the configured origin and owner credential", err)
	}
	defer first.Release()
	second, err := resolve(owner, session, environment)
	if err != nil || second.Token == first.Token {
		t.Fatal("independent execution owners reused a credential", err)
	}
	defer second.Release()
	status := func(token string) int {
		req := httptest.NewRequest(http.MethodPost, "/cloud/environment/"+source.environment+"/connect", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		registry.Handler().ServeHTTP(response, req)
		return response.Code
	}
	// Authenticated requests reach body validation; there is no executor fixture.
	if status(first.Token) != http.StatusBadRequest || status(second.Token) != http.StatusBadRequest {
		t.Fatal("issued credentials did not reach their native registry")
	}
	first.Release()
	if status(first.Token) != http.StatusUnauthorized || status(second.Token) != http.StatusBadRequest {
		t.Fatal("release did not retain the separate execution owner")
	}
	foreign := session
	foreign.TenantID = uuid.NewString()
	if _, err := resolve(owner, foreign, environment); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign tenant obtained Environment authority", err)
	}
	foreign = session
	foreign.Engine = "claude_sdk"
	if _, err := resolve(owner, foreign, environment); !errors.Is(err, store.ErrInvalidInput) {
		t.Fatal("Codex transport accepted another engine", err)
	}
	cancel()
	if status(second.Token) != http.StatusUnauthorized {
		t.Fatal("owner cancellation retained its credential")
	}
	owned = false
	failed, err := resolve(context.Background(), session, environment)
	if err == nil || failed.Token != "" || failed.Release != nil {
		t.Fatal("lost ownership issued a usable connection")
	}
}
