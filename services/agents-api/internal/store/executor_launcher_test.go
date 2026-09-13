package store_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestNativeExecutorLauncherTLSAndHelpers(t *testing.T) {
	launcher, binary, probe := os.Getenv("PARSAR_EXECUTOR_LAUNCHER"), os.Getenv("PARSAR_CODEX_BINARY"), os.Getenv("PARSAR_NATIVE_RELAY_PROBE")
	proof, image := os.Getenv("PARSAR_EXECUTOR_PROOF_DIR"), os.Getenv("PARSAR_PLACEMENT_EXECUTOR_IMAGE")
	if launcher == "" || binary == "" || probe == "" || proof == "" || image == "" {
		t.Skip("built launcher, native Codex installation/probe, private proof directory and pinned executor image required")
	}
	if !strings.HasPrefix(image, "sha256:") {
		t.Fatal("pin the preloaded executor image")
	}
	s, _ := store.NewTestStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	lease, err := s.AcquireExecutionLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close(context.Background())
	tenant := uuid.NewString()
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "launcher", Configuration: json.RawMessage(`{"environment":{"type":"self_hosted","workspace_directory":"/workspace","capability_directories":[]}}`)})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(ctx, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.IssueEnvironmentExecutorCredential(ctx, tenant, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	harnessToken := uuid.NewString()
	root, err := os.MkdirTemp(proof, "launcher-tls-")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"executor", "harness", "workspace"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeLauncherCredential(t, filepath.Join(root, "executor", "credential.json"), environment.ID, token)
	server := httptest.NewUnstartedServer(nil)
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	remote := "https://" + launcherTestHost + ":" + port
	registry, err := codex.New(codex.Config{Store: s, CheckOwnership: lease.Ping, PublicURL: remote,
		HarnessKeys: []codex.ScopedKey{{TokenSHA256: device.HashCredential(harnessToken), TenantID: tenant, EnvironmentID: environment.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	var connections, requests atomic.Int64
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	observation := &relayObservation{}
	handler := registry.Handler()
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		handler.ServeHTTP(&relayResponse{ResponseWriter: w, observation: observation, path: r.URL.Path}, r)
	})
	server.TLS = &tls.Config{Certificates: []tls.Certificate{launcherTestCertificate(t, root)}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer func() { registry.Close(); server.Close() }()

	for _, test := range []struct {
		name, remote string
		trusted      bool
	}{
		{"untrusted-ca", remote, false},
		{"wrong-hostname", strings.Replace(remote, launcherTestHost, "wrong."+launcherTestHost, 1), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := connections.Load()
			container := startLauncherContainer(t, ctx, root, image, binary, launcher, test.remote, environment.ID, test.trusted)
			awaitDaemonRemoteCondition(t, ctx, 20*time.Second, "TLS connection attempt", func() bool { return connections.Load() > before })
			stopLauncherContainer(t, ctx, container, false)
			if requests.Load() != 0 {
				t.Fatal("unverified TLS reached registry HTTP handling")
			}
		})
	}
	container := startLauncherContainer(t, ctx, root, image, binary, launcher, remote, environment.ID, true)
	awaitDaemonRemoteCondition(t, ctx, 20*time.Second, "verified executor registration", func() bool {
		connected, err := registry.Connected(ctx, tenant, environment.ID)
		return err == nil && connected
	})
	first := startLauncherProbe(t, ctx, root, image, binary, probe, remote, environment.ID, harnessToken, "first")
	awaitDaemonRemoteCondition(t, ctx, 60*time.Second, "native relay recovery checkpoint", func() bool {
		_, err := os.Stat(filepath.Join(root, "ready-to-disconnect"))
		return err == nil
	})
	observation.mu.Lock()
	connection := observation.harness
	observation.mu.Unlock()
	if connection == nil {
		t.Fatal("native harness socket missing")
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	first.wait(t)
	startLauncherProbe(t, ctx, root, image, binary, probe, remote, environment.ID, harnessToken, "fresh").wait(t)
	startLauncherProbe(t, ctx, root, image, binary, probe, remote, environment.ID, harnessToken, "helpers").wait(t)
	stopLauncherContainer(t, ctx, container, true)
	logs, err := exec.CommandContext(ctx, "docker", "logs", container).CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logs), token) || strings.Contains(string(logs), harnessToken) {
		t.Fatal("credential appeared in launcher logs")
	}
	if err := os.WriteFile(filepath.Join(root, "launcher.log"), logs, 0600); err != nil {
		t.Fatal(err)
	}
	report := map[string]any{"tls_hostname_and_ca": true, "untrusted_ca_rejected": true, "wrong_hostname_rejected": true,
		"native_recovery": true, "native_helpers": true, "graceful_launcher_exit": true, "model_calls": 0,
		"limits": "Controlled test DNS and CA; not public DNS/certificate deployment, full Environment API, model execution or OS quiescence."}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "acceptance.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("native launcher TLS/helper evidence", root)
}
