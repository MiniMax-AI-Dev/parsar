package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestNativePublicSelfHostedStandalone(t *testing.T) {
	runNativePublicSelfHosted(t, "empty_later")
}

func TestNativePublicSelfHostedOrdinaryInitialStandalone(t *testing.T) {
	runNativePublicSelfHosted(t, "ordinary_initial")
}

func TestNativePublicSelfHostedStreamedInitialStandalone(t *testing.T) {
	runNativePublicSelfHosted(t, "streamed_initial")
}

func runNativePublicSelfHosted(t *testing.T, mode string) {
	t.Helper()
	serverBinary, nativeBinary := os.Getenv("PARSAR_AGENTS_API_SERVER_BIN"), os.Getenv("PARSAR_CODEX_BINARY")
	image, keyFile := os.Getenv("PARSAR_PLACEMENT_EXECUTOR_IMAGE"), os.Getenv("PARSAR_PLACEMENT_MODEL_KEY_FILE")
	proofRoot, daemonBinary := os.Getenv("PARSAR_NATIVE_PROOF_DIR"), os.Getenv("PARSAR_NATIVE_DAEMON_BIN")
	if serverBinary == "" || nativeBinary == "" || keyFile == "" || proofRoot == "" || daemonBinary == "" ||
		!strings.HasPrefix(image, "sha256:") || os.Getenv("PARSAR_EXECUTOR_LAUNCHER") == "" || os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON") == "" {
		t.Skip("built standalone service, daemon, launcher, pinned native/SDK, local image and real provider credential required")
	}
	version, err := exec.Command(nativeBinary, "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != "codex-cli 0.153.4" {
		t.Fatal("native Codex 0.153.4 required")
	}
	keyBytes, err := os.ReadFile(keyFile)
	if err != nil || strings.TrimSpace(string(keyBytes)) == "" {
		t.Fatal("real provider credential unavailable")
	}
	key := strings.TrimSpace(string(keyBytes))
	s, pool := store.NewTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	t.Cleanup(cancel)
	root, err := os.MkdirTemp(proofRoot, "public-self-hosted-"+mode+"-")
	if err != nil {
		t.Fatal(err)
	}
	tenant, foreignTenant := uuid.NewString(), uuid.NewString()
	principal := store.FixtureExecutorPrincipal(t, s, tenant)
	executor, err := s.IssueExecutorCredential(ctx, principal, uuid.NewString(), "")
	if err != nil {
		t.Fatal(err)
	}
	caller, foreign, deviceToken := uuid.NewString(), uuid.NewString(), uuid.NewString()
	daemonDevice, err := s.CreateDevice(ctx, tenant, "Public self-hosted acceptance", device.HashCredential(deviceToken))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, release := context.WithTimeout(context.Background(), 10*time.Second)
		defer release()
		if err := s.RevokeDevice(cleanup, tenant, daemonDevice.ID); err != nil {
			t.Error("owned device credential cleanup failed", err)
		}
		if err := s.RevokeExecutorCredential(cleanup, principal, executor.KeyID); err != nil {
			t.Error("owned executor credential cleanup failed", err)
		}
	})
	keys := filepath.Join(root, "api-keys.json")
	writePublicNativeJSON(t, keys, []api.APIKey{
		{TenantID: tenant, OrganizationID: principal.OrganizationID, ProjectID: principal.ProjectID, SubjectKind: principal.SubjectKind, SubjectID: principal.SubjectID, TokenSHA256: device.HashCredential(caller)},
		{TenantID: foreignTenant, OrganizationID: principal.OrganizationID, ProjectID: foreignTenant, SubjectKind: principal.SubjectKind, SubjectID: principal.SubjectID, TokenSHA256: device.HashCredential(foreign)},
	})
	address := publicNativeAddress(t)
	base := "http://" + address
	serverEnvironment := publicNativeEnvironment(map[string]string{
		"AGENTS_API_DATABASE_URL": os.Getenv("PARSAR_AGENTS_API_TEST_DATABASE_URL"),
		"AGENTS_API_KEYS_FILE":    keys, "AGENTS_API_ADDR": address, "AGENTS_API_ENGINE": "codex",
		"AGENTS_API_DAEMON_WS_URL": "ws://" + address + "/api/v1/agent-daemon/ws",
		"AGENTS_API_EXECUTOR_URL":  base, "AGENTS_API_EXECUTOR_KEYS_FILE": "", "AGENTS_API_HARNESS_KEYS_FILE": "",
		"PARSAR_HOME": root,
	})
	server := startPublicNativeProcess(t, ctx, root, "server", serverEnvironment, serverBinary)
	awaitPublicNativeServer(t, ctx, server, base)
	instruction := "REMOTE_" + uuid.NewString()
	local := prepareDaemonRemoteWorkspace(t, root, instruction)
	workspace := "/parsar-public-self-hosted-" + uuid.NewString()
	const memory = "walnut heron violet cedar cobalt willow moss iris"
	observer := startPublicNativeClient(t, ctx, root, map[string]string{
		"base": base, "token": caller, "foreign_token": foreign, "workspace_directory": workspace,
		"remote_url": base, "memory": memory, "instruction": instruction, "creation_mode": mode,
	})
	waiting := awaitPublicNativeSignal(t, ctx, observer, "waiting", 70*time.Second)
	sessionID, environmentID := waiting["session_id"], waiting["environment_id"]
	environment, err := s.GetSessionEnvironment(ctx, tenant, sessionID)
	if err != nil || environment.ID != environmentID || waiting["remote_url"] != base || environment.Status != "pending" || waiting["creation_mode"] != mode {
		t.Fatal("public Environment differs from owned offline target", err)
	}
	var reservationID string
	if err := pool.QueryRow(ctx, "SELECT id FROM environment_input_reservations WHERE session_id=$1", sessionID).Scan(&reservationID); err != nil {
		t.Fatal("public first input did not retain its reservation", err)
	}
	reservation, err := s.GetEnvironmentInputReservation(ctx, tenant, sessionID, reservationID)
	if err != nil || reservation.State != store.EnvironmentInputPending || reservation.IsInitial != (mode != "empty_later") {
		t.Fatal("public first input has incorrect reservation origin/state", err)
	}
	startDaemonRemoteExecutor(t, ctx, root, local, workspace, nativeBinary, image, waiting["remote_url"], environmentID, executor)
	awaitEnvironmentConnectionState(t, ctx, s, tenant, environmentID, "connected")
	writePublicNativeJSON(t, filepath.Join(observer.directory, "initial-connection-ready.json"), map[string]string{"environment_id": environmentID})
	connectedRead := awaitPublicNativeSignal(t, ctx, observer, "initial-connection-read", 20*time.Second)
	if connectedRead["environment_id"] != environmentID {
		t.Fatal("public connected retrieval observed the wrong Environment")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/agents/environments/"+environmentID, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+executor.Token)
	request.Header.Set("OpenAI-Beta", "agents=v1")
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("executor-only credential retrieval request failed", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("executor-only credential authorized a public Environment read")
	}
	profile := filepath.Join(root, "parsar-daemon", "execution")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	writePublicNativeJSON(t, filepath.Join(profile, "auth.json"), map[string]string{
		"server_url": base + "/api/v1", "runtime_id": daemonDevice.ID, "runner_credential": deviceToken, "device_name": "Public self-hosted acceptance",
	})
	wrapper := filepath.Join(root, "codex-minimax")
	wrapperText := "#!/bin/sh\nfor argument in \"$@\"; do\n  if [ \"$argument\" = app-server ]; then printf '%s\\n' \"$$\" >> \"$PARSAR_PUBLIC_NATIVE_LAUNCH_LOG\"; fi\ndone\nexec \"$PARSAR_PUBLIC_NATIVE_CODEX\" -c 'model_provider=\"minimax_validation\"' -c 'model_providers.minimax_validation.name=\"MiniMax validation\"' -c 'model_providers.minimax_validation.base_url=\"https://api.minimax.cn/v1\"' -c 'model_providers.minimax_validation.env_key=\"MINIMAX_VALIDATION_KEY\"' -c 'model_providers.minimax_validation.wire_api=\"responses\"' \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(wrapperText), 0700); err != nil {
		t.Fatal(err)
	}
	daemonEnvironment := publicNativeEnvironment(map[string]string{
		"PARSAR_HOME": root, "PARSAR_CODEX_BIN": wrapper, "PARSAR_PUBLIC_NATIVE_CODEX": nativeBinary, "MINIMAX_VALIDATION_KEY": key,
		"PARSAR_PUBLIC_NATIVE_LAUNCH_LOG": filepath.Join(root, "native-starts"),
	})
	startPublicNativeProcess(t, ctx, root, "daemon", daemonEnvironment, daemonBinary, "connect", "--profile", "execution")
	first := awaitPublicNativeSignal(t, ctx, observer, "first-completed", 180*time.Second)
	binding, err := s.GetSessionDevice(ctx, tenant, sessionID)
	if err != nil || binding.ID != daemonDevice.ID || binding.NativeSessionID == "" {
		t.Fatal("first public Turn lacks native device/history binding", err)
	}
	firstStarts, err := os.ReadFile(filepath.Join(root, "native-starts"))
	if err != nil || len(strings.Fields(string(firstStarts))) != 1 {
		t.Fatal("first input retry started another native harness")
	}
	awaitEnvironmentConnectionState(t, ctx, s, tenant, environmentID, "connected")
	writePublicNativeJSON(t, filepath.Join(observer.directory, "resume-ready.json"), map[string]string{"session_id": sessionID})
	select {
	case <-observer.done:
		observer.finish(t)
	case <-ctx.Done():
		t.Fatal("public second Turn timed out; inspect private proof")
	}
	finalBinding, err := s.GetSessionDevice(ctx, tenant, sessionID)
	if err != nil || finalBinding != binding {
		t.Fatal("public continuation changed native history/device binding", err)
	}
	data, err := os.ReadFile(filepath.Join(observer.directory, "public-environment-proof.json"))
	if err != nil {
		t.Fatal(err)
	}
	var publicProof struct {
		CreationMode string `json:"creation_mode"`
		Turns        []struct {
			ID string `json:"id"`
		} `json:"turns"`
	}
	if json.Unmarshal(data, &publicProof) != nil || publicProof.CreationMode != mode || len(publicProof.Turns) != 2 || publicProof.Turns[0].ID != first["turn_id"] {
		t.Fatal("public proof does not contain the two accepted Turns")
	}
	settled, err := s.GetEnvironmentInputReservation(ctx, tenant, sessionID, reservationID)
	if err != nil || settled.State != store.EnvironmentInputAdmitted || settled.IsInitial != reservation.IsInitial || !settled.Deadline.Equal(reservation.Deadline) {
		t.Fatal("public retries changed original reservation origin/deadline", err)
	}
	for index, phase := range []string{"first", "resumed"} {
		var observed []proto.Envelope
		var after int32
		for {
			events, err := s.ListTurnEvents(ctx, tenant, sessionID, publicProof.Turns[index].ID, after, 100)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) == 0 {
				break
			}
			for _, event := range events {
				observed = append(observed, proto.Envelope{Type: event.Kind, Payload: event.Payload})
				after = event.Ordinal
			}
		}
		assertDaemonRemoteCommand(t, observed, phase, workspace)
		cwd, err := os.ReadFile(filepath.Join(local, phase+".cwd"))
		if err != nil || strings.TrimSpace(string(cwd)) != workspace {
			t.Fatal("actual command did not run in the executor-only workspace")
		}
	}
	count, err := os.ReadFile(filepath.Join(local, "execution-count"))
	if err != nil || string(count) != "first\nresumed\n" {
		t.Fatal("public retry repeated or omitted real command execution")
	}
	retained, err := os.ReadFile(filepath.Join(local, "retained.txt"))
	if err != nil || string(retained) != "remote-file-content\n" {
		t.Fatal("remote file did not persist between public Turns")
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatal("remote workspace appeared on the harness host")
	}
	if _, err := os.Stat(filepath.Join(local, "credential-failure")); !os.IsNotExist(err) {
		t.Fatal("native command inherited transport/provider credentials")
	}
	starts, err := os.ReadFile(filepath.Join(root, "native-starts"))
	processes := strings.Fields(string(starts))
	if err != nil || len(processes) != 2 || processes[0] == processes[1] {
		t.Fatal("continuation did not cold-start exactly one fresh harness per Turn")
	}
	artifact, err := os.ReadFile(serverBinary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(artifact)
	proof := map[string]any{
		"status": "built_service_public_self_hosted_real_execution_verified", "session_id": sessionID, "environment_id": environmentID,
		"creation_mode": mode, "reservation_id": reservationID, "reservation_initial": reservation.IsInitial, "reservation_deadline": reservation.Deadline,
		"server_binary": serverBinary, "server_sha256": hex.EncodeToString(digest[:]), "native_thread_id": binding.NativeSessionID,
		"native_version": strings.TrimSpace(string(version)), "completed_turns": 2, "launcher_remote_url": waiting["remote_url"],
		"launcher_environment_id": environmentID, "executor_key_id": executor.KeyID, "executor_key_issued_before_session": true,
		"already_connected_second_input": true, "command_execution_count": string(count), "public_evidence": filepath.Join(observer.directory, "public-environment-proof.json"),
		"native_harness_processes": processes,
	}
	persistDaemonRemoteProof(t, root, proof, []string{key, caller, foreign, deviceToken, executor.Token})
	t.Log("built standalone public self-hosted real-provider evidence", root)
}
