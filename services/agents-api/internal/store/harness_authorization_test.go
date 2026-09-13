package store_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/executor/codex"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestHarnessGrantsCheckPostgreSQLTenantOwnership(t *testing.T) {
	s, _ := store.NewTestStore(t)
	lease, err := s.AcquireExecutionLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close(context.Background())
	tenants := []string{uuid.NewString(), uuid.NewString()}
	sessions, environments := []string{}, []string{}
	for _, tenant := range tenants {
		session, err := s.CreateSession(t.Context(), tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "harness-scope", Configuration: json.RawMessage(`{"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)})
		if err != nil {
			t.Fatal(err)
		}
		environment, err := s.GetSessionEnvironment(t.Context(), tenant, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session.ID)
		environments = append(environments, environment.ID)
	}
	tokens := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	executorToken := uuid.NewString()
	key := func(token, tenant, environment string) codex.ScopedKey {
		return codex.ScopedKey{TokenSHA256: device.HashCredential(token), TenantID: tenant, EnvironmentID: environment}
	}
	server := httptest.NewUnstartedServer(nil)
	registry, err := codex.New(codex.Config{Store: s, CheckOwnership: lease.Ping, PublicURL: "http://" + server.Listener.Addr().String(), Keys: []codex.ScopedKey{key(executorToken, tenants[0], environments[0])}, HarnessKeys: []codex.ScopedKey{key(tokens[0], tenants[0], environments[0]), key(tokens[1], tenants[1], environments[1]), key(tokens[2], tenants[1], environments[0])}})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = registry.Handler()
	server.Start()
	defer func() { registry.Close(); server.Close() }()
	post := func(environment, route, token string, body any, status int, result any) {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/cloud/environment/"+environment+"/"+route, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != status {
			t.Fatalf("%s status %d, expected %d", route, resp.StatusCode, status)
		}
		if result != nil {
			if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
				t.Fatal(err)
			}
		}
	}
	publicKey := codex.PublicKey{Suite: "Noise_hybridIK_X25519+MLKEM768_AESGCM_SHA256", X25519: base64.StdEncoding.EncodeToString(make([]byte, 32)), MLKEM768: base64.StdEncoding.EncodeToString(make([]byte, 1184))}
	var registration codex.RegistrationResponse
	post(environments[0], "register", executorToken, codex.RegistrationRequest{SecurityProfile: "noise_hybrid_ik_v1", ExecutorPublicKey: publicKey}, 200, &registration)
	executor, resp, err := websocket.DefaultDialer.DialContext(t.Context(), registration.URL, nil)
	if resp != nil {
		resp.Body.Close()
	}
	if err != nil {
		t.Fatal("executor connection failed")
	}
	defer executor.Close()
	body := codex.ConnectRequest{HarnessPublicKey: publicKey}
	post(environments[1], "connect", tokens[0], body, 401, nil)
	post(environments[0], "connect", tokens[1], body, 401, nil)
	post(environments[0], "connect", tokens[2], body, 404, nil)
	var grant codex.ConnectResponse
	post(environments[0], "connect", tokens[0], body, 200, &grant)
	if err := s.DeleteSession(t.Context(), tenants[0], sessions[0]); err != nil {
		t.Fatal(err)
	}
	post(environments[0], "connect", tokens[0], body, 404, nil)
	harness, resp, err := websocket.DefaultDialer.DialContext(t.Context(), grant.URL, nil)
	if harness != nil {
		harness.Close()
	}
	if resp != nil {
		defer resp.Body.Close()
	}
	if err == nil || resp == nil || resp.StatusCode != 404 {
		t.Fatal("deleted owning Session accepted harness")
	}
	if _, err := s.GetEnvironment(t.Context(), tenants[1], environments[1]); err != nil {
		t.Fatal("foreign Environment affected", err)
	}
}
