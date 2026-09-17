//go:build linux

package claudesdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// Run only inside a separately qualified outer placement, with its pinned native
// dependencies. This fixture does not create isolation or public admission.
func TestLiveClaudeWorkspaceFactory(t *testing.T) {
	testLiveClaudeWorkspace(t, false)
}

func TestLiveClaudePreparedWorkspace(t *testing.T) {
	testLiveClaudeWorkspace(t, true)
}

func testLiveClaudeWorkspace(t *testing.T, explicitPreparation bool) {
	configFile := os.Getenv("PARSAR_CLAUDE_WORKSPACE_LIVE_CONFIG")
	if configFile == "" {
		t.Skip("requires explicit qualified placement and real provider configuration")
	}
	var placement struct {
		Node, Entrypoint, Proof, Scratch, KeyFile, DependencyPath, Proxy string
	}
	raw, err := os.ReadFile(configFile)
	if err != nil || json.Unmarshal(raw, &placement) != nil {
		t.Fatal("invalid private live configuration")
	}
	root, err := os.MkdirTemp(placement.Proof, "factory-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("workspace factory proof: %s", root)
	t.Setenv("PARSAR_HOME", root)
	t.Setenv("PARSAR_PARENT_SECRET", "parent-must-not-enter-workspace")
	key, err := os.ReadFile(placement.KeyFile)
	if err != nil || len(bytes.TrimSpace(key)) == 0 {
		t.Fatal("private real-provider key unavailable")
	}
	scratch, err := os.MkdirTemp(placement.Scratch, "f-")
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Node: placement.Node, Entrypoint: placement.Entrypoint, StateDir: filepath.Join(root, "state"),
		Workspace: &WorkspaceConfig{Directory: filepath.Join(root, "workspace"), HomeDir: filepath.Join(root, "home"),
			ScratchDir: scratch, ProtectedDirs: []string{filepath.Dir(placement.KeyFile)}, DependencyPath: placement.DependencyPath},
		Env: []string{"ANTHROPIC_BASE_URL=https://api.minimax.cn/anthropic", "ANTHROPIC_API_KEY=", "ANTHROPIC_AUTH_TOKEN=" + strings.TrimSpace(string(key)),
			"CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1", "ANTHROPIC_DEFAULT_SONNET_MODEL=MiniMax-M3", "ANTHROPIC_DEFAULT_OPUS_MODEL=MiniMax-M3", "ANTHROPIC_DEFAULT_HAIKU_MODEL=MiniMax-M3"}}
	if placement.Proxy != "" {
		config.Env = append(config.Env, "HTTP_PROXY="+placement.Proxy, "HTTPS_PROXY="+placement.Proxy, "NO_PROXY=127.0.0.1,localhost")
	}
	for _, dir := range []string{config.StateDir, config.Workspace.Directory, config.Workspace.HomeDir} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	liveWorkspaceReadFixtures(t, config.Workspace.Directory)
	heartbeat := filepath.Join(config.Workspace.Directory, "heartbeat.txt")
	artifact := filepath.Join(config.Workspace.Directory, "value.txt")
	type evidence struct {
		Reads                 []liveWorkspaceRead `json:"reads,omitempty"`
		RunID                 string              `json:"run_id"`
		Events                []proto.Envelope    `json:"events"`
		Done                  proto.DonePayload   `json:"done"`
		Failure               string              `json:"failure,omitempty"`
		Cancelled             bool                `json:"cancelled"`
		CancelMS              int64               `json:"cancel_ms,omitempty"`
		BridgePID             int                 `json:"bridge_pid"`
		Terminals             int                 `json:"terminals"`
		Heartbeats            []string            `json:"heartbeats,omitempty"`
		PreparedPID           int                 `json:"prepared_pid,omitempty"`
		NativeBefore          string              `json:"native_before,omitempty"`
		NativeAfter           string              `json:"native_after,omitempty"`
		StartContextCancelled bool                `json:"start_context_cancelled,omitempty"`
	}
	writeEvidence := func(name string, proof evidence) {
		t.Helper()
		encoded, err := json.MarshalIndent(proof, "", "  ")
		if err != nil || bytes.Contains(encoded, bytes.TrimSpace(key)) {
			t.Fatal("cannot safely encode factory observations")
		}
		if err := os.WriteFile(filepath.Join(root, name+".json"), encoded, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(name, prompt, resume string, cancelOnEffect bool) evidence {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
		defer cancel()
		out := make(chan proto.Envelope, 64)
		req := workspaceRequest()
		req.RunID, req.Prompt, req.AgentSessionID = uuid.NewString(), prompt, resume
		req.StrictResume, req.ReleaseOnCompletion, req.ObserveMessages, req.ObserveToolObservations = true, true, true, true
		req.AgentOptions = map[string]any{"model": "MiniMax-M3", "system_prompt": "Follow the exact verification instructions using the requested native tools. Preserve conversation facts. No other files, network operations or background work."}
		proof := evidence{RunID: req.RunID}
		var running agent.Session
		var owner agent.PreparedCancellation
		if explicitPreparation {
			preparation := req
			preparation.RunID, preparation.Prompt = "", ""
			var resource agent.Prepared
			resource, err = NewPreparationFactory(config)(ctx, preparation)
			if err == nil {
				defer resource.Close()
				owner = resource.(agent.PreparedCancellation)
				proof.PreparedPID = resource.(*prepared).session.process.Cmd.Process.Pid
				proof.NativeBefore = liveWorkspaceNativeIdentity(t, proof.PreparedPID)
				before, _ := os.ReadFile(artifact)
				time.Sleep(500 * time.Millisecond)
				after, _ := os.ReadFile(artifact)
				if !bytes.Equal(before, after) || liveWorkspaceNativeIdentity(t, proof.PreparedPID) != proof.NativeBefore || len(out) != 0 {
					t.Fatal("prepared resource changed before initial input")
				}
				proof.Reads = append(proof.Reads, liveWorkspaceReads(t, ctx, resource.(agent.WorkspaceReader), config.Workspace.Directory, "prepared", "read-binary.bin", "read-empty.bin", "read-large.bin")...)
				operation, stopOperation := context.WithCancel(ctx)
				running, err = resource.Start(operation, req.RunID, req.Prompt, out)
				stopOperation()
				proof.StartContextCancelled = true
				if err == nil {
					proof.NativeAfter = liveWorkspaceNativeIdentity(t, proof.PreparedPID)
					if proof.NativeBefore != proof.NativeAfter || resource.Close() != nil {
						t.Fatal("Start replaced the native process or Close affected its transfer")
					}
				}
			}
		} else {
			running, err = NewFactory(config)(ctx, req, out)
		}
		if err != nil {
			if name == "missing-history" && running == nil && strings.Contains(err.Error(), "history_unavailable") {
				proof.Failure = err.Error()
				writeEvidence(name, proof)
				return proof
			}
			t.Fatal(err)
		}
		s := running.(*session)
		defer s.Cancel(context.Background())
		proof.BridgePID = s.process.Cmd.Process.Pid
		proof.Reads = append(proof.Reads, liveWorkspaceReads(t, ctx, s, config.Workspace.Directory, "active", "read-binary.bin", "read-empty.bin", "read-large.bin")...)
		if explicitPreparation && proof.BridgePID != proof.PreparedPID {
			t.Fatal("Start replaced the prepared bridge")
		}
		cancelOwned, outcome := s.Cancel, s.CancellationOutcome
		if owner != nil {
			cancelOwned, outcome = owner.Cancel, owner.CancellationOutcome
		}
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		for out != nil {
			select {
			case <-ctx.Done():
				t.Fatal("real factory deadline expired")
			case <-ticker.C:
				value, _ := os.ReadFile(heartbeat)
				if cancelOnEffect && !proof.Cancelled && len(value) > 0 && string(value) != "0" && string(value) != "1" {
					proof.Reads = append(proof.Reads, liveWorkspaceReads(t, ctx, s, config.Workspace.Directory, "effect", "value.txt")...)
					started := time.Now()
					if err := cancelOwned(ctx); err != nil {
						t.Fatal("factory cancellation failed", err)
					}
					proof.CancelMS = time.Since(started).Milliseconds()
					proof.Cancelled = true
				}
			case event, ok := <-out:
				if !ok {
					out = nil
					continue
				}
				proof.Events = append(proof.Events, event)
				switch event.Type {
				case proto.TypeError:
					var failure proto.ErrorPayload
					_ = event.DecodePayload(&failure)
					proof.Failure = failure.Error
				case proto.TypeDone:
					proof.Terminals++
					_ = event.DecodePayload(&proof.Done)
				}
			}
		}
		if proof.Cancelled {
			a, _ := os.ReadFile(heartbeat)
			time.Sleep(1500 * time.Millisecond)
			b, _ := os.ReadFile(heartbeat)
			proof.Heartbeats = []string{string(a), string(b)}
			if len(a) == 0 || !bytes.Equal(a, b) {
				t.Fatal("native command effects continued after Cancel")
			}
			settled, _ := json.Marshal(outcome())
			done, _ := json.Marshal(proof.Done)
			if !bytes.Equal(settled, done) {
				t.Fatal("cancellation outcome differs from terminal Done")
			}
		}
		writeEvidence(name, proof)
		if proof.Terminals != 1 || (cancelOnEffect && !proof.Cancelled) {
			t.Fatal("missing actual cancellation or unique completion")
		}
		return proof
	}
	commands := []string{
		`python3 -c "import sys; print('OBS_SUCCESS_STDOUT'); print('OBS_SUCCESS_STDERR', file=sys.stderr)"`,
		`python3 -c "import sys; print('OBS_FAILURE_STDOUT'); print('OBS_FAILURE_STDERR', file=sys.stderr); sys.exit(7)"`,
	}
	observed := run("commands", fmt.Sprintf("Execute exactly these two foreground Bash calls, sequentially, with timeout 10000. Preserve each command exactly. The second intentionally fails; do not retry or repair it. Use no other tools.\n1. %s\n2. %s", commands[0], commands[1]), "", false)
	observedID, _ := observed.Done.Metadata[proto.DoneMetaAgentSessionID].(string)
	if observedID == "" || observed.Failure != "" {
		t.Fatal("real command observation query failed")
	}
	completed := liveWorkspaceCommands(t, observed.RunID, observed.Events, commands)
	for i, expected := range []struct{ status, output string }{
		{"completed", "OBS_SUCCESS_STDERR\nOBS_SUCCESS_STDOUT"},
		{"failed", "Exit code 7\nOBS_FAILURE_STDERR\nOBS_FAILURE_STDOUT"},
	} {
		var output string
		if completed[i].Observation.Status != expected.status || json.Unmarshal(completed[i].Observation.Output, &output) != nil || output != expected.output {
			t.Fatal("native command result text/status was not preserved")
		}
	}
	nonce := "conversation-" + uuid.NewString()
	command := "python3 -u - <<'VERIFY_PY'\nimport secrets,time\nfrom pathlib import Path\nPath('value.txt').write_text(secrets.token_hex(16)+'\\n')\nfor n in range(180):\n Path('heartbeat.txt').write_text(str(n))\n time.sleep(1)\nVERIFY_PY"
	first := run("first", fmt.Sprintf("Remember the conversation-only value %s; do not write it into any file. Execute exactly one foreground Bash call with timeout 120000 and this exact command. Wait for it; use no other tools.\n%s", nonce, command), observedID, true)
	id, _ := first.Done.Metadata[proto.DoneMetaAgentSessionID].(string)
	if id == "" || id != observedID {
		t.Fatal("cancelled native Session identity unavailable")
	}
	interrupted := liveWorkspaceCommands(t, first.RunID, first.Events, []string{command})
	if interrupted[0].ID == completed[0].ID || interrupted[0].ID == completed[1].ID ||
		(interrupted[0].Observation.Status != "incomplete" && interrupted[0].Observation.Status != "failed") ||
		(interrupted[0].Observation.Status == "failed" && len(interrupted[0].Observation.Output) == 0) {
		t.Fatal("cancelled command lost its native failure or incomplete observation")
	}
	original, err := os.ReadFile(artifact)
	if err != nil || len(bytes.TrimSpace(original)) != 32 {
		t.Fatal("actual workspace artifact missing")
	}
	second := run("resumed", "Use native Read to read value.txt. Then use native Edit to append the literal suffix -resumed to its value, retaining a trailing newline. Do not use Bash. Reply with the original file value and the conversation-only value remembered earlier.", id, false)
	liveWorkspaceCommands(t, second.RunID, second.Events, nil)
	final, err := os.ReadFile(artifact)
	if err != nil || string(final) != strings.TrimSpace(string(original))+"-resumed\n" || second.Failure != "" ||
		second.Done.Metadata[proto.DoneMetaAgentSessionID] != id || !strings.Contains(second.Done.Content, nonce) ||
		!strings.Contains(second.Done.Content, strings.TrimSpace(string(original))) || first.BridgePID == second.BridgePID {
		t.Fatal("fresh-process native workspace/history continuation failed")
	}
	retained := config.StateDir + "-retained"
	if err := os.Rename(config.StateDir, retained); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove(config.StateDir)
		_ = os.Rename(retained, config.StateDir)
	}()
	if err := os.Mkdir(config.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	missing := run("missing-history", "Continue the existing Session only; do not start another Session.", id, false)
	entries, err := os.ReadDir(config.StateDir)
	after, _ := os.ReadFile(artifact)
	if missing.Failure != "claudesdk: history_unavailable" || missing.Terminals != 0 || err != nil || len(entries) != 0 || !bytes.Equal(final, after) || missing.Done.Metadata[proto.DoneMetaAgentSessionID] != nil {
		t.Fatal("missing native history did not fail before new execution")
	}
	if err := os.RemoveAll(scratch); err != nil {
		t.Fatal(err)
	}
}

func liveWorkspaceNativeIdentity(t *testing.T, bridgePID int) string {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", bridgePID, bridgePID))
	children := strings.Fields(string(raw))
	if err != nil || len(children) != 1 {
		t.Fatal("expected exactly one retained native child", err)
	}
	status, err := os.ReadFile("/proc/" + children[0] + "/stat")
	fields := strings.Fields(string(status)[strings.LastIndex(string(status), ")")+1:])
	if err != nil || len(fields) < 20 {
		t.Fatal("native process identity unavailable", err)
	}
	return children[0] + ":" + fields[19]
}
