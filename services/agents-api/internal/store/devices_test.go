package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtime"
)

func registerTestDevice(t *testing.T, s *Store, tenant string) (ExecutionDevice, string) {
	t.Helper()
	secret := uuid.NewString() + uuid.NewString()
	d, err := s.CreateDevice(context.Background(), tenant, "isolated executor", device.HashCredential(secret))
	if err != nil {
		t.Fatal(err)
	}
	return d, secret
}

func TestDeviceBindingIsTenantScopedStableAndDurable(t *testing.T) {
	s, pool := testStore(t)
	other, _ := testStore(t)
	ctx := context.Background()
	tenant, session := newTurnSession(t, s)
	otherTenant, otherSession := newTurnSession(t, s)
	a, _ := registerTestDevice(t, s, tenant)
	b, _ := registerTestDevice(t, s, tenant)
	foreign, _ := registerTestDevice(t, s, otherTenant)
	for _, args := range [][3]string{{tenant, session.ID, foreign.ID}, {otherTenant, session.ID, foreign.ID}, {tenant, otherSession.ID, a.ID}} {
		if err := s.BindSessionDevice(ctx, args[0], args[1], args[2]); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign binding: %v", err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, id := s, a.ID
			if i%2 == 0 {
				st, id = other, b.ID
			}
			errs <- st.BindSessionDevice(ctx, tenant, session.ID, id)
		}()
	}
	wg.Wait()
	close(errs)
	success, conflicts := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrDeviceBindingConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if success != 4 || conflicts != 4 {
		t.Fatalf("unstable binding: success=%d conflicts=%d", success, conflicts)
	}
	winner, err := s.GetSessionDevice(ctx, tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSessionDevice(ctx, otherTenant, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign lookup: %v", err)
	}
	pool.Close()
	restarted, _ := testStore(t)
	got, err := restarted.GetSessionDevice(ctx, tenant, session.ID)
	if err != nil || got != winner {
		t.Fatalf("binding after restart: %+v %v", got, err)
	}
	if err := restarted.RevokeDevice(ctx, otherTenant, winner.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign revocation: %v", err)
	}
	for range 2 {
		if err := restarted.RevokeDevice(ctx, tenant, winner.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := restarted.GetSessionDevice(ctx, tenant, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked device remains dispatchable: %v", err)
	}
}

func TestStandaloneGatewayUsesExecutionCredentials(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	tenant, _ := newTurnSession(t, s)
	a, secret := registerTestDevice(t, s, tenant)
	_, foreignSecret := registerTestDevice(t, s, uuid.NewString())
	server := httptest.NewUnstartedServer(nil)
	wsURL := "ws://" + server.Listener.Addr().String() + "/api/v1/agent-daemon/ws"
	handler, registry, err := runtime.NewGateway(s, wsURL)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	t.Cleanup(func() { server.Close(); runtime.CloseConnections(registry) })
	for _, token := range []string{"", "session-api-key", foreignSecret, secret} {
		body, _ := json.Marshal(map[string]string{"device_id": a.ID})
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/agent-daemon/bootstrap", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]any
		err = json.NewDecoder(response.Body).Decode(&data)
		response.Body.Close()
		if token == secret {
			if err != nil || response.StatusCode != http.StatusOK || data["workspace_id"] != "" || data["ws_url"] != wsURL {
				t.Fatalf("standalone bootstrap: %d %v %v", response.StatusCode, data, err)
			}
		} else if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unrelated credential accepted: %d", response.StatusCode)
		}
	}
	u, _ := url.Parse(wsURL)
	q := url.Values{"device_id": {a.ID}, "token": {secret}, "version": {proto.Version}}
	u.RawQuery = q.Encode()
	connect := func() *websocket.Conn {
		t.Helper()
		conn, response, err := websocket.DefaultDialer.Dial(u.String(), nil)
		if response != nil {
			response.Body.Close()
		}
		if err != nil {
			t.Fatal("device connection failed") // Do not log the credential-bearing URL.
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	first := connect()
	previous, err := registry.WaitForDevice(ctx, a.ID, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	second := connect()
	_ = first.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, _, err := first.ReadMessage(); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseAbnormalClosure) {
				t.Fatalf("replaced connection was not closed: %v", err)
			}
			break
		}
	}
	current, err := registry.LookupDevice(a.ID)
	if err != nil || current == previous || current.IsClosed() {
		t.Fatalf("replacement connection missing: %v", err)
	}
	if err := s.RevokeDevice(ctx, tenant, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := second.WriteJSON(map[string]any{"type": proto.TypeHeartbeat, "payload": map[string]any{"version": "test"}}); err != nil {
		t.Fatal(err)
	}
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		if _, _, err := second.ReadMessage(); err != nil {
			if !websocket.IsCloseError(err, gateway.CloseRuntimeDeleted) {
				t.Fatalf("revocation did not close connection: %v", err)
			}
			break
		}
	}
	if _, err := gateway.NewAuthenticator(s).AuthenticateBearer(ctx, a.ID, secret); !errors.Is(err, gateway.ErrAuthUnknownDevice) {
		t.Fatalf("revoked credential survived: %v", err)
	}
}
