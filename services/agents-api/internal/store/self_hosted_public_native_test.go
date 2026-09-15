package store_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
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
	f := newPublicSelfHostedFixture(t, mode, "official_self_hosted.py")
	f.startDaemon(t)
	first := awaitPublicNativeSignal(t, f.ctx, f.observer, "first-completed", 180*time.Second)
	binding, err := f.store.GetSessionDevice(f.ctx, f.tenant, f.sessionID)
	if err != nil || binding.ID != f.deviceID || binding.NativeSessionID == "" {
		t.Fatal("first public Turn lacks native device/history binding", err)
	}
	firstStarts, err := os.ReadFile(filepath.Join(f.root, "native-starts"))
	if err != nil || len(strings.Fields(string(firstStarts))) != 1 {
		t.Fatal("first input retry started another native harness")
	}
	awaitEnvironmentConnectionState(t, f.ctx, f.store, f.tenant, f.environmentID, "connected")
	writePublicNativeJSON(t, filepath.Join(f.observer.directory, "resume-ready.json"), map[string]string{"session_id": f.sessionID})
	select {
	case <-f.observer.done:
		f.observer.finish(t)
	case <-f.ctx.Done():
		t.Fatal("public second Turn timed out; inspect private proof")
	}
	finalBinding, err := f.store.GetSessionDevice(f.ctx, f.tenant, f.sessionID)
	if err != nil || finalBinding != binding {
		t.Fatal("public continuation changed native history/device binding", err)
	}
	data, err := os.ReadFile(filepath.Join(f.observer.directory, "public-environment-proof.json"))
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
	settled, err := f.store.GetEnvironmentInputReservation(f.ctx, f.tenant, f.sessionID, f.reservation.ID)
	if err != nil || settled.State != store.EnvironmentInputAdmitted || settled.IsInitial != f.reservation.IsInitial || !settled.Deadline.Equal(f.reservation.Deadline) {
		t.Fatal("public retries changed original reservation origin/deadline", err)
	}
	for index, phase := range []string{"first", "resumed"} {
		observed := f.events(t, publicProof.Turns[index].ID)
		assertDaemonRemoteCommand(t, observed, phase, f.workspace)
		cwd, err := os.ReadFile(filepath.Join(f.local, phase+".cwd"))
		if err != nil || strings.TrimSpace(string(cwd)) != f.workspace {
			t.Fatal("actual command did not run in the executor-only workspace")
		}
	}
	count, err := os.ReadFile(filepath.Join(f.local, "execution-count"))
	if err != nil || string(count) != "first\nresumed\n" {
		t.Fatal("public retry repeated or omitted real command execution")
	}
	retained, err := os.ReadFile(filepath.Join(f.local, "retained.txt"))
	if err != nil || string(retained) != "remote-file-content\n" {
		t.Fatal("remote file did not persist between public Turns")
	}
	if _, err := os.Stat(f.workspace); !os.IsNotExist(err) {
		t.Fatal("remote workspace appeared on the harness host")
	}
	if _, err := os.Stat(filepath.Join(f.local, "credential-failure")); !os.IsNotExist(err) {
		t.Fatal("native command inherited transport/provider credentials")
	}
	starts, err := os.ReadFile(filepath.Join(f.root, "native-starts"))
	processes := strings.Fields(string(starts))
	if err != nil || len(processes) != 2 || processes[0] == processes[1] {
		t.Fatal("continuation did not cold-start exactly one fresh harness per Turn")
	}
	artifact, err := os.ReadFile(f.serverBinary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(artifact)
	proof := map[string]any{
		"status": "built_service_public_self_hosted_real_execution_verified", "session_id": f.sessionID, "environment_id": f.environmentID,
		"creation_mode": mode, "reservation_id": f.reservation.ID, "reservation_initial": f.reservation.IsInitial, "reservation_deadline": f.reservation.Deadline,
		"server_binary": f.serverBinary, "server_sha256": hex.EncodeToString(digest[:]), "native_thread_id": binding.NativeSessionID,
		"native_version": f.version, "completed_turns": 2, "launcher_remote_url": f.remoteURL,
		"launcher_environment_id": f.environmentID, "executor_key_id": f.executor.KeyID, "executor_key_issued_before_session": true,
		"already_connected_second_input": true, "command_execution_count": string(count), "public_evidence": filepath.Join(f.observer.directory, "public-environment-proof.json"),
		"native_harness_processes": processes,
	}
	persistDaemonRemoteProof(t, f.root, proof, f.secrets)
	t.Log("built standalone public self-hosted real-provider evidence", f.root)
}
