package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/google/uuid"
)

func preparedPublicHandler(t *testing.T, h *dispatchHarness, worker *execution.Worker, registry *codex.Registry) (http.Handler, string, string) {
	t.Helper()
	token, foreign, foreignTenant := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: "test-org", ProjectID: h.tenant, SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: device.HashCredential(token), TenantID: h.tenant},
		{OrganizationID: "test-org", ProjectID: foreignTenant, SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: device.HashCredential(foreign), TenantID: foreignTenant},
	})
	if err != nil {
		t.Fatal(err)
	}
	public, err := api.NewHandler(h.s, auth, "codex", api.WithExecution(worker), api.WithEnvironmentRemoteURL(registry.PublicURL()))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/cloud/environment/", registry.Handler())
	mux.Handle("/", public)
	return mux, token, foreign
}

type preparedPublicObserver struct {
	directory string
	command   *exec.Cmd
	cancel    context.CancelFunc
	done      chan struct{}
	err       error
}

type preparedPublicConnection struct {
	RemoteURL     string `json:"remote_url"`
	EnvironmentID string `json:"environment_id"`
}

func startPreparedPublicObserver(t *testing.T, ctx context.Context, root string, settings map[string]string) *preparedPublicObserver {
	t.Helper()
	directory := filepath.Join(root, "public-environment")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	log, err := os.OpenFile(filepath.Join(directory, "observer.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	settings["evidence"] = directory
	input, err := json.Marshal(settings)
	if err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(ctx, os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON"), "../../tests/official_environment_activity.py")
	command.Stdin = bytes.NewReader(input)
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		cancel()
		_ = log.Close()
		t.Fatal("public Environment observer failed to start", err)
	}
	observer := &preparedPublicObserver{directory: directory, command: command, cancel: cancel, done: make(chan struct{})}
	go func() {
		observer.err = command.Wait()
		_ = log.Close()
		close(observer.done)
	}()
	return observer
}

func (o *preparedPublicObserver) await(t *testing.T, ctx context.Context, name string) preparedPublicConnection {
	t.Helper()
	var value preparedPublicConnection
	awaitDaemonRemoteCondition(t, ctx, 30*time.Second, "public Environment "+name, func() bool {
		select {
		case <-o.done:
			t.Fatal("public Environment observer ended before signal; inspect private log", o.directory, o.err)
		default:
		}
		raw, err := os.ReadFile(filepath.Join(o.directory, name+".json"))
		if os.IsNotExist(err) {
			return false
		}
		if err != nil || json.Unmarshal(raw, &value) != nil {
			t.Fatal("invalid public Environment observer signal")
		}
		return true
	})
	return value
}

func (o *preparedPublicObserver) finish(t *testing.T) {
	t.Helper()
	select {
	case <-o.done:
		if o.err != nil {
			t.Fatal("public Environment SDK/raw/SSE verification failed; inspect private log", o.directory, o.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("public Environment observer did not finish", o.directory)
	}
}

func (o *preparedPublicObserver) close() {
	o.cancel()
	select {
	case <-o.done:
	case <-time.After(5 * time.Second):
		_ = o.command.Process.Kill()
		<-o.done
	}
}
