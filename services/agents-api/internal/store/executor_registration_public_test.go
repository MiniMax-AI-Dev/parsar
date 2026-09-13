package store_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestExecutorRegistrationPostgreSQLAndNativeReconnect(t *testing.T) {
	s, _ := store.NewTestStore(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	lease, err := s.AcquireExecutionLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close(context.Background())
	tenant, foreign, token, wrongTenantToken := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	config := json.RawMessage(`{"environment":{"type":"self_hosted","workspace_directory":"/workspace","capability_directories":[]}}`)
	session, err := s.CreateSession(ctx, tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "native-registry", Configuration: config})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(ctx, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateSession(ctx, foreign, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "native-registry", Configuration: config})
	if err != nil {
		t.Fatal(err)
	}
	otherEnvironment, err := s.GetSessionEnvironment(ctx, foreign, other.ID)
	if err != nil {
		t.Fatal(err)
	}
	keys := []codex.ScopedKey{
		{TokenSHA256: device.HashCredential(token), TenantID: tenant, EnvironmentID: environment.ID},
		{TokenSHA256: device.HashCredential(wrongTenantToken), TenantID: foreign, EnvironmentID: environment.ID},
	}
	server := httptest.NewUnstartedServer(nil)
	address := server.Listener.Addr().String()
	var attempts atomic.Int64
	start := func(server *httptest.Server) *codex.Registry {
		r, err := codex.New(codex.Config{Store: s, CheckOwnership: lease.Ping, PublicURL: "http://" + address, Keys: keys})
		if err != nil {
			t.Fatal(err)
		}
		handler := r.Handler()
		server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if strings.HasSuffix(req.URL.Path, "/register") {
				attempts.Add(1)
			}
			handler.ServeHTTP(w, req)
		})
		server.Start()
		return r
	}
	registry := start(server)
	defer func() { registry.Close(); server.Close() }()
	register := func(id, key string, status int) codex.RegistrationResponse {
		t.Helper()
		body := codex.RegistrationRequest{SecurityProfile: "noise_hybrid_ik_v1", ExecutorPublicKey: codex.PublicKey{Suite: "Noise_hybridIK_X25519+MLKEM768_AESGCM_SHA256", X25519: base64.StdEncoding.EncodeToString(make([]byte, 32)), MLKEM768: base64.StdEncoding.EncodeToString(make([]byte, 1184))}}
		encoded, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/cloud/environment/"+id+"/register", bytes.NewReader(encoded))
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != status {
			t.Fatalf("register status %d expected %d", resp.StatusCode, status)
		}
		var result codex.RegistrationResponse
		if status == 200 {
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	register(otherEnvironment.ID, token, 401)
	register(environment.ID, wrongTenantToken, 404)
	valid := register(environment.ID, token, 200)
	socket, response, err := websocket.DefaultDialer.DialContext(ctx, valid.URL, nil)
	if err != nil {
		if response != nil {
			response.Body.Close()
		}
		t.Fatal("scoped socket failed")
	}
	socket.Close()
	connected := func(want bool) {
		t.Helper()
		until := time.Now().Add(20 * time.Second)
		for time.Now().Before(until) {
			got, err := registry.Connected(ctx, tenant, environment.ID)
			if err == nil && got == want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("native presence did not converge")
	}
	connected(false)
	binary := os.Getenv("PARSAR_CODEX_BINARY")
	if binary != "" {
		version, err := exec.CommandContext(ctx, binary, "--version").Output()
		if err != nil || strings.TrimSpace(string(version)) != "codex-cli 0.153.4" {
			t.Fatal("pinned Codex0.153.4 required")
		}
		proof := os.Getenv("PARSAR_EXECUTOR_PROOF_DIR")
		if proof == "" {
			t.Fatal("private runtime directory required")
		}
		runtime, err := os.MkdirTemp(proof, "native-presence-")
		if err != nil {
			t.Fatal(err)
		}
		home := filepath.Join(runtime, "codex")
		if err := os.Mkdir(home, 0700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, binary, "exec-server", "--remote", server.URL, "--environment-id", environment.ID)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = 5 * time.Second
		cmd.Dir = runtime
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + runtime, "CODEX_HOME=" + home, "CODEX_API_KEY=" + token, "NO_PROXY=127.0.0.1,localhost", "RUST_LOG=off"}
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		defer func() {
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("native executor failed to exit")
			}
		}()
		connected(true)
		before := attempts.Load()
		registry.Close()
		server.Close()
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		server = httptest.NewUnstartedServer(nil)
		server.Listener.Close()
		server.Listener = listener
		registry = start(server)
		connected(true)
		if attempts.Load() <= before {
			t.Fatal("native executor did not re-register after registry restart")
		}
		t.Log("unmodified Codex0.153.4 registered, connected and re-registered after loss of process-local tickets")
	} else {
		t.Log("native CLI verification not requested; real PostgreSQL/socket checks remain active")
	}
	if err := s.DeleteSession(ctx, tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	register(environment.ID, token, 404)
	_, response, err = websocket.DefaultDialer.DialContext(ctx, valid.URL, nil)
	if err == nil {
		t.Fatal("deleted Environment accepted old connection")
	}
	if response != nil {
		response.Body.Close()
	}
	if _, err := s.GetEnvironment(ctx, foreign, otherEnvironment.ID); err != nil {
		t.Fatal("another tenant was affected", err)
	}
}
