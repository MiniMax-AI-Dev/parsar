package store_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type relayObservation struct {
	mu          sync.Mutex
	harness     net.Conn
	harnesses   int
	executors   int
	connects    int
	validations int
}

type relayResponse struct {
	http.ResponseWriter
	observation *relayObservation
	path        string
}

func (w *relayResponse) WriteHeader(status int) {
	if status == http.StatusOK {
		w.observation.mu.Lock()
		if strings.HasSuffix(w.path, "/connect") {
			w.observation.connects++
		}
		if strings.HasSuffix(w.path, "/validate") {
			w.observation.validations++
		}
		w.observation.mu.Unlock()
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *relayResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buffer, err := w.ResponseWriter.(http.Hijacker).Hijack()
	if err == nil {
		w.observation.mu.Lock()
		if strings.Contains(w.path, "/harness/") {
			w.observation.harness = conn
			w.observation.harnesses++
		} else {
			w.observation.executors++
		}
		w.observation.mu.Unlock()
	}
	return conn, buffer, err
}

type relayProcess struct {
	command *exec.Cmd
	done    chan struct{}
	err     error
}

func startRelayProcess(t *testing.T, ctx context.Context, directory string, env []string, binary string, args ...string) *relayProcess {
	t.Helper()
	command := exec.CommandContext(ctx, binary, args...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
	command.WaitDelay = 5 * time.Second
	command.Dir, command.Env = directory, env
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	process := &relayProcess{command: command, done: make(chan struct{})}
	go func() { process.err = command.Wait(); close(process.done) }()
	t.Cleanup(func() {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		select {
		case <-process.done:
		case <-time.After(5 * time.Second):
			t.Error("owned native process did not exit")
		}
	})
	return process
}
func (p *relayProcess) wait(t *testing.T) {
	t.Helper()
	select {
	case <-p.done:
		if p.err != nil {
			t.Fatal("native relay probe failed", p.err)
		}
	case <-time.After(100 * time.Second):
		t.Fatal("native relay probe timed out")
	}
}

func TestNativeHarnessRelayPostgreSQLAndProcessRecovery(t *testing.T) {
	probe, binary, proof := os.Getenv("PARSAR_NATIVE_RELAY_PROBE"), os.Getenv("PARSAR_CODEX_BINARY"), os.Getenv("PARSAR_EXECUTOR_PROOF_DIR")
	if probe == "" || binary == "" || proof == "" {
		t.Skip("pinned native relay probe, Codex binary and private evidence directory required")
	}
	s, _ := store.NewTestStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
	defer cancel()
	lease, err := s.AcquireExecutionLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close(context.Background())
	tenant := uuid.NewString()
	principal := store.FixtureExecutorPrincipal(t, s, tenant)
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "native-relay", Configuration: json.RawMessage(`{"environment":{"type":"self_hosted","workspace_directory":"/workspace","capability_directories":[]}}`)})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(ctx, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := s.IssueExecutorCredential(t.Context(), principal, environment.ID, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	executorToken := credential.Token
	server := httptest.NewUnstartedServer(nil)
	registry, err := codex.New(codex.Config{Store: s, CheckOwnership: lease.Ping, PublicURL: "http://" + server.Listener.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	harnessToken, releaseHarness, err := registry.IssueHarnessCredential(ctx, tenant, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseHarness()

	observation := &relayObservation{}
	handler := registry.Handler()
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		handler.ServeHTTP(&relayResponse{ResponseWriter: w, observation: observation, path: req.URL.Path}, req)
	})
	server.Start()
	defer func() { registry.Close(); server.Close() }()
	version, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil || strings.TrimSpace(string(version)) != "codex-cli 0.153.4" {
		t.Fatal("pinned Codex0.153.4 required")
	}
	runtime, err := os.MkdirTemp(proof, "native-relay-")
	if err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"codex", "workspace"} {
		if err := os.Mkdir(filepath.Join(runtime, sub), 0700); err != nil {
			t.Fatal(err)
		}
	}
	executorEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + runtime, "CODEX_HOME=" + filepath.Join(runtime, "codex"), "CODEX_API_KEY=" + executorToken, "NO_PROXY=127.0.0.1,localhost", "RUST_LOG=off"}
	startRelayProcess(t, ctx, runtime, executorEnv, binary, "exec-server", "--remote", server.URL, "--environment-id", environment.ID)
	until := time.Now().Add(20 * time.Second)
	for {
		connected, err := registry.Connected(ctx, tenant, environment.ID)
		if err == nil && connected {
			break
		}
		if time.Now().After(until) {
			t.Fatal("native executor not connected")
		}
		time.Sleep(20 * time.Millisecond)
	}
	harnessEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + runtime, "PARSAR_NATIVE_ENV_PROOF=" + runtime, "CODEX_EXEC_SERVER_NOISE_REGISTRY_URL=" + server.URL, "CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID=" + environment.ID, "CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN=" + harnessToken, "NO_PROXY=127.0.0.1,localhost", "RUST_LOG=off"}
	first := startRelayProcess(t, ctx, runtime, harnessEnv, probe, "first")
	until = time.Now().Add(50 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(runtime, "ready-to-disconnect")); err == nil {
			break
		}
		select {
		case <-first.done:
			t.Fatal("native probe ended before recovery checkpoint", first.err)
		default:
		}
		if time.Now().After(until) {
			t.Fatal("native probe did not reach recovery checkpoint")
		}
		time.Sleep(20 * time.Millisecond)
	}
	observation.mu.Lock()
	if observation.harnesses != 1 || observation.executors != 1 || observation.connects != 2 || observation.validations != 1 || observation.harness == nil {
		observation.mu.Unlock()
		t.Fatal("principal concurrency or same-key refresh replaced the active native pair")
	}
	connection := observation.harness
	observation.mu.Unlock()
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	first.wait(t)
	observation.mu.Lock()
	if observation.harnesses != 2 || observation.executors != 2 || observation.validations != 2 {
		observation.mu.Unlock()
		t.Fatal("both native peers did not reconnect exactly once during controlled recovery")
	}
	observation.mu.Unlock()
	// Wait for the executor's reconnect after the first harness process releases its pair.
	until = time.Now().Add(20 * time.Second)
	for {
		observation.mu.Lock()
		executors := observation.executors
		observation.mu.Unlock()
		if executors >= 3 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("executor not ready for fresh harness")
		}
		time.Sleep(20 * time.Millisecond)
	}
	fresh := startRelayProcess(t, ctx, runtime, harnessEnv, probe, "fresh")
	fresh.wait(t)
	firstProof, err := os.ReadFile(filepath.Join(runtime, "first.json"))
	if err != nil {
		t.Fatal(err)
	}
	freshProof, err := os.ReadFile(filepath.Join(runtime, "fresh.json"))
	if err != nil {
		t.Fatal(err)
	}
	report := map[string]any{"native_executor": "codex 0.153.4", "native_source": "3d2ee51ca2d5db578f328aa75e20aa22c0197c9a", "real_postgresql": true, "first": json.RawMessage(firstProof), "fresh": json.RawMessage(freshProof), "model_calls": 0, "limitations": []string{"Internal registry/relay workflow, not public Environment admission or model execution", "One independent harness connection per Environment", "One acknowledged-start process survived one bounded transport outage; arbitrary replay and durable crash restoration are not established"}}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtime, "proof.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("native commands, shared concurrency, 128KiB file, refresh, paired reconnect, one retained process and fresh file retention verified")
}
