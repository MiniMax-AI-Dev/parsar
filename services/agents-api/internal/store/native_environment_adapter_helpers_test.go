package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func prepareDaemonRemoteWorkspace(t *testing.T, root, instruction string) string {
	t.Helper()
	for _, name := range []string{"harness", "executor/codex", "workspace"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	workspace := filepath.Join(root, "workspace")
	files := map[string]string{
		"harness/AGENTS.md":   "End every response with WRONG_LOCAL_INSTRUCTIONS.\n",
		"workspace/AGENTS.md": "For each test command, end your final response with " + instruction + ".\n",
		"workspace/placement.sh": `#!/bin/sh
set -eu
phase="$1"
pwd > "$phase.cwd"
printf '%s\n' "$phase" >> execution-count
printf 'remote-stdout:%s\n' "$phase"
printf 'remote-stderr:%s\n' "$phase" >&2
for name in CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN CODEX_API_KEY MINIMAX_VALIDATION_KEY; do
  eval 'value=${'"$name"'-}'
  test -z "$value" || { printf '%s\n' "$name" >> credential-failure; exit 23; }
done
printf 'remote-file-content\n' > retained.txt
exit 7
`,
		"workspace/long.sh": `#!/bin/sh
set -eu
printf '%s\n' "$$" > "$1.pid"
printf 'started\n' > "$1.started"
while :; do date +%s > "$1.heartbeat"; sleep 1; done
`,
	}
	for name, content := range files {
		mode := os.FileMode(0600)
		if strings.HasSuffix(name, ".sh") {
			mode = 0700
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	return workspace
}

func startDaemonRemoteExecutor(t *testing.T, ctx context.Context, root, local, remote, binary, image, registryURL, environment, token string) string {
	t.Helper()
	if launcher := os.Getenv("PARSAR_EXECUTOR_LAUNCHER"); launcher != "" {
		return startDaemonLauncherExecutor(t, ctx, root, local, remote, binary, image, registryURL, environment, token, launcher)
	}
	container := "parsar-daemon-environment-" + uuid.NewString()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanup, "docker", "rm", "-f", container).Run()
	})
	args := []string{"run", "--detach", "--name", container, "--network", "host", "--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()), "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--env", "CODEX_API_KEY", "--env", "HOME=/executor", "--env", "CODEX_HOME=/executor/codex", "--env", "RUST_LOG=off", "--env", "NO_PROXY=127.0.0.1,localhost", "--workdir", remote,
		"--mount", "type=bind,src=" + binary + ",dst=/usr/local/bin/codex,readonly", "--mount", "type=bind,src=" + filepath.Join(root, "executor") + ",dst=/executor", "--mount", "type=bind,src=" + local + ",dst=" + remote,
		"--entrypoint", "/usr/local/bin/codex", image, "exec-server", "--remote", registryURL, "--environment-id", environment}
	command := exec.CommandContext(ctx, "docker", args...)
	command.Env = append(os.Environ(), "CODEX_API_KEY="+token)
	if err := command.Run(); err != nil {
		t.Fatal("native executor container failed to start", err)
	}
	return container
}

func awaitDaemonRemoteCondition(t *testing.T, ctx context.Context, timeout time.Duration, label string, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(label, "context expired")
		case <-deadline.C:
			t.Fatal(label, "timed out")
		case <-tick.C:
		}
	}
}

func daemonRemotePrompt(t *testing.T, ctx context.Context, peer *gateway.Session, req proto.PromptRequestPayload, cancelWhen func() bool) (proto.DonePayload, []proto.Envelope, *proto.InteractionDecisionAckPayload) {
	return daemonRemotePromptWithStart(t, ctx, peer, req, cancelWhen, nil)
}

func daemonRemotePromptWithStart(t *testing.T, ctx context.Context, peer *gateway.Session, req proto.PromptRequestPayload, cancelWhen func() bool, start func(string) error) (proto.DonePayload, []proto.Envelope, *proto.InteractionDecisionAckPayload) {
	t.Helper()
	run := uuid.NewString()
	req.RunID = run
	sub, err := peer.SubscribeDurable(run)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Unsubscribe(run)
	if start != nil {
		if err = start(run); err != nil {
			t.Fatal(err)
		}
	} else {
		envelope, err := proto.NewEnvelope(proto.TypePromptRequest, run, req)
		if err != nil {
			t.Fatal(err)
		}
		if err = peer.Send(ctx, envelope); err != nil {
			t.Fatal(err)
		}
	}
	var events []proto.Envelope
	var done proto.DonePayload
	cancelID := ""
	type cancelResult struct {
		ack proto.InteractionDecisionAckPayload
		err error
	}
	cancelReply := make(chan cancelResult, 1)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("remote daemon prompt timed out")
		case <-tick.C:
			if cancelWhen != nil && cancelID == "" && cancelWhen() {
				cancelID = uuid.NewString()
				request, e := proto.NewEnvelope(proto.TypePromptCancel, run, proto.PromptCancelPayload{DeliveryID: cancelID})
				if e != nil {
					t.Fatal(e)
				}
				go func(deliveryID string) {
					ackCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
					defer cancel()
					ack, err := peer.SendAndWaitInteractionAck(ackCtx, request, deliveryID)
					cancelReply <- cancelResult{ack: ack, err: err}
				}(cancelID)
			}
		case result := <-cancelReply:
			if result.err != nil {
				t.Fatal("remote cancellation receipt failed", result.err)
			}
			if result.ack.Outcome != nil {
				done = *result.ack.Outcome
			}
			return done, events, &result.ack
		case event, ok := <-sub.Events:
			if !ok {
				t.Fatal("remote subscription closed", sub.Err())
			}
			events = append(events, event)
			if event.Type == proto.TypeDone {
				if err = event.DecodePayload(&done); err != nil {
					t.Fatal(err)
				}
				if cancelWhen == nil {
					return done, events, nil
				}
				if cancelID == "" {
					t.Fatal("remote Turn ended before cancellation")
				}
			}
		}
	}
}

