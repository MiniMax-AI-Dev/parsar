package codex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type environmentFixture struct {
	mu     sync.Mutex
	values map[string]string
	lost   bool
}

func (s *environmentFixture) GetEnvironment(ctx context.Context, tenant, id string) (store.Environment, error) {
	if err := ctx.Err(); err != nil {
		return store.Environment{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values[id] != tenant {
		return store.Environment{}, store.ErrNotFound
	}
	return store.Environment{ID: id, TenantID: tenant}, nil
}
func (s *environmentFixture) owner(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lost {
		return errors.New("lost")
	}
	return nil
}

func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func nativeRequest() RegistrationRequest {
	return RegistrationRequest{SecurityProfile: securityProfile, ExecutorPublicKey: PublicKey{Suite: noiseSuite, X25519: base64.StdEncoding.EncodeToString(make([]byte, 32)), MLKEM768: base64.StdEncoding.EncodeToString(make([]byte, 1184))}}
}

type fixture struct {
	registry *Registry
	server   *httptest.Server
	source   *environmentFixture
	keys     []ScopedKey
	tokens   []string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	source := &environmentFixture{values: map[string]string{}}
	keys, tokens := []ScopedKey{}, []string{}
	for range 2 {
		token := uuid.NewString()
		k := ScopedKey{TokenSHA256: digest(token), TenantID: uuid.NewString(), EnvironmentID: uuid.NewString()}
		keys = append(keys, k)
		tokens = append(tokens, token)
		source.values[k.EnvironmentID] = k.TenantID
	}
	server := httptest.NewUnstartedServer(nil)
	registry, err := New(Config{Store: source, CheckOwnership: source.owner, PublicURL: "http://" + server.Listener.Addr().String(), Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = registry.Handler()
	server.Start()
	t.Cleanup(func() { registry.Close(); server.Close() })
	return fixture{registry, server, source, keys, tokens}
}
func (f fixture) register(t *testing.T, environment, token string, body any, expected int) RegistrationResponse {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, f.server.URL+"/cloud/environment/"+environment+"/register", bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != expected {
		t.Fatalf("registration status %d, expected %d", resp.StatusCode, expected)
	}
	var result RegistrationResponse
	if expected == 200 {
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if result.EnvironmentID != environment || result.ExecutorRegistrationID == "" || result.SecurityProfile != securityProfile {
			t.Fatal("invalid native response")
		}
	}
	return result
}
func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	c, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		if resp != nil {
			t.Fatalf("socket rejected: %d", resp.StatusCode)
		}
		t.Fatal("socket failed")
	}
	return c
}
func rejectSocket(t *testing.T, url string, status int) {
	t.Helper()
	c, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if c != nil {
		c.Close()
	}
	if resp != nil {
		defer resp.Body.Close()
	}
	if err == nil || resp == nil || resp.StatusCode != status {
		t.Fatal("unexpected socket authorization outcome")
	}
}
func awaitPresence(t *testing.T, f fixture, id string, want bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		connected, err := f.registry.Connected(t.Context(), f.keys[0].TenantID, id)
		if err == nil && connected == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("presence did not converge")
}

func TestRegistrationScopesAndRealSocketReplacement(t *testing.T) {
	f := newFixture(t)
	first := f.keys[0]
	f.register(t, first.EnvironmentID, f.tokens[1], nativeRequest(), 401)
	f.register(t, first.EnvironmentID, "caller-or-device-key", nativeRequest(), 401)
	f.register(t, uuid.NewString(), f.tokens[0], nativeRequest(), 401)
	bad := nativeRequest()
	bad.ExecutorPublicKey.Suite = "wrong"
	f.register(t, first.EnvironmentID, f.tokens[0], bad, 400)
	reg := f.register(t, first.EnvironmentID, f.tokens[0], nativeRequest(), 200)
	rejectSocket(t, reg.URL+"invalid", 401)
	c := dial(t, reg.URL)
	defer c.Close()
	awaitPresence(t, f, first.EnvironmentID, true)
	rejectSocket(t, reg.URL, 409)
	replacement := f.register(t, first.EnvironmentID, f.tokens[0], nativeRequest(), 200)
	rejectSocket(t, reg.URL, 401)
	next := dial(t, replacement.URL)
	defer next.Close()
	awaitPresence(t, f, first.EnvironmentID, true)
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("replaced socket survived")
	}
	awaitPresence(t, f, first.EnvironmentID, true)
	next.Close()
	awaitPresence(t, f, first.EnvironmentID, false)
	reconnect := dial(t, replacement.URL)
	reconnect.Close()
	awaitPresence(t, f, first.EnvironmentID, false)
	if _, err := f.registry.Connected(t.Context(), f.keys[1].TenantID, first.EnvironmentID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("foreign presence visible")
	}
}

func TestExpiredTicketAndClosedRegistryRejectAccess(t *testing.T) {
	f := newFixture(t)
	k := f.keys[0]
	reg := f.register(t, k.EnvironmentID, f.tokens[0], nativeRequest(), 200)
	f.registry.mu.Lock()
	f.registry.registrations[k.EnvironmentID].expires = time.Now().Add(-time.Second)
	f.registry.mu.Unlock()
	rejectSocket(t, reg.URL, 401)
	f.registry.Close()
	rejectSocket(t, reg.URL, 401)
	f.register(t, k.EnvironmentID, f.tokens[0], nativeRequest(), 503)
}

func TestOwnershipLossAndDeletionClosePresence(t *testing.T) {
	for _, loss := range []string{"deleted", "lease"} {
		t.Run(loss, func(t *testing.T) {
			f := newFixture(t)
			k := f.keys[0]
			reg := f.register(t, k.EnvironmentID, f.tokens[0], nativeRequest(), 200)
			c := dial(t, reg.URL)
			defer c.Close()
			f.source.mu.Lock()
			if loss == "deleted" {
				delete(f.source.values, k.EnvironmentID)
			} else {
				f.source.lost = true
			}
			f.source.mu.Unlock()
			_ = c.SetReadDeadline(time.Now().Add(2 * heartbeatInterval))
			if _, _, err := c.ReadMessage(); err == nil {
				t.Fatal("unauthorized socket survived")
			}
			expected := 404
			if loss == "lease" {
				expected = 503
			}
			f.register(t, k.EnvironmentID, f.tokens[0], nativeRequest(), expected)
		})
	}
}

func TestExecutorPresenceHasNoCommandRelay(t *testing.T) {
	f := newFixture(t)
	k := f.keys[0]
	reg := f.register(t, k.EnvironmentID, f.tokens[0], nativeRequest(), 200)
	c := dial(t, reg.URL)
	defer c.Close()
	if err := c.WriteMessage(websocket.BinaryMessage, []byte("no command relay")); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err := c.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseUnsupportedData) {
		t.Fatal("command traffic accepted")
	}
	awaitPresence(t, f, k.EnvironmentID, false)
}

