package codex

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func relayFixture(t *testing.T) (fixture, []string) {
	t.Helper()
	f := newFixture(t)
	tokens := []string{uuid.NewString(), uuid.NewString()}
	for i, token := range tokens {
		key := f.keys[i]
		key.TokenSHA256 = digest(token)
		f.registry.harnessKeys[sha256.Sum256([]byte(token))] = key
	}
	return f, tokens
}

func nativePOST(t *testing.T, f fixture, environment, route, token string, body any, status int, result any) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, f.server.URL+"/cloud/environment/"+environment+"/"+route, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		t.Fatalf("native %s status %d, expected %d", route, resp.StatusCode, status)
	}
	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			t.Fatal(err)
		}
	}
}
func harnessMaterial(t *testing.T, f fixture, token string) ConnectResponse {
	t.Helper()
	var result ConnectResponse
	nativePOST(t, f, f.keys[0].EnvironmentID, "connect", token, ConnectRequest{nativeRequest().ExecutorPublicKey}, 200, &result)
	if result.EnvironmentID != f.keys[0].EnvironmentID || result.ExecutorRegistrationID == "" || result.HarnessKeyAuthorization == "" || result.ExecutorPublicKey != nativeRequest().ExecutorPublicKey {
		t.Fatal("invalid native connect material")
	}
	return result
}
func validationBody(grant ConnectResponse) ValidationRequest {
	return ValidationRequest{grant.ExecutorRegistrationID, nativeRequest().ExecutorPublicKey, grant.HarnessKeyAuthorization}
}
func validateGrant(t *testing.T, f fixture, body ValidationRequest, want bool) {
	t.Helper()
	var result ValidationResponse
	nativePOST(t, f, f.keys[0].EnvironmentID, "validate", f.tokens[0], body, 200, &result)
	if result.Valid != want {
		t.Fatalf("native authorization %v, expected %v", result.Valid, want)
	}
}
func relayBytes(t *testing.T, source, target *websocket.Conn, data []byte) {
	t.Helper()
	if err := source.WriteMessage(websocket.BinaryMessage, data); err != nil {
		t.Fatal(err)
	}
	_ = target.SetReadDeadline(time.Now().Add(2 * time.Second))
	kind, got, err := target.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage || !bytes.Equal(data, got) {
		t.Fatal("opaque frame did not arrive unchanged")
	}
}
func expectClosed(t *testing.T, c *websocket.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * heartbeatInterval))
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("closed peer remained usable")
	}
}

func TestHarnessAuthorizationOpaqueRelayAndRefresh(t *testing.T) {
	f, tokens := relayFixture(t)
	k := f.keys[0]
	request := ConnectRequest{nativeRequest().ExecutorPublicKey}
	nativePOST(t, f, k.EnvironmentID, "connect", tokens[0], request, 503, nil)
	for _, token := range []string{f.tokens[0], tokens[1], "caller-or-device"} {
		nativePOST(t, f, k.EnvironmentID, "connect", token, request, 401, nil)
	}
	nativePOST(t, f, k.EnvironmentID, "register", tokens[0], nativeRequest(), 401, nil)
	reg := f.register(t, k.EnvironmentID, f.tokens[0], nativeRequest(), 200)
	executor := dial(t, reg.URL)
	defer executor.Close()
	grant := harnessMaterial(t, f, tokens[0])
	validateGrant(t, f, validationBody(grant), false)
	rejectSocket(t, strings.Replace(grant.URL, "/harness/", "/executor/", 1), 401)
	harness := dial(t, grant.URL)
	defer harness.Close()
	rejectSocket(t, grant.URL, 401)
	wrong := validationBody(grant)
	wrong.HarnessKeyAuthorization = "wrong"
	validateGrant(t, f, wrong, false)
	wrong = validationBody(grant)
	wrong.HarnessPublicKey.X25519 = nativeRequest().ExecutorPublicKey.X25519[0:1] + "Q" + nativeRequest().ExecutorPublicKey.X25519[2:]
	validateGrant(t, f, wrong, false)
	wrong = validationBody(grant)
	wrong.ExecutorRegistrationID = uuid.NewString()
	validateGrant(t, f, wrong, false)
	nativePOST(t, f, k.EnvironmentID, "validate", tokens[0], validationBody(grant), 401, nil)
	validateGrant(t, f, validationBody(grant), true)
	validateGrant(t, f, validationBody(grant), false)
	data := bytes.Repeat([]byte{0, 255, 81, 2}, maxRelayMessageSize/4)
	relayBytes(t, harness, executor, data)
	relayBytes(t, executor, harness, []byte{0, 0, 83, 254})
	refresh := harnessMaterial(t, f, tokens[0])
	if refresh.ExecutorRegistrationID != grant.ExecutorRegistrationID {
		t.Fatal("refresh changed executor registration")
	}
	rejectSocket(t, refresh.URL, 409)
	validateGrant(t, f, validationBody(refresh), false)
	relayBytes(t, harness, executor, []byte("healthy after same-key refresh"))
	f.registry.mu.Lock()
	for _, g := range f.registry.registrations[k.EnvironmentID].grants {
		g.expires = time.Now().Add(-time.Second)
	}
	f.registry.mu.Unlock()
	rejectSocket(t, refresh.URL, 401)
	relayBytes(t, executor, harness, []byte("expiry must not terminate an established pair"))
	harness.Close()
	expectClosed(t, executor)
	awaitPresence(t, f, k.EnvironmentID, false)
	rejectSocket(t, grant.URL, 401)
	rejectSocket(t, refresh.URL, 401)
	next := dial(t, reg.URL)
	defer next.Close()
	validateGrant(t, f, validationBody(grant), false)
	fresh := harnessMaterial(t, f, tokens[0])
	resumed := dial(t, fresh.URL)
	defer resumed.Close()
	relayBytes(t, resumed, next, []byte("fresh generation"))
}

