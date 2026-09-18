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
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestNativePublicSelfHostedSteeringStandalone(t *testing.T) {
	f := newPublicSelfHostedFixture(t, "empty_later", "official_self_hosted_steering_native.py")
	const gate = `#!/bin/sh
set -eu
phase="$1"
printf '%s\n' "$phase" >> gate-start-count
remaining=90
while [ ! -f "$phase.release" ]; do
  test "$remaining" -gt 0 || { printf 'fixture gate timed out\n' >&2; exit 94; }
  date +%s > "$phase.heartbeat"
  remaining=$((remaining - 1))
  sleep 1
done
exec ./placement.sh "$phase"
`
	if err := os.WriteFile(filepath.Join(f.local, "gate.sh"), []byte(gate), 0700); err != nil {
		t.Fatal(err)
	}
	f.startDaemon(t)
	t.Cleanup(func() {
		for _, phase := range []string{"first", "resumed"} {
			_ = os.WriteFile(filepath.Join(f.local, phase+".release"), []byte("cleanup\n"), 0600)
		}
	})
	awaitHeartbeat := func(phase string) {
		t.Helper()
		awaitDaemonRemoteCondition(t, f.ctx, 150*time.Second, "actual "+phase+" command heartbeat", func() bool {
			select {
			case <-f.observer.done:
				t.Fatal("public steering client exited before native gate; inspect private proof")
			default:
			}
			_, err := os.Stat(filepath.Join(f.local, phase+".heartbeat"))
			return err == nil
		})
	}
	releaseGate := func(phase string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(f.local, phase+".release"), []byte("continue\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	awaitHeartbeat("first")
	writePublicNativeJSON(t, filepath.Join(f.observer.directory, "steer-ready.json"), map[string]string{"environment_id": f.environmentID})
	submitted := awaitPublicNativeSignal(t, f.ctx, f.observer, "steer-submitted", 30*time.Second)
	firstID := submitted["turn_id"]
	inputs, err := f.store.ListTurnInputs(f.ctx, f.tenant, f.sessionID, firstID, 0, 100)
	if err != nil || len(inputs) != 2 || inputs[1].Kind != "message" || submitted["value"] == "" || !strings.Contains(string(inputs[1].Payload), submitted["value"]) {
		t.Fatal("active public input did not target the original Turn exactly once", err)
	}
	sequence := inputs[1].Sequence
	var releaseReceipt proto.PromptSteerAckPayload
	awaitDaemonRemoteCondition(t, f.ctx, 30*time.Second, "actual native steering write", func() bool {
		for _, event := range f.events(t, firstID) {
			if event.Type == proto.TypePromptSteerAck {
				var receipt proto.PromptSteerAckPayload
				if event.DecodePayload(&receipt) != nil {
					t.Fatal("invalid native steering receipt")
				}
				if receipt.InputID == strconv.FormatInt(sequence, 10) && (receipt.Written || receipt.Accepted) {
					releaseReceipt = receipt
					return true
				}
			}
		}
		return false
	})
	// Written releases this test gate only; final acceptance below requires native Accepted.
	releaseGate("first")
	first := awaitPublicNativeSignal(t, f.ctx, f.observer, "first-completed", 150*time.Second)
	if first["turn_id"] != firstID {
		t.Fatal("steering completed a different public Turn")
	}
	completed, err := f.store.GetTurn(f.ctx, f.tenant, f.sessionID, firstID)
	var outcome execution.Result
	if err != nil || completed.Status != store.TurnCompleted || json.Unmarshal(completed.Outcome, &outcome) != nil || outcome.AppliedThrough != sequence {
		t.Fatal("completed Turn did not retain native steering application", err)
	}
	var applied []proto.PromptSteerAckPayload
	for _, event := range f.events(t, firstID) {
		if event.Type == proto.TypePromptSteerAck {
			var receipt proto.PromptSteerAckPayload
			if event.DecodePayload(&receipt) != nil || receipt.InputID != strconv.FormatInt(sequence, 10) {
				t.Fatal("native steering receipt has the wrong input identity")
			}
			if receipt.Accepted {
				applied = append(applied, receipt)
			}
		}
	}
	if len(applied) != 1 || applied[0].ErrorCode != "" {
		t.Fatal("active input lacks exactly one actual native acceptance receipt")
	}
	binding, err := f.store.GetSessionExecutionBinding(f.ctx, f.tenant, f.sessionID)
	if err != nil || binding.Device.ID != f.deviceID || binding.NativeSessionID == "" {
		t.Fatal("steered Turn lacks native device/history binding", err)
	}
	firstStarts, err := os.ReadFile(filepath.Join(f.root, "native-starts"))
	if err != nil || len(strings.Fields(string(firstStarts))) != 1 {
		t.Fatal("steering started another harness instead of using the active one")
	}
	awaitEnvironmentConnectionState(t, f.ctx, f.store, f.tenant, f.environmentID, "connected")
	writePublicNativeJSON(t, filepath.Join(f.observer.directory, "resume-ready.json"), map[string]string{"session_id": f.sessionID})
	awaitHeartbeat("resumed")
	writePublicNativeJSON(t, filepath.Join(f.observer.directory, "resumed-active.json"), map[string]string{"session_id": f.sessionID})
	second := awaitPublicNativeSignal(t, f.ctx, f.observer, "old-steer-retried", 30*time.Second)
	secondID := second["turn_id"]
	active, err := f.store.GetTurn(f.ctx, f.tenant, f.sessionID, secondID)
	if err != nil || active.Status != store.TurnInProgress || secondID == firstID {
		t.Fatal("old active-input retry changed later execution", err)
	}
	secondInputs, err := f.store.ListTurnInputs(f.ctx, f.tenant, f.sessionID, secondID, 0, 100)
	if err != nil || len(secondInputs) != 1 || strings.Contains(string(secondInputs[0].Payload), submitted["value"]) {
		t.Fatal("old active input was replayed into the cold Turn", err)
	}
	releaseGate("resumed")
	select {
	case <-f.observer.done:
		f.observer.finish(t)
	case <-f.ctx.Done():
		t.Fatal("public steering continuation timed out; inspect private proof")
	}
	finalBinding, err := f.store.GetSessionExecutionBinding(f.ctx, f.tenant, f.sessionID)
	if err != nil || finalBinding != binding {
		t.Fatal("cold steering continuation changed native history binding", err)
	}
	for _, event := range f.events(t, secondID) {
		if event.Type == proto.TypePromptSteerAck {
			t.Fatal("old active-input retry reached the later native Turn")
		}
	}
	settled, err := f.store.GetEnvironmentInputReservation(f.ctx, f.tenant, f.sessionID, f.reservation.ID)
	if err != nil || settled.State != store.EnvironmentInputAdmitted || settled.IsInitial || !settled.Deadline.Equal(f.reservation.Deadline) {
		t.Fatal("active inputs changed the original reservation", err)
	}
	for index, phase := range []string{"first", "resumed"} {
		assertDaemonRemoteCommand(t, f.events(t, []string{firstID, secondID}[index]), phase, f.workspace)
		cwd, err := os.ReadFile(filepath.Join(f.local, phase+".cwd"))
		if err != nil || strings.TrimSpace(string(cwd)) != f.workspace {
			t.Fatal("real command did not use the executor-only workspace")
		}
	}
	for name, want := range map[string]string{"execution-count": "first\nresumed\n", "gate-start-count": "first\nresumed\n", "retained.txt": "remote-file-content\n"} {
		value, err := os.ReadFile(filepath.Join(f.local, name))
		if err != nil || string(value) != want {
			t.Fatal("real steering/continuation side effects differ", name, err)
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
		t.Fatal("steering continuation did not use exactly one fresh harness per Turn")
	}
	artifact, err := os.ReadFile(f.serverBinary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(artifact)
	proof := map[string]any{
		"status": "built_service_public_self_hosted_steering_verified", "case": "public_steering", "completed_turns": 2,
		"session_id": f.sessionID, "environment_id": f.environmentID, "native_thread_id": binding.NativeSessionID,
		"server_binary": f.serverBinary, "server_sha256": hex.EncodeToString(digest[:]), "native_version": f.version,
		"launcher_remote_url": f.remoteURL, "launcher_environment_id": f.environmentID, "executor_key_id": f.executor.KeyID,
		"native_harness_processes": processes, "gate_release_receipt": releaseReceipt, "native_accepted_receipts": applied,
		"steered_turn_id": firstID, "input_sequence": sequence, "applied_through": outcome.AppliedThrough,
		"old_input_did_not_retarget": true, "command_execution_count": "first\nresumed\n",
		"public_evidence": filepath.Join(f.observer.directory, "public-steering-proof.json"),
		"limits":          "Written only releases the fixture gate. Accepted, applied cursor and model answers establish this workflow; no general crash recovery or OS-quiescence claim.",
	}
	persistDaemonRemoteProof(t, f.root, proof, f.secrets)
	t.Log("built standalone public self-hosted steering real-provider evidence", f.root)
}