func TestExecutorConfigRejectsUnsafeOrAmbiguousBindings(t *testing.T) {
	f := newFixture(t)
	base := Config{Store: f.source, CheckOwnership: f.source.owner, PublicURL: f.server.URL, Keys: f.keys}
	for _, url := range []string{"http://executor.example", "https://user:secret@example", "https://example/path", "https://example?token=x", "ws://localhost"} {
		c := base
		c.PublicURL = url
		if _, err := New(c); err == nil {
			t.Fatal("unsafe URL accepted")
		}
	}
	c := base
	c.Keys = append(c.Keys, c.Keys[0])
	if _, err := New(c); err == nil {
		t.Fatal("duplicate scope accepted")
	}
	c = base
	c.Keys = []ScopedKey{{TokenSHA256: strings.Repeat("g", 64), TenantID: uuid.NewString(), EnvironmentID: uuid.NewString()}}
	if _, err := New(c); err == nil {
		t.Fatal("invalid digest accepted")
	}
}

func TestRegistrationCallerCancellationKeepsOtherConnections(t *testing.T) {
	f := newFixture(t)
	k := f.keys[0]
	reg := f.register(t, k.EnvironmentID, f.tokens[0], nativeRequest(), 200)
	c := dial(t, reg.URL)
	defer c.Close()
	awaitPresence(t, f, k.EnvironmentID, true)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.registry.Connected(ctx, k.TenantID, k.EnvironmentID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	awaitPresence(t, f, k.EnvironmentID, true)
}
