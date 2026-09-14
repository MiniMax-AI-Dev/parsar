package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestNativePreparedWorkerRemoteEnvironment(t *testing.T) {
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
	workspace := "/parsar-prepared-dispatch-" + uuid.NewString()
	configuration, err := json.Marshal(map[string]any{
		"agent":       map[string]any{"model": "MiniMax-M3", "instructions": "Use the native shell for requested commands. Command verification requires the exact supplied command argument. Never append echo, separators, wrappers or error recovery. Exit 7 is intentional and must remain the tool's exit status; do not turn it into exit 0."},
		"environment": map[string]any{"type": "self_hosted", "workspace_directory": workspace, "capability_directories": []string{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.session, err = h.s.CreateSession(ctx, h.tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "remote-dispatch", Configuration: configuration})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.GetSessionDevice(ctx, h.tenant, h.session.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("native fixture must begin without a device binding", err)
	}
	environment, err := h.s.GetSessionEnvironment(ctx, h.tenant, h.session.ID)
	if err != nil {
		t.Fatal(err)
	}
	executorToken, err := h.s.IssueEnvironmentExecutorCredential(ctx, h.tenant, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	var registry *codex.Registry
	var tokenMu sync.Mutex
	secrets := []string{key, executorToken, h.credential}
	harnessTokens := []string{}
	h.d.EnvironmentConnection = func(owner context.Context, session store.Session, selected store.Environment) (execution.EnvironmentConnection, error) {
		token, release, err := registry.IssueHarnessCredential(owner, session.TenantID, selected.ID)
		if err != nil {
			return execution.EnvironmentConnection{}, err
		}
		tokenMu.Lock()
		defer tokenMu.Unlock()
		harnessTokens = append(harnessTokens, token)
		secrets = append(secrets, token)
		return execution.EnvironmentConnection{URL: server.URL, Token: token, Release: release}, nil
	}
	h.d.Options = func(context.Context, store.Session) (map[string]any, error) {
		return map[string]any{"codex_provider": map[string]any{"name": "MiniMax validation", "base_url": "https://api.minimax.cn/v1", "bearer_token": key, "wire_api": "responses"}}, nil
	}
	worker, err := execution.StartWorker(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, cancelWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	defer func() {
		cancelWorker()
		select {
		case err := <-workerDone:
			if err != nil && err != context.Canceled {
				t.Error("worker stopped unexpectedly", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("worker did not release execution ownership")
		}
		if registry != nil {
			registry.Close()
		}
		server.Close()
	}()
	registry, err = codex.New(codex.Config{Store: h.s, CheckOwnership: worker.CheckOwnership, PublicURL: "http://" + server.Listener.Addr().String()})
	if err != nil {
		cancelWorker()
		workerDone <- worker.Run(workerCtx)
		t.Fatal(err)
	}
	server.Config.Handler = registry.Handler()
	server.Start()
	go func() { workerDone <- worker.Run(workerCtx) }()
	instruction := "REMOTE_" + uuid.NewString()
	local := prepareDaemonRemoteWorkspace(t, root, instruction)
	startDaemonRemoteExecutor(t, ctx, root, local, workspace, binary, image, server.URL, environment.ID, executorToken)
	proof := map[string]any{"scope": "private Worker device selection and scheduling; public Environment lifecycle remains pending", "environment_id": environment.ID, "session_id": h.session.ID, "native_version": strings.TrimSpace(string(version))}
	defer func() { tokenMu.Lock(); defer tokenMu.Unlock(); persistDaemonRemoteProof(t, root, proof, secrets) }()
	const memory = "walnut heron violet cedar cobalt willow moss iris"
	nativeID := ""
	for index, phase := range []string{"first", "resumed"} {
		awaitDaemonRemoteCondition(t, ctx, 30*time.Second, "executor registration after previous release", func() bool {
			connected, err := registry.Connected(ctx, h.tenant, environment.ID)
			return err == nil && connected
		})
		text := "Run the exact command `./placement.sh " + phase + "` once with the native shell. The tool command argument must be exactly the text inside the backticks: no wrapper, no appended echo, no separators, no error recovery. Exit 7 is intentional; preserve that native exit status and do not retry. Report stdout, stderr and the verification memory briefly."
		if index == 0 {
			text += " The fictional festival name to remember is " + memory + "."
		} else {
			text += " Recall the fictional festival name from the first Turn and read retained.txt."
		}
		payload, _ := json.Marshal(map[string]string{"text": text})
		pending, err := h.s.ReserveEnvironmentInput(ctx, h.tenant, h.session.ID, phase, []store.Input{{Kind: "message", Payload: payload}})
		if err != nil {
			t.Fatal(err)
		}
		run := awaitWorkerEnvironmentRun(t, ctx, h.s, h.tenant, pending)
		proof[phase] = run
		if run.Turn.Status != store.TurnCompleted || len(run.Reservation.Receipts) != 1 {
			t.Fatal("native prepared dispatch did not complete; inspect private proof", err)
		}
		var result execution.Result
		if json.Unmarshal(run.Turn.Outcome, &result) != nil || !strings.Contains(result.Done.Content, memory) || !strings.Contains(result.Done.Content, instruction) || strings.Contains(result.Done.Content, "WRONG_LOCAL_INSTRUCTIONS") {
			t.Fatal("remote instructions or native memory missing; inspect private proof")
		}
		bound, err := h.s.GetSessionDevice(ctx, h.tenant, h.session.ID)
		if err != nil || bound.ID != h.device.ID || bound.NativeSessionID == "" || (index == 1 && bound.NativeSessionID != nativeID) {
			t.Fatal("native continuation identity changed", err)
		}
		nativeID = bound.NativeSessionID
		var events []proto.Envelope
		var after int32
		for {
			batch, err := h.s.ListTurnEvents(ctx, h.tenant, h.session.ID, run.Turn.ID, after, 100)
			if err != nil {
				t.Fatal(err)
			}
			if len(batch) == 0 {
				break
			}
			for _, event := range batch {
				events = append(events, proto.Envelope{Type: event.Kind, ID: run.Turn.ID, Payload: event.Payload})
				after = event.Ordinal
			}
		}
		proof[phase+"_events"] = events
		assertDaemonRemoteCommand(t, events, phase, workspace)
		retry, err := h.s.ReserveEnvironmentInput(ctx, h.tenant, h.session.ID, phase, []store.Input{{Kind: "message", Payload: payload}})
		if err != nil || len(retry.Receipts) != 1 || !retry.Receipts[0].Replayed || retry.Receipts[0].TurnID != run.Turn.ID {
			t.Fatal("reservation retry allocated or executed another native preparation")
		}
	}
	count, err := os.ReadFile(filepath.Join(local, "execution-count"))
	if err != nil || string(count) != "first\nresumed\n" {
		t.Fatal("native command omitted or repeated")
	}
	if data, err := os.ReadFile(filepath.Join(local, "retained.txt")); err != nil || string(data) != "remote-file-content\n" {
		t.Fatal("remote file did not persist")
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatal("executor path appeared on the harness host")
	}
	tokenMu.Lock()
	tokens := append([]string(nil), harnessTokens...)
	tokenMu.Unlock()
	if len(tokens) != 2 {
		t.Fatal("worker prepared a reservation more than once")
	}
	for _, token := range tokens {
		assertDaemonRemoteSecrets(t, root, "agents-api-"+h.session.ID, key, executorToken, token, h.credential)
	}
	proof["native_thread_id"] = nativeID
	proof["status"] = "private_worker_verified_public_integration_pending"
	proof["completed_turns"] = 2
	proof["reservation_retries_did_not_prepare_or_start"] = true
	t.Log("real-provider bound Worker evidence", root)
}
