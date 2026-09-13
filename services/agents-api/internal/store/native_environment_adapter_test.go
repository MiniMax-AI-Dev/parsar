package store_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestNativeDaemonRemoteEnvironment(t *testing.T) {
	binary, image := os.Getenv("PARSAR_CODEX_BINARY"), os.Getenv("PARSAR_PLACEMENT_EXECUTOR_IMAGE")
	keyFile := os.Getenv("PARSAR_PLACEMENT_MODEL_KEY_FILE")
	if binary == "" || !strings.HasPrefix(image, "sha256:") || keyFile == "" {
		t.Skip("pinned native binary, local executor image and real provider key file required")
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
	h, ctx, root := nativeDispatchHarnessWithTimeout(t, 8*time.Minute)
	peer, err := h.registry.LookupDevice(h.device.ID)
	if err != nil {
		t.Fatal(err)
	}
	info, found, known := peer.AgentKindStatus("codex")
	if !known || !found || !info.Available || !info.Capabilities.RemoteEnvironment {
		t.Fatal("real heartbeat omitted remote capability")
	}
	// These credentials do not exist when the authenticated daemon starts.
	executorToken, harnessToken := uuid.NewString(), uuid.NewString()
	workspace := "/parsar-daemon-remote-" + uuid.NewString()
	configuration, err := json.Marshal(map[string]any{"environment": map[string]any{"type": "self_hosted", "workspace_directory": workspace, "capability_directories": []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := h.s.CreateSession(ctx, h.tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "remote-adapter", Configuration: configuration})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := h.s.GetSessionEnvironment(ctx, h.tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := h.s.AcquireExecutionLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close(context.Background())
	scope := func(token string) codex.ScopedKey {
		return codex.ScopedKey{TokenSHA256: device.HashCredential(token), TenantID: h.tenant, EnvironmentID: environment.ID}
	}
	executorToken, err = h.s.IssueEnvironmentExecutorCredential(t.Context(), h.tenant, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	registry, err := codex.New(codex.Config{Store: h.s, CheckOwnership: lease.Ping, PublicURL: "http://" + server.Listener.Addr().String(), HarnessKeys: []codex.ScopedKey{scope(harnessToken)}})
	if err != nil {
		t.Fatal(err)
	}
	observation := &relayObservation{}
	handler := registry.Handler()
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(&relayResponse{ResponseWriter: w, observation: observation, path: r.URL.Path}, r)
	})
	server.Start()
	defer func() { registry.Close(); server.Close() }()
	memory, instruction := uuid.NewString(), "REMOTE_"+uuid.NewString()
	local := prepareDaemonRemoteWorkspace(t, root, instruction)
	container := startDaemonRemoteExecutor(t, ctx, root, local, workspace, binary, image, server.URL, environment.ID, executorToken)
	awaitDaemonRemoteCondition(t, ctx, 30*time.Second, "executor registration", func() bool {
		connected, e := registry.Connected(ctx, h.tenant, environment.ID)
		return e == nil && connected
	})
	req := proto.PromptRequestPayload{AgentKind: "codex", AgentStateKey: "remote-" + session.ID, WorkDir: filepath.Join(root, "harness"), ReleaseOnCompletion: true, StrictResume: true, DisableSubagents: true, ObserveMessages: true, ObserveToolObservations: true,
		AgentOptions:      map[string]any{"model": "MiniMax-M3", "web_search": "disabled", "system_prompt": "Follow the user instructions and use the native shell for requested commands.", "codex_provider": map[string]any{"name": "MiniMax validation", "base_url": "https://api.minimax.cn/v1", "bearer_token": key, "wire_api": "responses"}},
		RemoteEnvironment: &proto.RemoteEnvironment{ID: environment.ID, WorkspaceDirectory: workspace, ConnectionURL: server.URL, ConnectionToken: harnessToken}}
	proof := map[string]any{"scope": "authenticated daemon adapter; public Environment admission and dispatcher remain pending", "native_version": string(version), "environment_id": environment.ID, "remote_workspace": workspace, "events": []proto.Envelope{}}
	defer persistDaemonRemoteProof(t, root, proof, []string{key, executorToken, harnessToken, h.credential})
	bad := req
	bad.AgentStateKey += "-rejected"
	badBinding := *req.RemoteEnvironment
	badBinding.ConnectionToken = "invalid-test-token"
	bad.RemoteEnvironment = &badBinding
	bad.Prompt = "This must fail before a model Turn."
	_, rejected, _ := daemonRemotePrompt(t, ctx, peer, bad, nil)
	if !daemonRemoteHasError(rejected) {
		t.Fatal("invalid transient authorization accepted")
	}
	proof["invalid_authorization_rejected"] = true
	for index, phase := range []string{"first", "resumed"} {
		observation.mu.Lock()
		before := observation.executors
		observation.mu.Unlock()
		req.Prompt = "Run the exact command `./placement.sh " + phase + "` once with the native shell. The tool command argument must be exactly the text inside the backticks: no wrapper, no appended echo, no separators, no error recovery. Exit 7 is intentional; preserve that native exit status and do not retry. Report stdout, stderr and the verification memory briefly."
		if index == 0 {
			req.Prompt += " Remember this memory value: " + memory + "."
		} else {
			req.Prompt += " Recall the memory value from the first Turn and read retained.txt."
		}
		done, events, _ := daemonRemotePrompt(t, ctx, peer, req, nil)
		proof[phase] = map[string]any{"done": done, "events": events}
		if daemonRemoteHasError(events) || !strings.Contains(done.Content, memory) || !strings.Contains(done.Content, instruction) || strings.Contains(done.Content, "WRONG_LOCAL_INSTRUCTIONS") {
			t.Fatal("remote instructions or cold native memory not observed; inspect private proof")
		}
		native, ok := done.Metadata[proto.DoneMetaAgentSessionID].(string)
		if !ok || native == "" || (index == 1 && native != req.AgentSessionID) {
			t.Fatal("native continuation identity changed")
		}
		req.AgentSessionID = native
		assertDaemonRemoteCommand(t, events, phase, workspace)
		if data, e := os.ReadFile(filepath.Join(local, phase+".cwd")); e != nil || strings.TrimSpace(string(data)) != workspace {
			t.Fatal("command used a different workspace")
		}
		awaitDaemonRemoteCondition(t, ctx, 30*time.Second, "executor reconnect after harness release", func() bool {
			observation.mu.Lock()
			reconnected := observation.executors > before
			observation.mu.Unlock()
			connected, e := registry.Connected(ctx, h.tenant, environment.ID)
			return reconnected && e == nil && connected
		})
	}
	count, err := os.ReadFile(filepath.Join(local, "execution-count"))
	if err != nil || string(count) != "first\nresumed\n" {
		t.Fatal("remote command was omitted or repeated")
	}
	if data, e := os.ReadFile(filepath.Join(local, "retained.txt")); e != nil || string(data) != "remote-file-content\n" {
		t.Fatal("remote file did not persist")
	}
	req.Prompt = "Run the exact command `./long.sh cancel` using the native shell. It deliberately runs until cancelled. Keep waiting or polling; do not finish this Turn or produce a final answer while it is running."
	cancelAt := time.Time{}
	_, events, ack := daemonRemotePrompt(t, ctx, peer, req, func() bool {
		_, e := os.Stat(filepath.Join(local, "cancel.heartbeat"))
		if e == nil && cancelAt.IsZero() {
			cancelAt = time.Now()
			return true
		}
		return false
	})
	proof["cancel_events"] = events
	proof["cancel_receipt"] = ack
	if cancelAt.IsZero() || ack == nil || !ack.Applied || ack.ErrorCode != "" {
		t.Fatal("native command was not cancelled through the daemon")
	}
	awaitDaemonRemoteExit(t, ctx, container, local)
	proof["cancel_to_observed_exit_seconds"] = time.Since(cancelAt).Seconds()
	if peer.IsClosed() {
		t.Fatal("daemon disconnected during cancellation acceptance")
	}
	connected, err := registry.Connected(ctx, h.tenant, environment.ID)
	if err != nil || !connected {
		t.Fatal("registry/executor stopped during cancellation acceptance")
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatal("remote path was created on the harness host")
	}
	if _, err := os.Stat(filepath.Join(local, "credential-failure")); !os.IsNotExist(err) {
		t.Fatal("command inherited credential variables")
	}
	assertDaemonRemoteSecrets(t, root, req.AgentStateKey, key, executorToken, harnessToken, h.credential)
	if strings.Contains(string(session.Configuration), harnessToken) {
		t.Fatal("connection credential reached stored configuration")
	}
	proof["status"] = "daemon_adapter_verified_public_integration_pending"
	proof["native_thread_id"] = req.AgentSessionID
	proof["harness_token_only_from_typed_prompt"] = true
	t.Log("real-provider daemon remote execution evidence", root)
}
