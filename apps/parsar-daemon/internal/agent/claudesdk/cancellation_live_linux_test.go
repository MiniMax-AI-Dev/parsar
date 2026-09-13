//go:build linux

package claudesdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func TestLiveClaudeSDKCancelResume(t *testing.T) {
	entrypoint := os.Getenv("PARSAR_CLAUDE_SDK_ENTRYPOINT")
	keyFile := os.Getenv("PARSAR_CLAUDE_SDK_MINIMAX_KEY_FILE")
	if entrypoint == "" || keyFile == "" {
		t.Skip("real cancellation acceptance requires explicit SDK entrypoint and private key file")
	}
	proofRoot := os.Getenv("PARSAR_CLAUDE_SDK_PROOF_DIR")
	if !filepath.IsAbs(proofRoot) {
		t.Fatal("PARSAR_CLAUDE_SDK_PROOF_DIR must be an absolute managed directory")
	}
	root, err := os.MkdirTemp(proofRoot, "claude-cancel-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("real cancellation evidence: %s", root)
	t.Setenv("PARSAR_HOME", root)
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	config := Config{Entrypoint: entrypoint, StateDir: filepath.Join(root, "state"), Env: []string{
		"ANTHROPIC_BASE_URL=https://api.minimax.cn/anthropic", "ANTHROPIC_AUTH_TOKEN=" + strings.TrimSpace(string(key)),
		"ANTHROPIC_API_KEY=", "CLAUDE_CODE_OAUTH_TOKEN=", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1",
		"ANTHROPIC_DEFAULT_SONNET_MODEL=MiniMax-M3", "ANTHROPIC_DEFAULT_OPUS_MODEL=MiniMax-M3", "ANTHROPIC_DEFAULT_HAIKU_MODEL=MiniMax-M3",
	}}
	readiness, err := CheckRuntime(context.Background(), config)
	if err != nil {
		t.Fatal("real runtime readiness failed", err)
	}
	readinessJSON, _ := json.MarshalIndent(readiness, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "readiness.json"), readinessJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	type evidence struct {
		NodePID    int               `json:"node_pid"`
		NativePIDs []int             `json:"native_pids"`
		CancelMS   int64             `json:"cancel_milliseconds,omitempty"`
		Cancelled  bool              `json:"cancelled"`
		Failure    string            `json:"failure,omitempty"`
		Outcome    proto.DonePayload `json:"outcome"`
		Events     []proto.Envelope  `json:"events"`
	}
	run := func(prompt, resume string, cancelOnText bool) evidence {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		out := make(chan proto.Envelope, 64)
		request := proto.PromptRequestPayload{RunID: uuid.NewString(), Prompt: prompt, AgentSessionID: resume, StrictResume: true, ReleaseOnCompletion: true, ObserveMessages: true, DisableExecutionEnvironment: true, DisableSubagents: true, ExecutionControls: &proto.ExecutionControls{WebSearch: "disabled", TextVerbosity: "medium"}, AgentOptions: map[string]any{"model": "MiniMax-M3", "system_prompt": "Follow the user's requested format. Preserve the exact verification value in conversation history. Use no tools."}}
		running, err := NewFactory(config)(ctx, request, out)
		if err != nil {
			t.Fatal(err)
		}
		s := running.(*session)
		defer s.Cancel(ctx)
		proof := evidence{NodePID: s.process.Cmd.Process.Pid}
		done := false
		for event := range out {
			proof.Events = append(proof.Events, event)
			if event.Type == proto.TypeDelta && len(proof.NativePIDs) == 0 {
				raw, _ := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", proof.NodePID, proof.NodePID))
				for _, value := range strings.Fields(string(raw)) {
					pid, _ := strconv.Atoi(value)
					args, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
					if bytes.Contains(args, []byte("\x00--input-format\x00stream-json\x00")) && bytes.Contains(args, []byte("\x00--output-format\x00stream-json\x00")) {
						proof.NativePIDs = append(proof.NativePIDs, pid)
					}
				}
			}
			switch event.Type {
			case proto.TypeDelta:
				if cancelOnText && !proof.Cancelled {
					select {
					case <-s.process.Done():
						t.Fatal("native execution ended before cancellation")
					default:
					}
					started := time.Now()
					if err := s.Cancel(ctx); err != nil {
						t.Fatal("live cancellation failed", err)
					}
					proof.CancelMS = time.Since(started).Milliseconds()
					proof.Cancelled = true
					proof.Outcome = s.CancellationOutcome()
				}
			case proto.TypeError:
				var payload proto.ErrorPayload
				_ = event.DecodePayload(&payload)
				proof.Failure = payload.Error
			case proto.TypeDone:
				done = true
				var payload proto.DonePayload
				if err := event.DecodePayload(&payload); err != nil {
					t.Fatal(err)
				}
				if proof.Cancelled {
					expected, _ := json.Marshal(proof.Outcome)
					actual, _ := json.Marshal(payload)
					if !bytes.Equal(expected, actual) {
						t.Fatal("live cancellation outcome differs from Done")
					}
				}
				proof.Outcome = payload
			}
		}
		data, _ := json.MarshalIndent(proof, "", "  ")
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("execution-%d.json", proof.NodePID)), data, 0o600); err != nil {
			t.Fatal(err)
		}
		if !done || len(proof.NativePIDs) == 0 || proof.Cancelled != cancelOnText {
			t.Fatal("missing real execution/completion/cancellation evidence")
		}
		select {
		case <-s.process.Done():
		default:
			t.Fatal("completion preceded owned process release")
		}
		for _, pid := range append([]int{proof.NodePID}, proof.NativePIDs...) {
			if value, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
				fields := strings.Fields(string(value)[strings.LastIndex(string(value), ")")+1:])
				if len(fields) == 0 || fields[0] != "Z" {
					t.Fatalf("execution process %d remains alive", pid)
				}
			}
		}
		return proof
	}
	nonce := "cancel-history-" + uuid.NewString()
	first := run("Remember this exact verification value: "+nonce+". First repeat it, then write two hundred numbered sentences about trees. Do not use tools.", "", true)
	id, _ := first.Outcome.Metadata[proto.DoneMetaAgentSessionID].(string)
	if id == "" || first.Outcome.Content == "" || first.Failure == "" {
		t.Fatal("live cancellation lost identity, partial output or interruption evidence")
	}
	second := run("Return only the exact cancel-history verification value in the earlier user request. Ignore the earlier request for numbered sentences.", id, false)
	if second.Failure != "" || second.Outcome.Metadata[proto.DoneMetaAgentSessionID] != id || !strings.Contains(second.Outcome.Content, nonce) || first.NodePID == second.NodePID {
		t.Fatalf("cold continuation did not preserve identity/history; evidence %s", root)
	}
	data, _ := json.MarshalIndent(map[string]any{"scope": "private Go factory -> maintained SDK/native -> real MiniMax cancellation and cold continuation; public admission remains separate", "verification_value": nonce, "executions": []evidence{first, second}}, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "proof.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
