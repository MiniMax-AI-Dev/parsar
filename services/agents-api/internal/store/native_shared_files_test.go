package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type sharedFilesProbeProof struct {
	Status                string `json:"status"`
	Transport             string `json:"transport"`
	Phase                 string `json:"phase"`
	NativeThreadID        string `json:"native_thread_id"`
	NativeTurnID          string `json:"native_turn_id"`
	Marker                string `json:"marker"`
	HistoryValue          string `json:"history_value"`
	Answer                string `json:"answer"`
	TurnStartedCount      int    `json:"turn_started_count"`
	TurnCompletedCount    int    `json:"turn_completed_count"`
	CommandStartedCount   int    `json:"command_started_count"`
	CommandCompletedCount int    `json:"command_completed_count"`
	ShutdownCompleted     bool   `json:"shutdown_completed"`
	Command               struct {
		ID               string `json:"id"`
		Command          string `json:"command"`
		Cwd              string `json:"cwd"`
		AggregatedOutput string `json:"aggregated_output"`
		ExitCode         int    `json:"exit_code"`
		Started          bool   `json:"started"`
		Completed        bool   `json:"completed"`
	} `json:"command"`
	Files struct {
		BinaryBytes          int      `json:"binary_bytes"`
		BinarySHA256         string   `json:"binary_sha256"`
		MetadataSize         int      `json:"metadata_size"`
		DirectoryNames       []string `json:"directory_names"`
		ActiveHeartbeat      string   `json:"active_heartbeat"`
		ActiveBinaryVerified bool     `json:"active_binary_verified"`
		ActiveDirectoryNames []string `json:"active_directory_names"`
		Artifact             string   `json:"artifact"`
	} `json:"files"`
}

type sharedFilesProfile struct {
	probeVariable string
	status        string
	transport     string
	limitations   string
}

func TestNativeSharedEnvironmentFiles(t *testing.T) {
	testNativeSharedEnvironmentFiles(t, sharedFilesProfile{
		probeVariable: "PARSAR_SHARED_FILES_PROBE", status: "shared_native_files_characterized_with_blockers",
		transport:   "in_process",
		limitations: "The upstream event queue may drop nonrequired events without Lagged. Principal counters/answers/effects characterize this bounded workload, not a lossless production transport, public protocol compatibility, workspace confinement or OS quiescence.",
	})
}

func TestNativeRawEnvironmentFiles(t *testing.T) {
	testNativeSharedEnvironmentFiles(t, sharedFilesProfile{
		probeVariable: "PARSAR_RAW_FILES_PROBE", status: "raw_native_files_characterized",
		transport:   "raw_unix_socket",
		limitations: "The native remote client has an internal unbounded event queue. This finite workflow does not qualify production backpressure, complete output, public Files, idle ownership, authorization/fencing or cancellation.",
	})
}

