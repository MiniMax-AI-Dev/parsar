package store_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestNativeAppServerRemoteModelPlacement(t *testing.T) {
	binary, proof := os.Getenv("PARSAR_CODEX_BINARY"), os.Getenv("PARSAR_EXECUTOR_PROOF_DIR")
	image, key := os.Getenv("PARSAR_PLACEMENT_EXECUTOR_IMAGE"), os.Getenv("PARSAR_PLACEMENT_MODEL_KEY_FILE")
	if binary == "" || proof == "" || image == "" || key == "" {
		t.Skip("pinned native Codex executable, private evidence directory, executor image and real model key file required")
	}
	s, _ := store.NewTestStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	lease, err := s.AcquireExecutionLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close(context.Background())
	tenant := uuid.NewString()
	principal := store.FixtureExecutorPrincipal(t, s, tenant)
	workspace := "/parsar-remote-" + uuid.NewString()
	configuration, err := json.Marshal(map[string]any{"environment": map[string]any{"type": "self_hosted", "workspace_directory": workspace, "capability_directories": []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "native-placement", Configuration: configuration})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(ctx, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := s.IssueExecutorCredential(t.Context(), principal, environment.ID, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	executorToken := credential.Token
	server := httptest.NewUnstartedServer(nil)
	registry, err := codex.New(codex.Config{Store: s, CheckOwnership: lease.Ping, PublicURL: "http://" + server.Listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	harnessToken, releaseHarness, err := registry.IssueHarnessCredential(ctx, tenant, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseHarness()

	server.Config.Handler = registry.Handler()
	server.Start()
	defer func() { registry.Close(); server.Close() }()
	runtime, err := os.MkdirTemp(proof, "native-placement-")
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../tests/native/environment_model_probe.py")
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "PARSAR_CODEX_BINARY=" + binary, "PARSAR_PLACEMENT_ROOT=" + runtime,
		"PARSAR_PLACEMENT_EXECUTOR_IMAGE=" + image, "PARSAR_PLACEMENT_MODEL_KEY_FILE=" + key,
		"PARSAR_PLACEMENT_EXECUTOR_TOKEN=" + executorToken, "CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN=" + harnessToken,
		"CODEX_EXEC_SERVER_NOISE_REGISTRY_URL=" + server.URL, "CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID=" + environment.ID,
		"NO_PROXY=127.0.0.1,localhost"}
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY"} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	container := "parsar-placement-" + uuid.NewString()
	env = append(env, "PARSAR_PLACEMENT_WORKSPACE="+workspace, "PARSAR_PLACEMENT_CONTAINER="+container)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanup, "docker", "rm", "-f", container).Run()
	})
	process := startRelayProcess(t, ctx, runtime, env, "python3", script)
	select {
	case <-process.done:
		if process.err != nil {
			t.Fatal("real-model native placement failed; inspect private evidence", runtime, process.err)
		}
	case <-ctx.Done():
		t.Fatal("real-model native placement timed out", runtime)
	}
	data, err := os.ReadFile(filepath.Join(runtime, "proof.json"))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Report struct {
			Status string `json:"status"`
		} `json:"report"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.Report.Status != "characterized_with_blockers" {
		t.Fatal("native placement was not characterized", runtime)
	}
	t.Log("real-provider native app-server remote placement evidence", runtime)
}
