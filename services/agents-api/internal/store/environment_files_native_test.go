package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestNativePublicEnvironmentFiles(t *testing.T) {
	binary, image := os.Getenv("PARSAR_CODEX_BINARY"), os.Getenv("PARSAR_PLACEMENT_EXECUTOR_IMAGE")
	keyFile, python := os.Getenv("PARSAR_PLACEMENT_MODEL_KEY_FILE"), os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if binary == "" || !strings.HasPrefix(image, "sha256:") || keyFile == "" || python == "" || os.Getenv("PARSAR_EXECUTOR_LAUNCHER") == "" {
		t.Skip("qualified Codex executor, directory artifacts, pinned SDK and real provider required")
	}
	version, err := exec.Command(binary, "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != "codex-cli 0.153.4" {
		t.Fatal("native Codex 0.153.4 required")
	}
	keyBytes, err := os.ReadFile(keyFile)
	if err != nil || strings.TrimSpace(string(keyBytes)) == "" {
		t.Fatal("real model credential unavailable")
	}
	key := strings.TrimSpace(string(keyBytes))
	t.Setenv("PARSAR_CODEX_BIN", binary)
	artifact := prepareWorkerDirectoryArtifact(t, binary)
	h, ctx, root := nativeDispatchHarnessWithTimeout(t, 10*time.Minute)
	second := &dispatchHarness{t: t, s: h.s, tenant: uuid.NewString(), credential: uuid.NewString(), registry: h.registry, url: h.url}
	second.device, err = h.s.CreateDevice(ctx, second.tenant, "second isolated executor", device.HashCredential(second.credential))
	if err != nil {
		t.Fatal(err)
	}
	secondRoot, err := os.MkdirTemp(os.Getenv("PARSAR_NATIVE_PROOF_DIR"), "execution-native-")
	if err != nil {
		t.Fatal(err)
	}
	startNativeDispatchDaemon(t, second, secondRoot, os.Getenv("PARSAR_NATIVE_DAEMON_BIN"))
	peers, roots := []*dispatchHarness{h, second}, []string{root, secondRoot}
	tokens := []string{uuid.NewString(), uuid.NewString()}
	secrets := []string{key, h.credential, second.credential, tokens[0], tokens[1]}
	var secretMu sync.Mutex
	var registry *codex.Registry
	h.d.Options = func(context.Context, store.Session) (map[string]any, error) {
		return map[string]any{"codex_provider": map[string]any{"name": "MiniMax validation", "base_url": "https://api.minimax.cn/v1", "bearer_token": key, "wire_api": "responses"}}, nil
	}
	h.d.EnvironmentConnection = func(owner context.Context, session store.Session, environment store.Environment) (execution.EnvironmentConnection, error) {
		token, release, err := registry.IssueHarnessCredential(owner, session.TenantID, environment.ID)
		secretMu.Lock()
		secrets = append(secrets, token)
		secretMu.Unlock()
		return execution.EnvironmentConnection{URL: registry.PublicURL(), Token: token, Release: release}, err
	}
	h.d.CloseEnvironmentConnections = func() {
		if registry != nil {
			registry.Close()
		}
	}
	worker, err := execution.StartWorker(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	var started sync.Once
	start := func() { started.Do(func() { go func() { done <- worker.Run(workerCtx) }() }) }
	server := httptest.NewUnstartedServer(nil)
	defer func() {
		cancel()
		start()
		select {
		case err := <-done:
			if err != nil && err != context.Canceled {
				t.Error("worker stopped unexpectedly", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("worker retained Files acceptance ownership")
		}
		if registry != nil {
			registry.Close()
			if registry.LifecycleError() != nil {
				t.Error("registry lifecycle failed")
			}
		}
		server.Close()
	}()
	registry, err = codex.New(codex.Config{Store: h.s, CheckOwnership: worker.CheckOwnership, ReplaceConnection: worker.ReplaceEnvironmentConnection, ObserveConnection: worker.ObserveEnvironmentConnection, PublicURL: "http://" + server.Listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]api.APIKey, 0, len(peers))
	for index, peer := range peers {
		keys = append(keys, api.APIKey{OrganizationID: "test-org", ProjectID: peer.tenant, SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: device.HashCredential(tokens[index]), TenantID: peer.tenant})
	}
	auth, err := api.NewAuthenticator(keys)
	if err != nil {
		t.Fatal(err)
	}
	public, err := api.NewHandler(h.s, auth, "codex", api.WithExecution(worker), api.WithEnvironmentDirectoryReader(worker), api.WithEnvironmentRemoteURL(registry.PublicURL()))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/cloud/environment/", registry.Handler())
	mux.Handle("/", public)
	server.Config.Handler = mux
	server.Start()
	start()
	tenants := make([]map[string]string, 0, len(peers))
	for index, peer := range peers {
		workspace := "/parsar-public-files-" + uuid.NewString()
		peer.session, err = createPublicFilesSession(ctx, peer, workspace)
		if err != nil {
			t.Fatal(err)
		}
		environment, err := h.s.GetSessionEnvironment(ctx, peer.tenant, peer.session.ID)
		if err != nil {
			t.Fatal(err)
		}
		credential, err := h.s.IssueExecutorCredential(ctx, store.FixtureExecutorPrincipal(t, h.s, peer.tenant), uuid.NewString(), "")
		if err != nil {
			t.Fatal(err)
		}
		secretMu.Lock()
		secrets = append(secrets, credential.Token)
		secretMu.Unlock()
		local := prepareDaemonRemoteWorkspace(t, roots[index], "FILES_"+uuid.NewString())
		artifact.container = startDaemonRemoteExecutor(t, ctx, roots[index], local, workspace, binary, image, registry.PublicURL(), environment.ID, credential)
		artifact.installDirectoryHelper(t, ctx)
		awaitEnvironmentConnectionState(t, ctx, h.s, peer.tenant, environment.ID, "connected")
		tokenFile := filepath.Join(roots[index], "public-token")
		if err := os.WriteFile(tokenFile, []byte(tokens[index]), 0600); err != nil {
			t.Fatal(err)
		}
		tenants = append(tenants, map[string]string{"session_id": peer.session.ID, "token_file": tokenFile})
	}
	settings, err := json.Marshal(map[string]any{"engine": "codex", "base": server.URL, "tenants": tenants})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, python, "../../tests/official_environment_files_native.py")
	command.Stdin = bytes.NewReader(settings)
	output, err := command.CombinedOutput()
	secretMu.Lock()
	for _, secret := range secrets {
		if secret != "" && bytes.Contains(output, []byte(secret)) {
			t.Error("credential appeared in public Files evidence")
			output = bytes.ReplaceAll(output, []byte(secret), []byte("[REDACTED]"))
		}
	}
	secretMu.Unlock()
	if writeErr := os.WriteFile(filepath.Join(root, "public-files-evidence.json"), output, 0600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if err != nil || !json.Valid(output) {
		t.Fatal("real public Files SDK/raw verification failed; inspect private evidence", root)
	}
	for _, home := range roots {
		remaining, err := filepath.Glob(filepath.Join(home, "parsar-daemon", "workspace-read", "read-*"))
		if err != nil || len(remaining) != 0 {
			t.Fatal("public Files returned before temporary read cleanup")
		}
	}
	t.Log("real two-tenant Files.list evidence", root)
}

func createPublicFilesSession(ctx context.Context, h *dispatchHarness, workspace string) (store.Session, error) {
	instructions := "Use the native shell for requested file operations. Do not delegate."
	agent := v1.Agent{ID: "agent_" + uuid.NewString(), Model: "MiniMax-M3", Instructions: &instructions,
		MultiAgent: v1.MultiAgentConfig{Enabled: false}, Reasoning: v1.Reasoning{}, ServiceTier: "auto",
		Text: v1.TextConfig{Format: v1.TextFormat{Type: "text"}, Verbosity: "medium"}, Tools: []json.RawMessage{}}
	configuration, err := json.Marshal(map[string]any{"agent": agent, "environment": map[string]any{"type": "self_hosted", "workspace_directory": workspace, "capability_directories": []string{}}})
	if err != nil {
		return store.Session{}, err
	}
	return h.s.CreateSession(ctx, h.tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: configuration})
}