func daemonRemoteHasError(events []proto.Envelope) bool {
	for _, event := range events {
		if event.Type == proto.TypeError {
			return true
		}
	}
	return false
}

func assertDaemonRemoteCommand(t *testing.T, events []proto.Envelope, phase, workspace string) {
	t.Helper()
	for _, event := range events {
		if event.Type != proto.TypeToolCall {
			continue
		}
		var tool proto.ToolCallPayload
		if event.DecodePayload(&tool) != nil {
			t.Fatal("invalid tool observation")
		}
		observation := tool.Observation
		if tool.Stage == "after" && observation != nil && observation.Kind == "command" && observation.ExitCode != nil && *observation.ExitCode == 7 && observation.Cwd != nil && *observation.Cwd == workspace && strings.Contains(string(observation.Output), "remote-stdout:"+phase) && strings.Contains(string(observation.Output), "remote-stderr:"+phase) {
			return
		}
	}
	t.Fatal("missing actual remote command stdout/stderr/exit/cwd observation")
}

func awaitDaemonRemoteExit(t *testing.T, ctx context.Context, container, workspace string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(workspace, "cancel.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		t.Fatal("invalid owned command PID")
	}
	awaitDaemonRemoteCondition(t, ctx, 60*time.Second, "owned remote process exit", func() bool {
		result, err := exec.CommandContext(ctx, "docker", "exec", container, "sh", "-c", `if kill -0 "$1" 2>/dev/null; then printf alive; else printf gone; fi`, "--", strconv.Itoa(pid)).Output()
		return err == nil && string(result) == "gone"
	})
	before, err := os.ReadFile(filepath.Join(workspace, "cancel.heartbeat"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("heartbeat check context expired")
	case <-time.After(1200 * time.Millisecond):
	}
	after, err := os.ReadFile(filepath.Join(workspace, "cancel.heartbeat"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("cancelled command heartbeat continued")
	}
	state, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Running}}", container).Output()
	if err != nil || strings.TrimSpace(string(state)) != "true" {
		t.Fatal("executor container was stopped instead of its command")
	}
}

func assertDaemonRemoteSecrets(t *testing.T, root, stateKey, provider, executor, harness, device string) {
	t.Helper()
	configRoot := filepath.Join(root, "parsar-daemon", "agent-sessions") + string(os.PathSeparator)
	profile := filepath.Join(root, "parsar-daemon", "execution", "auth.json")
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		// Native executable symlinks refer to paths inside the executor container.
		// Scan persisted regular files without following executable links.
		if !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(harness)) {
			t.Errorf("harness credential persisted in %s", path)
		}
		if bytes.Contains(data, []byte(executor)) {
			info, e := entry.Info()
			if os.Getenv("PARSAR_EXECUTOR_LAUNCHER") == "" || path != filepath.Join(root, "executor", "credential.json") || e != nil || info.Mode().Perm() != 0600 {
				t.Errorf("executor credential outside its private provisioned file: %s", path)
			}
		}
		if bytes.Contains(data, []byte(device)) && path != profile {
			t.Errorf("device credential outside its profile: %s", path)
		}
		if bytes.Contains(data, []byte(provider)) {
			info, e := entry.Info()
			if !strings.HasPrefix(path, configRoot) || filepath.Base(path) != "config.toml" || e != nil || info.Mode().Perm() != 0600 {
				t.Errorf("provider credential outside private native config: %s", path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(configRoot, stateKey, "config.toml")
	if _, err := os.Stat(config); err != nil {
		t.Fatal("expected private provider config missing")
	}
}

func persistDaemonRemoteProof(t *testing.T, root string, proof map[string]any, secrets []string) {
	t.Helper()
	data, err := json.MarshalIndent(proof, "", "  ")
	if err != nil {
		t.Error(err)
		return
	}
	for _, secret := range secrets {
		if secret != "" && bytes.Contains(data, []byte(secret)) {
			t.Error("credential appeared in observed response evidence")
			data = bytes.ReplaceAll(data, []byte(secret), []byte("[REDACTED]"))
		}
	}
	if err = os.WriteFile(filepath.Join(root, "remote-adapter-proof.json"), data, 0600); err != nil {
		t.Error(err)
	}
}
