package store_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/runtime"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestManagedRuntimeConnectionTracksAuthenticatedSocket(t *testing.T) {
	s, _ := store.NewTestStore(t)
	tenant, session, environment := managedSession(t, s)
	server := httptest.NewUnstartedServer(nil)
	wsURL := "ws://" + server.Listener.Addr().String() + "/api/v1/agent-daemon/ws"
	handler, registry, err := runtime.NewGateway(s, wsURL)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	t.Cleanup(func() { server.Close(); runtime.CloseConnections(registry) })
	p := &lifecycleProvider{resources: map[string]sandbox.Info{}}
	key := uuid.NewString()
	start := func() *execution.Worker {
		w, err := execution.StartWorker(t.Context(), &execution.Dispatcher{Store: s, Registry: registry, ManagedRuntimes: &execution.RuntimeProviders{CoreURL: server.URL + "/api/v1", Providers: map[string]sandbox.Provider{key: p}}})
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	stop := func(w *execution.Worker) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = w.Run(ctx)
	}
	w := start()
	t.Cleanup(func() { stop(w) })
	owner, err := w.ProvisionEnvironment(t.Context(), tenant, environment.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	assertStatus := func(want string) {
		t.Helper()
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
			if err := w.ReconcileManagedRuntimes(t.Context()); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetEnvironment(t.Context(), tenant, environment.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("Environment did not reach", want)
	}
	dial := func(token string) (*websocket.Conn, error) {
		u, _ := url.Parse(wsURL)
		u.RawQuery = url.Values{"device_id": {owner.DeviceID}, "token": {token}, "version": {proto.Version}}.Encode()
		conn, response, err := websocket.DefaultDialer.Dial(u.String(), nil)
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return conn, err
	}
	assertStatus("pending") // Compute existence alone is insufficient.
	if conn, err := dial(uuid.NewString()); err == nil {
		conn.Close()
		t.Fatal("unrelated credential connected")
	}
	assertStatus("pending")
	conn, err := dial(p.credential)
	if err != nil {
		t.Fatal("authorized connection failed")
	}
	defer conn.Close()
	assertStatus("connected")
	got, err := s.GetSession(t.Context(), tenant, session.ID)
	if err != nil || got.LastTurn != nil || got.EnvironmentInputActivity != nil {
		t.Fatal("connection fabricated native execution", err)
	}
	if _, err := s.GetEnvironment(t.Context(), uuid.NewString(), environment.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign Environment access", err)
	}
	p.unavailable = true
	conn.Close()
	assertStatus("disconnected") // Provider outage cannot conceal socket loss.
	p.unavailable = false
	conn, err = dial(p.credential)
	if err != nil {
		t.Fatal("authorized reconnection failed")
	}
	defer conn.Close()
	assertStatus("connected")
	stop(w)
	w = start()
	assertStatus("connected")
	retained, err := s.GetRuntimeAllocation(t.Context(), tenant, environment.ID)
	if err != nil || retained.ID != owner.ID || retained.DeviceID != owner.DeviceID || p.creates != 1 {
		t.Fatal("restart replaced Runtime identity", err)
	}
	if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	reconcileManagedState(t, w, s, tenant, environment.ID, "released")
	if conn, err := dial(p.credential); err == nil {
		conn.Close()
		t.Fatal("released Runtime reconnected")
	}
}
