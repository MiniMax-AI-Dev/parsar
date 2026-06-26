package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/runtimecrypto"
)

// Regression for the "runner_public_key required" pair failure: the
// daemon used to never populate that field on the wire, so server-side
// pairing rejected with HTTP 400. Asserts the request carries a
// base64(stdEncoding) X25519 pubkey AND the resulting Profile retains
// the matching privkey for later OpenSeal.
func TestPairProfileGeneratesAndSendsRunnerPublicKey(t *testing.T) {
	var sentPubKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/runtimes/pair" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		var body pairRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		sentPubKey = body.RunnerPublicKey
		_ = json.NewEncoder(w).Encode(pairResponse{
			Runtime: pairRuntime{
				ID:   "rt_test_123",
				Type: "agent_daemon",
				Name: "test-device",
			},
			RunnerCredential: "rc_test_secret",
		})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	prof, _, err := pairProfile(ctx, srv.URL, "pairing-token", "test-device")
	if err != nil {
		t.Fatalf("pairProfile: %v", err)
	}

	// Server must have received a non-empty key.
	if sentPubKey == "" {
		t.Fatal("server did not receive runner_public_key on the wire")
	}
	// And it must be a valid X25519 pubkey.
	if _, err := runtimecrypto.DecodeKey(sentPubKey); err != nil {
		t.Fatalf("server received invalid pubkey %q: %v", sentPubKey, err)
	}

	// Profile must carry both halves.
	if prof.RunnerPublicKey != sentPubKey {
		t.Errorf("Profile.RunnerPublicKey = %q, want sent %q", prof.RunnerPublicKey, sentPubKey)
	}
	if prof.RunnerPrivateKey == "" {
		t.Fatal("Profile.RunnerPrivateKey is empty")
	}
	if _, err := runtimecrypto.DecodeKey(prof.RunnerPrivateKey); err != nil {
		t.Errorf("Profile.RunnerPrivateKey is not a valid X25519 key: %v", err)
	}

	// Sanity-check the rest of the Profile so a future refactor that
	// drops one of these fields fails loudly.
	if prof.RuntimeID != "rt_test_123" {
		t.Errorf("RuntimeID = %q, want rt_test_123", prof.RuntimeID)
	}
	if prof.RunnerCredential != "rc_test_secret" {
		t.Errorf("RunnerCredential = %q, want rc_test_secret", prof.RunnerCredential)
	}
}

// Pair-error pass-through must still surface a 400 when the server
// fails the pair for any reason (reused token, device limit, etc.).
func TestPairProfileSurfacesServerPairFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"pair_failed","message":"token reused"}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, err := pairProfile(ctx, srv.URL, "pairing-token", "test-device")
	if err == nil {
		t.Fatal("pairProfile succeeded against a 400 server response, want error")
	}
}
