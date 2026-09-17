package store_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestNativeExecutorDirectoryHelper(t *testing.T) {
	probe, helper, proof := os.Getenv("PARSAR_DIRECTORY_PROBE"), os.Getenv("PARSAR_DIRECTORY_HELPER"), os.Getenv("PARSAR_EXECUTOR_PROOF_DIR")
	if probe == "" || helper == "" || proof == "" {
		t.Skip("private directory qualification binaries required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp(proof, "native-directory-")
	if err != nil {
		t.Fatal(err)
	}
	local, workspace := filepath.Join(root, "workspace"), "/scoped-directory-"+uuid.NewString()
	for _, name := range []string{"workspace/sub", "workspace/empty", "executor/codex", "harness"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(local, "retained.txt"), []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(local, "a")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5000; i++ {
		if err := os.WriteFile(filepath.Join(local, "sub", strconv.Itoa(i)), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s, _ := store.NewTestStore(t)
	lease, err := s.AcquireExecutionLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close(context.Background())
	tenant := uuid.NewString()
	principal := store.FixtureExecutorPrincipal(t, s, tenant)
	configuration, _ := json.Marshal(map[string]any{"environment": map[string]any{"type": "self_hosted", "workspace_directory": workspace, "capability_directories": []string{}}})
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "directory-primitive", Configuration: configuration})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(ctx, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := s.IssueExecutorCredential(ctx, principal, environment.ID, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	registry, err := codex.New(codex.Config{Store: s, CheckOwnership: lease.Ping, ReplaceConnection: lease.Store().ReplaceEnvironmentConnection, ObserveConnection: lease.Store().ObserveEnvironmentConnection, PublicURL: "http://" + server.Listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = registry.Handler()
	server.Start()
	defer func() { registry.Close(); server.Close() }()
	container := startDaemonRemoteExecutor(t, ctx, root, local, workspace, os.Getenv("PARSAR_CODEX_BINARY"), os.Getenv("PARSAR_PLACEMENT_EXECUTOR_IMAGE"), server.URL, environment.ID, credential)
	t.Cleanup(func() { _ = os.Remove(filepath.Join(root, "executor", "credential.json")) })
	if err := exec.CommandContext(ctx, "docker", "cp", helper, container+":/usr/local/bin/scoped-directory").Run(); err != nil {
		t.Fatal("install qualification helper", err)
	}
	awaitDaemonRemoteCondition(t, ctx, 30*time.Second, "executor ready", func() bool {
		connected, e := registry.Connected(ctx, tenant, environment.ID)
		return e == nil && connected
	})
	token, release, err := registry.IssueHarnessCredential(ctx, tenant, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	command := exec.CommandContext(ctx, probe)
	command.Dir = filepath.Join(root, "harness")
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(root, "harness"), "PARSAR_NATIVE_ENV_PROOF=" + root, "PARSAR_DIRECTORY_WORKSPACE=" + workspace, "CODEX_EXEC_SERVER_NOISE_REGISTRY_URL=" + server.URL, "CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID=" + environment.ID, "CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN=" + token, "NO_PROXY=127.0.0.1,localhost", "RUST_LOG=off"}
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.ReplaceAll(string(output), token, "[redacted]")
		message = strings.ReplaceAll(message, credential.Token, "[redacted]")
		t.Fatalf("native directory probe: %v: %s", err, message)
	}
	data, err := os.ReadFile(filepath.Join(root, "directory-native.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatal("invalid evidence")
	}
	t.Log("native directory evidence", root)
}
