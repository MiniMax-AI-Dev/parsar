package store_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestNativePublicSelfHostedCancellationStandalone(t *testing.T) {
	f := newPublicSelfHostedFixture(t, "empty_later", "official_self_hosted_cancel_native.py")
	// Hold the later native command open while the client retries the old cancel key.
	resumedScript := `#!/bin/sh
set -eu
printf 'started\n' >> resumed-start-count
while [ ! -f resumed.release ]; do date +%s > resumed.heartbeat; sleep 1; done
exec ./placement.sh resumed
`
	if err := os.WriteFile(filepath.Join(f.local, "resume.sh"), []byte(resumedScript), 0700); err != nil {
		t.Fatal(err)
	}
	daemon := f.startDaemon(t)
	awaitDaemonRemoteCondition(t, f.ctx, 150*time.Second, "actual long command heartbeat", func() bool {
		_, err := os.Stat(filepath.Join(f.local, "cancel.heartbeat"))
		return err == nil
	})
	f.observeHarnessOwner(t)
	writePublicNativeJSON(t, filepath.Join(f.observer.directory, "cancel-ready.json"), map[string]string{"environment_id": f.environmentID})
	requested := awaitPublicNativeSignal(t, f.ctx, f.observer, "cancel-requested", 150*time.Second)
	unix, err := strconv.ParseFloat(requested["request_started_unix"], 64)
	if err != nil || unix <= 0 {
		t.Fatal("missing public cancellation request time")
	}
	cancelAt := time.UnixMilli(int64(unix * 1000))
	// Measure actual process exit and stopped side effects separately from HTTP and Turn status.
	awaitDaemonRemoteExit(t, f.ctx, f.container, f.local)
	observedExitSeconds := time.Since(cancelAt).Seconds()
	first := awaitPublicNativeSignal(t, f.ctx, f.observer, "first-cancelled", 45*time.Second)
	if first["turn_id"] != requested["turn_id"] {
		t.Fatal("cancelled Turn differs from public request target")
	}
	binding, err := f.store.GetSessionDevice(f.ctx, f.tenant, f.sessionID)
	if err != nil || binding.ID != f.deviceID || binding.NativeSessionID == "" {
		t.Fatal("cancellation lost native device/history binding", err)
	}
	var receipt *proto.InteractionDecisionAckPayload
	firstEvents := f.events(t, first["turn_id"])
	assertDaemonRemoteCommand(t, firstEvents, "first", f.workspace)
	for _, event := range firstEvents {
		if event.Type != "cancel_receipt" {
			continue
		}
		var value proto.InteractionDecisionAckPayload
		if receipt != nil || event.DecodePayload(&value) != nil {
			t.Fatal("invalid or repeated native cancellation receipt")
		}
		receipt = &value
	}
	if receipt == nil || !receipt.Applied || receipt.Outcome == nil || receipt.ErrorCode != "" || receipt.DeliveryID != "cancel:"+first["turn_id"] || receipt.Outcome.Metadata[proto.DoneMetaAgentSessionID] != binding.NativeSessionID {
		t.Fatal("public cancellation lacks its actual native outcome/identity")
	}
	firstStarts, err := os.ReadFile(filepath.Join(f.root, "native-starts"))
	if err != nil || len(strings.Fields(string(firstStarts))) != 1 {
		t.Fatal("cancelled input started another native harness")
	}
	awaitEnvironmentConnectionState(t, f.ctx, f.store, f.tenant, f.environmentID, "connected")
	select {
	case <-daemon.done:
		t.Fatal("daemon exited during cancellation")
	default:
	}
	writePublicNativeJSON(t, filepath.Join(f.observer.directory, "resume-ready.json"), map[string]string{"session_id": f.sessionID})
	awaitDaemonRemoteCondition(t, f.ctx, 150*time.Second, "actual resumed native command", func() bool {
		_, err := os.Stat(filepath.Join(f.local, "resumed.heartbeat"))
		return err == nil
	})
	f.observeHarnessOwner(t)
	writePublicNativeJSON(t, filepath.Join(f.observer.directory, "resumed-active.json"), map[string]string{"session_id": f.sessionID})
	second := awaitPublicNativeSignal(t, f.ctx, f.observer, "old-cancel-retried", 30*time.Second)
	before, err := os.ReadFile(filepath.Join(f.local, "resumed.heartbeat"))
	if err != nil {
		t.Fatal(err)
	}
	awaitDaemonRemoteCondition(t, f.ctx, 5*time.Second, "resumed heartbeat after old cancellation replay", func() bool {
		after, err := os.ReadFile(filepath.Join(f.local, "resumed.heartbeat"))
		return err == nil && string(after) != string(before)
	})
	active, err := f.store.GetTurn(f.ctx, f.tenant, f.sessionID, second["turn_id"])
	if err != nil || active.Status != store.TurnInProgress || !active.CancelRequestedAt.IsZero() {
		t.Fatal("old public cancellation affected the active resumed Turn", err)
	}
	if err := os.WriteFile(filepath.Join(f.local, "resumed.release"), []byte("continue\n"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.observer.done:
		f.observer.finish(t)
	case <-f.ctx.Done():
		t.Fatal("public cancellation continuation timed out; inspect private proof")
	}
	finalBinding, err := f.store.GetSessionDevice(f.ctx, f.tenant, f.sessionID)
	if err != nil || finalBinding != binding {
		t.Fatal("cold continuation changed native device/history binding", err)
	}
	data, err := os.ReadFile(filepath.Join(f.observer.directory, "public-cancellation-proof.json"))
	if err != nil {
		t.Fatal(err)
	}
	var publicProof struct {
		Case  string `json:"case"`
		Turns []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"turns"`
	}
	if json.Unmarshal(data, &publicProof) != nil || publicProof.Case != "public_cancellation" || len(publicProof.Turns) != 2 || publicProof.Turns[0].ID != first["turn_id"] || publicProof.Turns[0].Status != "cancelled" || publicProof.Turns[1].ID != second["turn_id"] || publicProof.Turns[1].Status != "completed" {
		t.Fatal("public proof lacks cancelled and cold-resumed real Turns")
	}
	settled, err := f.store.GetEnvironmentInputReservation(f.ctx, f.tenant, f.sessionID, f.reservation.ID)
	if err != nil || settled.State != store.EnvironmentInputAdmitted || settled.IsInitial || !settled.Deadline.Equal(f.reservation.Deadline) {
		t.Fatal("cancellation/retries changed original reservation identity", err)
	}
	assertDaemonRemoteCommand(t, f.events(t, second["turn_id"]), "resumed", f.workspace)
	for _, phase := range []string{"first", "resumed"} {
		cwd, err := os.ReadFile(filepath.Join(f.local, phase+".cwd"))
		if err != nil || strings.TrimSpace(string(cwd)) != f.workspace {
			t.Fatal("real command did not use the executor-only workspace")
		}
	}
	for name, want := range map[string]string{"execution-count": "first\nresumed\n", "resumed-start-count": "started\n", "retained.txt": "remote-file-content\n"} {
		value, err := os.ReadFile(filepath.Join(f.local, name))
		if err != nil || string(value) != want {
			t.Fatal("real cancellation/continuation side effects differ", name, err)
		}
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
		t.Fatal("continuation did not start exactly one fresh harness per Turn")
	}
	artifact, err := os.ReadFile(f.serverBinary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(artifact)
	proof := map[string]any{
		"status": "built_service_public_self_hosted_cancellation_verified", "case": "public_cancellation",
		"session_id": f.sessionID, "environment_id": f.environmentID, "server_binary": f.serverBinary, "server_sha256": hex.EncodeToString(digest[:]),
		"native_version": f.version, "native_thread_id": binding.NativeSessionID, "native_harness_processes": processes,
		"cancelled_turn": first["turn_id"], "completed_turn": second["turn_id"], "native_cancel_receipt": receipt,
		"cancel_request_to_observed_exit_seconds": observedExitSeconds, "process_exit_and_stopped_heartbeat_observed": true,
		"executor_and_daemon_retained": true, "old_cancel_retry_did_not_retarget": true, "command_execution_count": "first\nresumed\n",
		"launcher_remote_url": f.remoteURL, "launcher_environment_id": f.environmentID, "executor_key_id": f.executor.KeyID,
		"reservation_id": f.reservation.ID, "reservation_deadline": f.reservation.Deadline,
		"public_evidence": filepath.Join(f.observer.directory, "public-cancellation-proof.json"),
		"limits":          "Observed native cleanup timing is scenario-specific; no general OS quiescence, pre-Start Outcome or complete final usage claim.",
	}
	f.assertHarnessReleased(t, proof)
	persistDaemonRemoteProof(t, f.root, proof, f.secrets)
	t.Log("built standalone public cancellation real-provider evidence", f.root)
}
