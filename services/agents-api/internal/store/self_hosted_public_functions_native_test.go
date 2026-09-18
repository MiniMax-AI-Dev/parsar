package store_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestNativePublicSelfHostedFunctionsStandalone(t *testing.T) {
	f := newPublicSelfHostedFixture(t, "empty_later", "official_self_hosted_functions_native.py")
	f.startDaemon(t)
	first := awaitPublicNativeSignal(t, f.ctx, f.observer, "first-completed", 180*time.Second)
	binding, err := f.store.GetSessionExecutionBinding(f.ctx, f.tenant, f.sessionID)
	if err != nil || binding.Device.ID != f.deviceID || binding.NativeSessionID == "" {
		t.Fatal("first function Turn lacks native device/history binding", err)
	}
	call, err := f.store.GetFunctionCall(f.ctx, f.tenant, f.sessionID, first["turn_id"], first["call_id"])
	if err != nil || !call.Applied || len(call.Result) == 0 {
		t.Fatal("first public function result lacks a native application receipt", err)
	}
	firstStarts, err := os.ReadFile(filepath.Join(f.root, "native-starts"))
	if err != nil || len(strings.Fields(string(firstStarts))) != 1 {
		t.Fatal("first function Turn started more than one native harness")
	}
	awaitEnvironmentConnectionState(t, f.ctx, f.store, f.tenant, f.environmentID, "connected")
	writePublicNativeJSON(t, filepath.Join(f.observer.directory, "resume-ready.json"), map[string]string{"session_id": f.sessionID})
	select {
	case <-f.observer.done:
		f.observer.finish(t)
	case <-f.ctx.Done():
		t.Fatal("public function continuation timed out; inspect private proof")
	}
	finalBinding, err := f.store.GetSessionExecutionBinding(f.ctx, f.tenant, f.sessionID)
	if err != nil || finalBinding != binding {
		t.Fatal("function continuation changed native history/device binding", err)
	}
	data, err := os.ReadFile(filepath.Join(f.observer.directory, "public-functions-proof.json"))
	if err != nil {
		t.Fatal(err)
	}
	var publicProof struct {
		Case   string `json:"case"`
		Status string `json:"status"`
		Turns  []struct {
			ID string `json:"id"`
		} `json:"turns"`
		Calls       []string `json:"calls"`
		Submissions []struct {
			Event map[string]any `json:"event"`
		} `json:"accepted_results"`
	}
	if json.Unmarshal(data, &publicProof) != nil || publicProof.Case != "public_functions" || publicProof.Status != "public_functions_and_cold_continuation_verified" || len(publicProof.Turns) != 2 || len(publicProof.Calls) != 2 || len(publicProof.Submissions) != 2 || publicProof.Turns[0].ID != first["turn_id"] {
		t.Fatal("public proof does not contain the two accepted function Turns")
	}
	settled, err := f.store.GetEnvironmentInputReservation(f.ctx, f.tenant, f.sessionID, f.reservation.ID)
	if err != nil || settled.State != store.EnvironmentInputAdmitted || settled.IsInitial || !settled.Deadline.Equal(f.reservation.Deadline) {
		t.Fatal("function results changed the original message reservation", err)
	}
	receipts := make([]store.FunctionCall, 0, 2)
	for index, phase := range []string{"first", "resumed"} {
		turnID := publicProof.Turns[index].ID
		call, err := f.store.GetFunctionCall(f.ctx, f.tenant, f.sessionID, turnID, publicProof.Calls[index])
		if err != nil || !call.Applied || call.ExecutorCallID == "" || call.Name != "lookup_festival" {
			t.Fatal("public function result was not acknowledged by the native adapter", err)
		}
		var arguments, result map[string]any
		if json.Unmarshal(call.Arguments, &arguments) != nil || !reflect.DeepEqual(arguments, map[string]any{"phase": phase}) || json.Unmarshal(call.Result, &result) != nil {
			t.Fatal("stored function arguments/result differ from the public call")
		}
		expected := publicProof.Submissions[index].Event
		for _, field := range []string{"type", "turn_id", "call_id"} {
			delete(expected, field)
		}
		if !reflect.DeepEqual(result, expected) {
			t.Fatal("stored function result did not preserve the SDK submission")
		}
		receipts = append(receipts, call)
		assertDaemonRemoteCommand(t, f.events(t, turnID), phase, f.workspace)
		cwd, err := os.ReadFile(filepath.Join(f.local, phase+".cwd"))
		if err != nil || strings.TrimSpace(string(cwd)) != f.workspace {
			t.Fatal("function Turn command did not run in the executor-only workspace")
		}
	}
	count, err := os.ReadFile(filepath.Join(f.local, "execution-count"))
	if err != nil || string(count) != "first\nresumed\n" {
		t.Fatal("function or input retry repeated or omitted real command execution")
	}
	retained, err := os.ReadFile(filepath.Join(f.local, "retained.txt"))
	if err != nil || string(retained) != "remote-file-content\n" {
		t.Fatal("remote file did not persist between function Turns")
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
		t.Fatal("function continuation did not cold-start exactly one fresh harness per Turn")
	}
	artifact, err := os.ReadFile(f.serverBinary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(artifact)
	proof := map[string]any{
		"status": "built_service_public_self_hosted_functions_verified", "case": "public_functions", "completed_turns": 2,
		"session_id": f.sessionID, "environment_id": f.environmentID, "native_thread_id": binding.NativeSessionID,
		"server_binary": f.serverBinary, "server_sha256": hex.EncodeToString(digest[:]), "native_version": f.version,
		"launcher_remote_url": f.remoteURL, "launcher_environment_id": f.environmentID, "executor_key_id": f.executor.KeyID,
		"native_harness_processes": processes, "native_function_receipts": receipts, "command_execution_count": string(count),
		"old_result_retry_did_not_retarget": true, "public_evidence": filepath.Join(f.observer.directory, "public-functions-proof.json"),
		"limits": "One success mapping and one SDK-generated error through real native callbacks; no complete tool-set, image understanding or crash recovery claim.",
	}
	persistDaemonRemoteProof(t, f.root, proof, f.secrets)
	t.Log("built standalone public self-hosted function real-provider evidence", f.root)
}
