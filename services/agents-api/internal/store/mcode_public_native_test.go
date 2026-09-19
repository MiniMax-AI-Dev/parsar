package store_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

// This opt-in fixture never supplies model responses. The provider options must
// name a real API; private operator files are deliberately outside the repository.
func TestNativeMCodePublicExecution(t *testing.T) {
	python, binary, root, optionsFile := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON"), os.Getenv("PARSAR_NATIVE_DAEMON_BIN"), os.Getenv("PARSAR_NATIVE_PROOF_DIR"), os.Getenv("PARSAR_MCODE_REAL_OPTIONS")
	if python == "" || binary == "" || root == "" || optionsFile == "" {
		t.Skip("native daemon, fixed SDK, private real-model options and proof directory required")
	}
	raw, err := os.ReadFile(optionsFile)
	if err != nil {
		t.Fatal(err)
	}
	var options map[string]any
	if json.Unmarshal(raw, &options) != nil {
		t.Fatal("invalid private options")
	}
	model, _ := options["model"].(string)
	if model == "" {
		t.Fatal("real model required")
	}
	h := newDispatchHarness(t)
	h.d.Options = func(context.Context, store.Session) (map[string]any, error) { return options, nil }
	home, err := os.MkdirTemp(root, "mcode-public-")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	worker, err := execution.StartWorker(ctx, h.d)
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(20 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	token, foreign := uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: "test", ProjectID: h.tenant, SubjectKind: "service_account", SubjectID: "owner", TokenSHA256: device.HashCredential(token), TenantID: h.tenant},
		{OrganizationID: "test", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "other", TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(h.s, auth, "mcode", api.WithExecution(worker))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	stop := startMCodeDaemon(t, h, home, binary)
	defer func() { stop() }()
	evidence := filepath.Join(home, "public.json")
	run := func(stage string) {
		command := exec.CommandContext(ctx, python, "../../tests/official_mcode_native.py", server.URL, token, foreign, model, stage, evidence)
		if log, err := command.CombinedOutput(); err != nil {
			data, _ := os.ReadFile(evidence)
			var identity struct {
				Session string `json:"session"`
			}
			_ = json.Unmarshal(data, &identity)
			if page, e := h.s.ListTurns(ctx, h.tenant, identity.Session, "", 100, true); e == nil {
				diagnostic, _ := json.Marshal(page)
				text := string(diagnostic)
				if provider, ok := options["mcode_provider"].(map[string]any); ok {
					if opts, ok := provider["options"].(map[string]any); ok {
						if key, ok := opts["apiKey"].(string); ok && key != "" {
							text = strings.ReplaceAll(text, key, "[REDACTED]")
						}
					}
				}
				_ = os.WriteFile(filepath.Join(home, "failed-turns.json"), []byte(text), 0600)
			}
			t.Fatalf("public mcode %s failed: %v %s; evidence %s", stage, err, log, home)
		}
	}
	run("initial")
	data, err := os.ReadFile(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var proof struct {
		Session   string `json:"session"`
		FirstTurn string `json:"first_turn"`
	}
	if json.Unmarshal(data, &proof) != nil {
		t.Fatal("invalid evidence")
	}
	turn, err := h.s.GetTurn(ctx, h.tenant, proof.Session, proof.FirstTurn)
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := h.s.ListTurnInputs(ctx, h.tenant, proof.Session, proof.FirstTurn, 0, 100)
	if err != nil || len(inputs) != 2 {
		t.Fatal("steering input not in same turn", err)
	}
	var outcome execution.Result
	if json.Unmarshal(turn.Outcome, &outcome) != nil || outcome.AppliedThrough != inputs[1].Sequence {
		t.Fatal("native applied receipt missing")
	}
	before, err := h.s.GetSessionExecutionBinding(ctx, h.tenant, proof.Session)
	if err != nil || before.NativeSessionID == "" {
		t.Fatal("native binding missing", err)
	}
	stop()
	stop = startMCodeDaemon(t, h, home, binary)
	run("resume")
	after, err := h.s.GetSessionExecutionBinding(ctx, h.tenant, proof.Session)
	if err != nil || before.NativeSessionID != after.NativeSessionID {
		t.Fatal("native history changed", err)
	}
	if err := os.WriteFile(filepath.Join(home, "native-session-id"), []byte(after.NativeSessionID), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("Real mcode common-contract acceptance passed: %s", home)
}

func startMCodeDaemon(t *testing.T, h *dispatchHarness, home, binary string) func() {
	t.Helper()
	if h.conn != nil {
		_ = h.conn.Close()
	}
	profile := filepath.Join(home, "parsar-daemon", "execution")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	auth, _ := json.Marshal(map[string]string{"server_url": h.url + "/api/v1", "runtime_id": h.device.ID, "runner_credential": h.credential, "device_name": "mcode native proof"})
	if err := os.WriteFile(filepath.Join(profile, "auth.json"), auth, 0600); err != nil {
		t.Fatal(err)
	}
	log, err := os.OpenFile(filepath.Join(home, "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := h.registry.LookupDevice(h.device.ID)
	cmd := exec.Command(binary, "connect", "--profile", "execution")
	cmd.Env = append(os.Environ(), "PARSAR_HOME="+home, "PARSAR_MCODE_AGENTS_API=1")
	cmd.Stdout, cmd.Stderr = log, log
	if err = cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	stop := func() {
		select {
		case <-done:
			return
		default:
		}
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		_ = log.Close()
	}
	t.Cleanup(stop)
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if peer, err := h.registry.LookupDevice(h.device.ID); err == nil && peer != old {
			if info, found, known := peer.AgentKindStatus("mcode"); found && known && info.Available && info.Capabilities.EnvironmentNone {
				return stop
			}
		}
		select {
		case <-done:
			t.Fatalf("daemon exited; evidence %s", home)
		default:
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("native mcode daemon not ready; evidence %s", home)
	return stop
}