func testNativeSharedEnvironmentFiles(t *testing.T, profile sharedFilesProfile) {
	probe, binary := os.Getenv(profile.probeVariable), os.Getenv("PARSAR_CODEX_BINARY")
	proofDirectory, image := os.Getenv("PARSAR_EXECUTOR_PROOF_DIR"), os.Getenv("PARSAR_PLACEMENT_EXECUTOR_IMAGE")
	keyFile, launcher := os.Getenv("PARSAR_PLACEMENT_MODEL_KEY_FILE"), os.Getenv("PARSAR_EXECUTOR_LAUNCHER")
	if probe == "" || binary == "" || proofDirectory == "" || image == "" || keyFile == "" || launcher == "" {
		t.Skip("built shared-files probe, pinned native installation/launcher/image, private proof and real provider key required")
	}
	if !strings.HasPrefix(image, "sha256:") {
		t.Fatal("pin the preloaded executor image")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	t.Cleanup(cancel)
	version, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != "codex-cli 0.153.4" {
		t.Fatal("native Codex 0.153.4 required")
	}
	keyBytes, err := os.ReadFile(keyFile)
	if err != nil || strings.TrimSpace(string(keyBytes)) == "" {
		t.Fatal("real provider credential unavailable")
	}
	key := strings.TrimSpace(string(keyBytes))
	callerHome, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(callerHome) || !filepath.IsAbs(proofDirectory) {
		t.Fatal("absolute caller HOME and proof directory required")
	}
	stateRoot, err := filepath.EvalSymlinks(filepath.Join(callerHome, ".parsar"))
	if err != nil {
		t.Fatal("resolve caller state directory", err)
	}
	proofDirectory, err = filepath.EvalSymlinks(proofDirectory)
	if err != nil {
		t.Fatal("resolve proof directory", err)
	}
	relative, err := filepath.Rel(stateRoot, proofDirectory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatal("proof directory must resolve under caller ~/.parsar")
	}
	root, err := os.MkdirTemp(proofDirectory, "shared-files-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("shared native filesystem evidence", root)
	instruction := "REMOTE_" + uuid.NewString()
	local := prepareDaemonRemoteWorkspace(t, root, instruction)
	workspace := "/parsar-shared-files-" + uuid.NewString()
	if err := os.WriteFile(filepath.Join(local, "shared-gate.sh"), []byte(sharedFilesGate), 0700); err != nil {
		t.Fatal(err)
	}

	s, _ := store.NewTestStore(t)
	lease, err := s.AcquireExecutionLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := lease.Close(cleanup); err != nil {
			t.Error("execution lease cleanup failed", err)
		}
	})
	tenant := uuid.NewString()
	principal := store.FixtureExecutorPrincipal(t, s, tenant)
	configuration, err := json.Marshal(map[string]any{"environment": map[string]any{
		"type": "self_hosted", "workspace_directory": workspace, "capability_directories": []string{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{
		Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "shared-files", Configuration: configuration,
	})
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
	registry, err := codex.New(codex.Config{
		Store: s, CheckOwnership: lease.Ping, ReplaceConnection: lease.Store().ReplaceEnvironmentConnection,
		ObserveConnection: lease.Store().ObserveEnvironmentConnection, PublicURL: "http://" + server.Listener.Addr().String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	observation := &relayObservation{}
	handler := registry.Handler()
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		handler.ServeHTTP(&relayResponse{ResponseWriter: w, observation: observation, path: req.URL.Path}, req)
	})
	server.Start()
	t.Cleanup(func() { registry.Close(); server.Close() })
	startDaemonRemoteExecutor(t, ctx, root, local, workspace, binary, image, server.URL, environment.ID, credential)
	awaitDaemonRemoteCondition(t, ctx, 30*time.Second, "caller executor connection", func() bool {
		connected, err := registry.Connected(ctx, tenant, environment.ID)
		return err == nil && connected
	})
	proof := map[string]any{
		"scope":          "actual PostgreSQL/registry and native shared filesystem; private Session setup, no public file endpoint or daemon cutover",
		"environment_id": environment.ID, "session_id": session.ID, "remote_workspace": workspace,
		"native_version": strings.TrimSpace(string(version)), "phases": map[string]sharedFilesProbeProof{},
		"relay_observations": map[string]map[string]int{},
		"limitations":        profile.limitations,
		"transport":          profile.transport,
	}
	secrets := []string{key, credential.Token}
	defer func() { persistDaemonRemoteProof(t, root, proof, secrets) }()
	var previous sharedFilesProbeProof
	processIDs := []int{}
	for index, phase := range []string{"first", "fresh"} {
		owner, stopOwner := context.WithCancel(ctx)
		t.Cleanup(stopOwner)
		harnessToken, releaseHarness, err := registry.IssueHarnessCredential(owner, tenant, environment.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(releaseHarness)
		secrets = append(secrets, harnessToken)
		probeEnvironment := []string{
			"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(root, "harness"), "CODEX_HOME=" + filepath.Join(root, "harness", "codex"),
			"PARSAR_SHARED_FILES_ROOT=" + root, "PARSAR_SHARED_FILES_WORKSPACE=" + workspace,
			"PARSAR_SHARED_FILES_CALLER_HOME=" + callerHome, "TMPDIR=" + root,
			"PARSAR_PLACEMENT_MODEL_KEY_FILE=" + keyFile, "PARSAR_PROBE_MODEL_KEY=" + key, "PARSAR_CODEX_BINARY=" + binary,
			"CODEX_EXEC_SERVER_NOISE_REGISTRY_URL=" + server.URL, "CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID=" + environment.ID,
			"CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN=" + harnessToken, "NO_PROXY=127.0.0.1,localhost", "RUST_LOG=off",
		}
		for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY"} {
			if value := os.Getenv(name); value != "" {
				probeEnvironment = append(probeEnvironment, name+"="+value)
			}
		}
		process := startPublicNativeProcess(t, ctx, root, phase, probeEnvironment, probe, phase)
		processIDs = append(processIDs, process.command.Process.Pid)
		t.Cleanup(func() {
			_ = os.WriteFile(filepath.Join(local, phase+".release"), []byte("cleanup\n"), 0600)
			_ = os.WriteFile(filepath.Join(root, phase+"-active-observed"), []byte("cleanup\n"), 0600)
			_ = os.WriteFile(filepath.Join(root, phase+"-release"), []byte("cleanup\n"), 0600)
		})
		awaitSharedFilesSignal(t, ctx, process, filepath.Join(local, phase+".heartbeat"), 180*time.Second)
		if _, err := os.Stat(filepath.Join(local, phase+".release")); !os.IsNotExist(err) {
			t.Fatal("native gate released before independent active observation")
		}
		proof["relay_observations"].(map[string]map[string]int)[phase+"_active"] = assertSharedFilesPair(t, observation, index+1)
		writeSharedFilesSignal(t, filepath.Join(root, phase+"-active-observed"))
		awaitSharedFilesSignal(t, ctx, process, filepath.Join(root, phase+"-ready.json"), 180*time.Second)
		checkpoint := readSharedFilesProof(t, filepath.Join(root, phase+"-ready.json"))
		assertSharedFilesProof(t, checkpoint, phase, workspace, local, profile)
		proof["relay_observations"].(map[string]map[string]int)[phase+"_completed"] = assertSharedFilesPair(t, observation, index+1)
		writeSharedFilesSignal(t, filepath.Join(root, phase+"-release"))
		select {
		case <-process.done:
			if process.err != nil {
				t.Fatal("shared native probe failed; inspect private phase evidence", phase, process.err)
			}
		case <-time.After(50 * time.Second):
			t.Fatal("shared native owner did not shut down within the fixture bound")
		}
		result := readSharedFilesProof(t, filepath.Join(root, phase+".json"))
		assertSharedFilesProof(t, result, phase, workspace, local, profile)
		if !strings.Contains(result.Answer, instruction) {
			t.Fatal("native model did not use executor-side instructions")
		}
		if !result.ShutdownCompleted || result.NativeThreadID != checkpoint.NativeThreadID || result.NativeTurnID != checkpoint.NativeTurnID {
			t.Fatal("shutdown proof changed the completed native identity")
		}
		if index == 1 && (result.NativeThreadID != previous.NativeThreadID || result.NativeTurnID == previous.NativeTurnID || result.HistoryValue != previous.HistoryValue || result.Marker == previous.Marker) {
			t.Fatal("fresh native process did not preserve history and distinct Turn/marker identities")
		}
		proof["phases"].(map[string]sharedFilesProbeProof)[phase] = result
		previous = result
		releaseHarness()
		stopOwner()
		awaitDaemonRemoteCondition(t, ctx, 30*time.Second, "executor reconnect after shared owner release", func() bool {
			observation.mu.Lock()
			executors, harnesses := observation.executors, observation.harnesses
			observation.mu.Unlock()
			connected, err := registry.Connected(ctx, tenant, environment.ID)
			return executors == index+2 && harnesses == index+1 && err == nil && connected
		})
	}
	for _, name := range []string{"shared-gate-count", "execution-count"} {
		data, err := os.ReadFile(filepath.Join(local, name))
		if err != nil || string(data) != "first\nfresh\n" {
			t.Fatal("native command was omitted or repeated", name, err)
		}
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatal("executor-only workspace appeared on the harness host")
	}
	if _, err := os.Stat(filepath.Join(local, "credential-failure")); !os.IsNotExist(err) {
		t.Fatal("model command inherited a private credential")
	}
	if processIDs[0] == processIDs[1] {
		t.Fatal("cold continuation did not use a fresh probe process")
	}
	for _, name := range []string{"first.log", "fresh.log", "first.json", "fresh.json"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range secrets {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatal("private credential appeared in probe evidence", name)
			}
		}
	}
	proof["probe_process_ids"] = processIDs
	proof["probe_sha256"] = sharedFilesHash(t, probe)
	proof["native_sha256"] = sharedFilesHash(t, binary)
	proof["launcher_sha256"] = sharedFilesHash(t, launcher)
	proof["status"] = profile.status
}

const sharedFilesGate = `#!/bin/sh
set -eu
phase="$1"
case "$phase" in first|fresh) ;; *) exit 95 ;; esac
for name in PARSAR_PROBE_MODEL_KEY PARSAR_PLACEMENT_MODEL_KEY_FILE; do
  eval 'value=${'"$name"'-}'
  test -z "$value" || { printf '%s\n' "$name" >> credential-failure; exit 23; }
done
pwd > "$phase.cwd"
printf '%s\n' "$phase" >> shared-gate-count
remaining=120
while [ ! -f "$phase.release" ]; do
  test "$remaining" -gt 0 || { printf 'fixture gate timed out\n' >&2; exit 94; }
  date +%s > "$phase.heartbeat"
  remaining=$((remaining - 1))
  sleep 1
done
cat "$phase-marker.txt"
cp "$phase-marker.txt" "$phase-artifact.txt"
exec ./placement.sh "$phase"
`

func awaitSharedFilesSignal(t *testing.T, ctx context.Context, process *relayProcess, path string, timeout time.Duration) {
	t.Helper()
	awaitDaemonRemoteCondition(t, ctx, timeout, "shared native checkpoint "+filepath.Base(path), func() bool {
		select {
		case <-process.done:
			t.Fatal("shared native probe ended before checkpoint; inspect private phase log", process.err)
		default:
		}
		_, err := os.Stat(path)
		return err == nil
	})
}

func writeSharedFilesSignal(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("continue\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func readSharedFilesProof(t *testing.T, path string) sharedFilesProbeProof {
	t.Helper()
	data, err := os.ReadFile(path)
	var proof sharedFilesProbeProof
	if err != nil || json.Unmarshal(data, &proof) != nil {
		t.Fatal("invalid shared filesystem proof", path, err)
	}
	return proof
}

func assertSharedFilesPair(t *testing.T, observation *relayObservation, pairs int) map[string]int {
	t.Helper()
	observation.mu.Lock()
	defer observation.mu.Unlock()
	if observation.harnesses != pairs || observation.executors != pairs || observation.validations != pairs || observation.harness == nil {
		t.Fatal("filesystem/model work replaced or duplicated the authorized native pair")
	}
	return map[string]int{"harnesses": observation.harnesses, "executors": observation.executors, "validations": observation.validations, "connects": observation.connects}
}

func assertSharedFilesProof(t *testing.T, proof sharedFilesProbeProof, phase, workspace, local string, profile sharedFilesProfile) {
	t.Helper()
	if proof.Status != profile.status || proof.Phase != phase || proof.NativeThreadID == "" || proof.NativeTurnID == "" || proof.Marker == "" || proof.HistoryValue == "" || proof.Marker == proof.HistoryValue {
		t.Fatal("shared filesystem proof lacks its native identities or random evidence")
	}
	if profile.transport == "raw_unix_socket" && proof.Transport != profile.transport {
		t.Fatal("raw probe did not identify its qualified transport")
	}
	if proof.TurnStartedCount != 1 || proof.TurnCompletedCount != 1 || proof.CommandStartedCount != 1 || proof.CommandCompletedCount != 1 {
		t.Fatal("native probe omitted or repeated principal lifecycle observations")
	}
	command := proof.Command
	if command.ID == "" || !command.Started || !command.Completed || command.ExitCode != 7 || command.Cwd != workspace || !strings.Contains(command.Command, "./shared-gate.sh "+phase) || !strings.Contains(command.AggregatedOutput, proof.Marker) || !strings.Contains(command.AggregatedOutput, "remote-stdout:"+phase) || !strings.Contains(command.AggregatedOutput, "remote-stderr:"+phase) {
		t.Fatal("actual remote command evidence is incomplete")
	}
	if !strings.Contains(proof.Answer, proof.Marker) || !strings.Contains(proof.Answer, proof.HistoryValue) || strings.Contains(proof.Answer, "WRONG_LOCAL_INSTRUCTIONS") {
		t.Fatal("model did not use direct filesystem input or retained native history")
	}
	files := proof.Files
	binary, err := os.Stat(filepath.Join(local, "shared-binary.bin"))
	if err != nil || binary.Size() != 128*1024 {
		t.Fatal("independent binary file size differs", err)
	}
	if files.BinaryBytes != 128*1024 || files.MetadataSize != 128*1024 || files.BinarySHA256 != sharedFilesHash(t, filepath.Join(local, "shared-binary.bin")) || files.ActiveHeartbeat == "" || files.Artifact != proof.Marker+"\n" {
		t.Fatal("direct filesystem bytes, metadata or active observation is incomplete")
	}
	activeBinaryListed := false
	for _, name := range files.ActiveDirectoryNames {
		activeBinaryListed = activeBinaryListed || name == "shared-binary.bin"
	}
	if !files.ActiveBinaryVerified || !activeBinaryListed {
		t.Fatal("active native binary/hash/metadata or directory observation is incomplete")
	}
	for _, expected := range []string{"shared-binary.bin", phase + "-marker.txt", phase + "-artifact.txt"} {
		found := false
		for _, name := range files.DirectoryNames {
			found = found || name == expected
		}
		if !found {
			t.Fatal("native directory observation omitted an actual file", expected)
		}
	}
	for name, expected := range map[string]string{phase + "-marker.txt": proof.Marker + "\n", phase + "-artifact.txt": files.Artifact, phase + ".cwd": workspace + "\n"} {
		data, err := os.ReadFile(filepath.Join(local, name))
		if err != nil || string(data) != expected {
			t.Fatal("independent executor filesystem evidence differs", name, err)
		}
	}
	entries, err := os.ReadDir(local)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(local, entry.Name()))
		if err != nil || bytes.Contains(data, []byte(proof.HistoryValue)) {
			t.Fatal("history-only value reached an executor file", err)
		}
	}
}

func sharedFilesHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