func TestHarnessPairClosesOnReplacementDeletionOwnershipOrPeerLoss(t *testing.T) {
	for _, cause := range []string{"executor", "replacement", "deleted", "lease"} {
		t.Run(cause, func(t *testing.T) {
			f, tokens := relayFixture(t)
			k := f.keys[0]
			reg := f.register(t, k.EnvironmentID, f.tokens[0], nativeRequest(), 200)
			executor := dial(t, reg.URL)
			defer executor.Close()
			grant := harnessMaterial(t, f, tokens[0])
			harness := dial(t, grant.URL)
			defer harness.Close()
			switch cause {
			case "executor":
				executor.Close()
			case "replacement":
				replacement := f.register(t, k.EnvironmentID, f.tokens[0], nativeRequest(), 200)
				next := dial(t, replacement.URL)
				defer next.Close()
				nextGrant := harnessMaterial(t, f, tokens[0])
				nextHarness := dial(t, nextGrant.URL)
				defer nextHarness.Close()
				relayBytes(t, nextHarness, next, []byte("replacement survives stale callbacks"))
				validateGrant(t, f, validationBody(grant), false)
			default:
				f.source.mu.Lock()
				if cause == "deleted" {
					delete(f.source.values, k.EnvironmentID)
				} else {
					f.source.lost = true
				}
				f.source.mu.Unlock()
			}
			expectClosed(t, harness)
			expectClosed(t, executor)
			rejectSocket(t, grant.URL, 401)
		})
	}
}

func TestHarnessGrantsAreBoundedAndPruneExpiredPending(t *testing.T) {
	f, tokens := relayFixture(t)
	reg := f.register(t, f.keys[0].EnvironmentID, f.tokens[0], nativeRequest(), 200)
	executor := dial(t, reg.URL)
	defer executor.Close()
	for range maxHarnessGrants {
		harnessMaterial(t, f, tokens[0])
	}
	nativePOST(t, f, f.keys[0].EnvironmentID, "connect", tokens[0], ConnectRequest{nativeRequest().ExecutorPublicKey}, 429, nil)
	f.registry.mu.Lock()
	for _, grant := range f.registry.registrations[f.keys[0].EnvironmentID].grants {
		grant.expires = time.Now().Add(-time.Second)
	}
	f.registry.mu.Unlock()
	fresh := harnessMaterial(t, f, tokens[0])
	c := dial(t, fresh.URL)
	c.Close()
	expectClosed(t, executor)
}

func TestHarnessPairRejectsTextOversizeAndBackpressure(t *testing.T) {
	for _, fault := range []string{"text", "oversize", "backpressure"} {
		t.Run(fault, func(t *testing.T) {
			f, tokens := relayFixture(t)
			reg := f.register(t, f.keys[0].EnvironmentID, f.tokens[0], nativeRequest(), 200)
			executor := dial(t, reg.URL)
			defer executor.Close()
			grant := harnessMaterial(t, f, tokens[0])
			harness := dial(t, grant.URL)
			defer harness.Close()
			switch fault {
			case "text":
				_ = harness.WriteMessage(websocket.TextMessage, []byte("invalid"))
			case "oversize":
				_ = harness.WriteMessage(websocket.BinaryMessage, make([]byte, maxRelayMessageSize+1))
			case "backpressure":
				_ = harness.SetWriteDeadline(time.Now().Add(2 * relayWriteTimeout))
				data := make([]byte, maxRelayMessageSize)
				for range 256 {
					if err := harness.WriteMessage(websocket.BinaryMessage, data); err != nil {
						break
					}
				}
			}
			expectClosed(t, harness)
			// The stalled destination may retain already-forwarded frames in its kernel buffer.
			awaitPresence(t, f, f.keys[0].EnvironmentID, false)
		})
	}
}

func TestHarnessCredentialCannotRegisterExecutor(t *testing.T) {
	f, tokens := relayFixture(t)
	f.register(t, f.keys[0].EnvironmentID, tokens[0], nativeRequest(), 401)
}
